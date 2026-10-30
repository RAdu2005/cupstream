package gateway

import (
	"context"
	"net/http"
	"testing"
	"time"

	"github.com/RAdu2005/cupstream/server/internal/mediamtx"
	"github.com/RAdu2005/cupstream/server/internal/mediamtx/mtxtest"
)

// eventually waits for cond, failing the test after a few seconds.
func eventually(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for !cond() {
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for %s", what)
		}
		time.Sleep(5 * time.Millisecond)
	}
}

// follow runs the gateway's event loop until the test ends or the stream
// drops; the returned channel receives its error.
func (f *fixture) follow(t *testing.T) <-chan error {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	done := make(chan error, 1)
	subs := f.mtx.EventSubscribers()
	go func() { done <- f.gw.follow(ctx) }()
	eventually(t, "the event subscription", func() bool { return f.mtx.EventSubscribers() > subs })
	return done
}

func TestEventsDriveLiveState(t *testing.T) {
	f := newFixture(t)
	f.gw.DetectFeatures(context.Background())
	if !f.gw.Features().Events {
		t.Fatal("cupstream.2 has the event stream")
	}
	done := f.follow(t)

	// Going live and viewers joining and leaving arrive as events: no
	// Reconcile call in between.
	st := f.publish(t)
	eventually(t, "live", func() bool { return f.state(t, st.Stream.ID) == Live })
	v := f.play(t, st, f.user.ID, "", http.StatusCreated)
	if n := f.viewers(t, st); n != 1 {
		t.Fatalf("viewers = %d, want 1", n)
	}
	f.mtx.Drop(v.mtxID)
	eventually(t, "the viewer to leave", func() bool { return f.viewers(t, st) == 0 })

	pub := f.mtx.Sessions("s/" + st.Stream.ID)[0]
	f.mtx.Drop(pub.ID)
	eventually(t, "reconnecting", func() bool { return f.state(t, st.Stream.ID) == Reconnecting })

	// The publisher comes back, then MediaMTX restarts: changes during the
	// outage are picked up by the re-synchronization after reconnecting.
	f.post(t, st, "Bearer "+st.Token, http.StatusCreated)
	eventually(t, "live again", func() bool { return f.state(t, st.Stream.ID) == Live })
	v = f.play(t, st, f.user.ID, "", http.StatusCreated)
	f.mtx.DisconnectEvents()
	if err := <-done; err == nil {
		t.Fatal("follow returned without an error")
	}
	f.mtx.DropSilently(v.mtxID)
	if n := f.viewers(t, st); n != 1 {
		t.Fatalf("viewers while disconnected = %d, want the stale 1", n)
	}
	f.follow(t)
	eventually(t, "the re-synchronization", func() bool { return f.viewers(t, st) == 0 })
	if got := f.state(t, st.Stream.ID); got != Live {
		t.Fatalf("after re-synchronizing: %s", got)
	}
}

func TestEarlySessionEvents(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()

	// MediaMTX announces a session before answering: it is confirmed as soon
	// as it is recorded.
	f.gw.HandleEvent(ctx, sessionEvent(mediamtx.EventSessionCreated, "early"))
	f.gw.addSession(&session{mtxID: "early", streamID: "x", created: f.clock})
	if s := f.gw.sessions["early"]; s == nil || !s.confirmed {
		t.Fatalf("session %+v, want it recorded and confirmed", s)
	}

	// A session that closed before its answer was handled is not recorded.
	f.gw.HandleEvent(ctx, sessionEvent(mediamtx.EventSessionClosed, "gone"))
	f.gw.addSession(&session{mtxID: "gone", streamID: "x", created: f.clock})
	if _, ok := f.gw.sessions["gone"]; ok {
		t.Fatal("a closed session was recorded")
	}

	// Unmatched early events expire.
	f.gw.HandleEvent(ctx, sessionEvent(mediamtx.EventSessionCreated, "stray"))
	f.clock = f.clock.Add(earlyEventTTL + time.Second)
	f.gw.expire(ctx)
	if len(f.gw.early) != 0 {
		t.Fatalf("early events left: %v", f.gw.early)
	}
}

func sessionEvent(typ, id string) mediamtx.Event {
	e := mediamtx.Event{Type: typ}
	e.Reader = &struct {
		Type string `json:"type"`
		ID   string `json:"id"`
	}{"webRTCSession", id}
	return e
}

func TestPollingWithoutEvents(t *testing.T) {
	f := newFixture(t)
	f.mtx.SetVersion("v1.21.2")
	f.gw.DetectFeatures(context.Background())
	if f := f.gw.Features(); f.Events || f.Pause {
		t.Fatalf("upstream MediaMTX reported features %+v", f)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go f.gw.poll(ctx, time.Minute)

	st := f.publish(t)
	eventually(t, "live by polling", func() bool { return f.state(t, st.Stream.ID) == Live })
}

func TestStats(t *testing.T) {
	f := newFixture(t)
	st := f.publish(t)
	path := "s/" + st.Stream.ID
	v := f.play(t, st, f.user.ID, "", http.StatusCreated)
	var pub mtxtest.Session
	for _, s := range f.mtx.Sessions(path) {
		if s.State == "publish" {
			pub = s
		}
	}
	f.mtx.SetKeyFrameInterval(path, 8.5)
	f.reconcile(t)

	f.mtx.Update(pub.ID, func(s *mtxtest.Session) {
		s.InboundBytes, s.InboundRTPPackets, s.InboundRTPPacketsLost = 1_000_000, 990, 10
	})
	f.mtx.Update(v.mtxID, func(s *mtxtest.Session) { s.OutboundBytes, s.OutboundLossRatio = 500_000, 0.02 })
	f.clock = f.clock.Add(2 * time.Second)
	f.reconcile(t)

	ls, _ := f.gw.Stream(st.Stream.ID)
	if ls.Stats.Bitrate != 4_000_000 || ls.Stats.LossRatio != 0.01 || ls.Stats.KeyFrameInterval != 8500*time.Millisecond {
		t.Fatalf("stream stats %+v", ls.Stats)
	}
	vs := f.gw.ViewerStats(st.Stream.ID)
	if len(vs) != 1 || vs[0].Bitrate != 2_000_000 || vs[0].LossRatio != 0.02 || !vs[0].Watching || vs[0].UserID != f.user.ID {
		t.Fatalf("viewer stats %+v", vs)
	}
}
