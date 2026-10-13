package gateway

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/RAdu2005/cupstream/server/internal/auth"
	"github.com/RAdu2005/cupstream/server/internal/mediamtx"
	"github.com/RAdu2005/cupstream/server/internal/mediamtx/mtxtest"
	"github.com/RAdu2005/cupstream/server/internal/oauth"
	"github.com/RAdu2005/cupstream/server/internal/rooms"
	"github.com/RAdu2005/cupstream/server/internal/store"
)

const testOffer = "v=0\r\nm=video 9 UDP/TLS/RTP/SAVPF 96\r\na=rtpmap:96 H264/90000\r\n"

type fixture struct {
	db    *store.DB
	mtx   *mtxtest.Fake
	gw    *Gateway
	clock time.Time
	user  store.User
}

func newFixture(t *testing.T) *fixture {
	t.Helper()
	ctx := context.Background()
	db, err := store.Open(ctx, ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	f := &fixture{db: db, mtx: mtxtest.New(t), clock: time.Unix(1_800_000_000, 0)}
	f.gw = f.newGateway()
	f.user, _, err = auth.NewAccounts(db, nil).SignIn(ctx, oauth.Identity{Provider: "dev", Subject: "alice", Username: "alice"})
	if err != nil {
		t.Fatal(err)
	}
	return f
}

func (f *fixture) newGateway() *Gateway {
	gw := New(f.db, mediamtx.New(f.mtx.URL, f.mtx.URL), rooms.New(f.db), f.mtx.URL)
	gw.now = func() time.Time { return f.clock }
	return gw
}

func (f *fixture) reconcile(t *testing.T) {
	t.Helper()
	if err := f.gw.Reconcile(context.Background()); err != nil {
		t.Fatal(err)
	}
}

// publish starts a web stream and publishes to it through the gateway.
func (f *fixture) publish(t *testing.T) Started {
	t.Helper()
	st, err := f.gw.Start(context.Background(), StartRequest{User: f.user, Source: SourceWeb, LinkFallback: true})
	if err != nil {
		t.Fatal(err)
	}
	f.post(t, st, "Bearer "+st.Token, http.StatusCreated)
	return st
}

func (f *fixture) post(t *testing.T, st Started, authz string, want int) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, "/api/streams/"+st.Stream.ID+"/whip", strings.NewReader(testOffer))
	req.SetPathValue("id", st.Stream.ID)
	req.Header.Set("Content-Type", "application/sdp")
	req.Header.Set("Authorization", authz)
	rec := httptest.NewRecorder()
	f.gw.HandleTokenPublish(rec, req)
	if rec.Code != want {
		t.Fatalf("publish = %d %s, want %d", rec.Code, rec.Body, want)
	}
	return rec
}

func (f *fixture) state(t *testing.T, id string) State {
	t.Helper()
	ls, ok := f.gw.Stream(id)
	if !ok {
		return "ended"
	}
	return ls.State
}

func (f *fixture) endReason(t *testing.T, id string) string {
	t.Helper()
	s, err := f.db.GetStream(context.Background(), id)
	if err != nil {
		t.Fatal(err)
	}
	return s.EndReason.String
}

func TestLifecycle(t *testing.T) {
	f := newFixture(t)
	st := f.publish(t)
	if got := f.state(t, st.Stream.ID); got != Starting {
		t.Fatalf("after publish: %s", got)
	}
	f.reconcile(t)
	if got := f.state(t, st.Stream.ID); got != Live {
		t.Fatalf("after reconcile: %s", got)
	}

	// The publisher drops: reconnecting, then ended after the grace period.
	pub := f.mtx.Sessions("s/" + st.Stream.ID)[0]
	f.mtx.Drop(pub.ID)
	f.reconcile(t)
	if got := f.state(t, st.Stream.ID); got != Reconnecting {
		t.Fatalf("after drop: %s", got)
	}
	f.clock = f.clock.Add(reconnectGrace - time.Second)
	f.reconcile(t)
	if got := f.state(t, st.Stream.ID); got != Reconnecting {
		t.Fatalf("within grace: %s", got)
	}

	// Reconnecting with the same token resumes the same stream.
	f.post(t, st, "Bearer "+st.Token, http.StatusCreated)
	f.reconcile(t)
	if got := f.state(t, st.Stream.ID); got != Live {
		t.Fatalf("after reconnect: %s", got)
	}

	f.mtx.Drop(f.mtx.Sessions("s/" + st.Stream.ID)[0].ID)
	f.reconcile(t)
	f.clock = f.clock.Add(reconnectGrace + time.Second)
	f.reconcile(t)
	if got := f.state(t, st.Stream.ID); got != "ended" {
		t.Fatalf("after grace: %s", got)
	}
	if r := f.endReason(t, st.Stream.ID); r != EndDisconnected {
		t.Fatalf("end reason %q", r)
	}
}

func TestNeverPublished(t *testing.T) {
	f := newFixture(t)
	st, err := f.gw.Start(context.Background(), StartRequest{User: f.user, Source: SourceWeb, LinkFallback: true})
	if err != nil {
		t.Fatal(err)
	}
	f.clock = f.clock.Add(startTimeout + time.Second)
	f.reconcile(t)
	if r := f.endReason(t, st.Stream.ID); r != EndFailedToStart {
		t.Fatalf("end reason %q", r)
	}
}

func TestWrongToken(t *testing.T) {
	f := newFixture(t)
	st, _ := f.gw.Start(context.Background(), StartRequest{User: f.user, Source: SourceWeb, LinkFallback: true})
	f.post(t, st, "Bearer nope", http.StatusUnauthorized)
	f.post(t, st, "", http.StatusUnauthorized)
}

func TestRestoreAfterRestart(t *testing.T) {
	f := newFixture(t)
	live := f.publish(t)
	f.reconcile(t)

	// A second stream by another user whose publisher is gone during the restart.
	bob, _, _ := auth.NewAccounts(f.db, nil).SignIn(context.Background(), oauth.Identity{Provider: "dev", Subject: "bob", Username: "bob"})
	gone, _ := f.gw.Start(context.Background(), StartRequest{User: bob, Source: SourceWeb, LinkFallback: true})

	f.gw = f.newGateway() // restart: in-memory state is lost
	if err := f.gw.Restore(context.Background()); err != nil {
		t.Fatal(err)
	}
	f.reconcile(t)
	if got := f.state(t, live.Stream.ID); got != Live {
		t.Fatalf("restored stream: %s", got)
	}
	f.clock = f.clock.Add(reconnectGrace + time.Second)
	f.reconcile(t)
	if got := f.state(t, gone.Stream.ID); got != "ended" {
		t.Fatalf("stream without publisher: %s", got)
	}
	// The publisher session was re-adopted, not kicked.
	if k := f.mtx.Kicked(); len(k) != 0 {
		t.Fatalf("kicked %v", k)
	}
}

func TestStrayMediaSessionIsKicked(t *testing.T) {
	f := newFixture(t)
	// A publisher on a path the gateway does not know (e.g. its stream ended).
	resp, err := http.Post(f.mtx.URL.String()+"/s/unknownstream000/whip", "application/sdp", strings.NewReader(testOffer))
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	f.reconcile(t)
	if k := f.mtx.Kicked(); len(k) != 1 {
		t.Fatalf("kicked %v, want the stray session", k)
	}
}

func TestOnlyTricklePatches(t *testing.T) {
	f := newFixture(t)
	st := f.publish(t)
	sess := f.mtx.Sessions("s/" + st.Stream.ID)[0]
	patch := func(ct string) int {
		req := httptest.NewRequest(http.MethodPatch, "/", strings.NewReader(`{"paused":true}`))
		req.SetPathValue("stream", st.Stream.ID)
		req.SetPathValue("kind", "whip")
		req.SetPathValue("secret", sess.Secret)
		req.Header.Set("Content-Type", ct)
		rec := httptest.NewRecorder()
		f.gw.HandleSession(rec, req)
		return rec.Code
	}
	if c := patch("application/json"); c != http.StatusUnsupportedMediaType {
		t.Fatalf("JSON PATCH = %d, want 415", c)
	}
	if c := patch("application/trickle-ice-sdpfrag"); c != http.StatusNoContent {
		t.Fatalf("trickle PATCH = %d, want 204", c)
	}
	if p := f.mtx.Sessions("s/" + st.Stream.ID)[0].Patches; len(p) != 1 {
		t.Fatalf("MediaMTX got %d PATCHes, want 1", len(p))
	}
}

func TestViewerDeleteUpdatesCount(t *testing.T) {
	f := newFixture(t)
	st := f.publish(t)
	f.reconcile(t)

	req := httptest.NewRequest(http.MethodPost, "/", strings.NewReader(testOffer))
	req.SetPathValue("id", st.Stream.ID)
	req.Header.Set("Content-Type", "application/sdp")
	rec := httptest.NewRecorder()
	f.gw.Play(rec, req, f.user.ID)
	if rec.Code != http.StatusCreated {
		t.Fatalf("play = %d %s", rec.Code, rec.Body)
	}
	if ls, _ := f.gw.Stream(st.Stream.ID); ls.Viewers != 1 {
		t.Fatalf("viewers = %d, want 1", ls.Viewers)
	}

	parts := strings.Split(rec.Header().Get("Location"), "/")
	del := httptest.NewRequest(http.MethodDelete, "/", nil)
	del.SetPathValue("stream", st.Stream.ID)
	del.SetPathValue("kind", "whep")
	del.SetPathValue("secret", parts[len(parts)-1])
	f.gw.HandleSession(httptest.NewRecorder(), del)
	if ls, _ := f.gw.Stream(st.Stream.ID); ls.Viewers != 0 {
		t.Fatalf("viewers after DELETE = %d, want 0", ls.Viewers)
	}
}
