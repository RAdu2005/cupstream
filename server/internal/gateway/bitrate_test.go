package gateway

import (
	"context"
	"net/http"
	"testing"
	"time"

	"github.com/RAdu2005/cupstream/server/internal/access"
	"github.com/RAdu2005/cupstream/server/internal/mediamtx/mtxtest"
)

func TestBitrateControlStep(t *testing.T) {
	t0 := time.Unix(1_800_000_000, 0)
	at := func(s float64) time.Time { return t0.Add(time.Duration(s * float64(time.Second))) }
	var c bitrateControl

	// Calm viewers: no limit.
	if c = c.step(4, 0, 6_000_000, at(0)); c.Bitrate != 0 {
		t.Fatalf("calm: %+v", c.Target)
	}
	// One of four struggling is not enough.
	if c = c.step(4, 1, 6_000_000, at(2)); c.Bitrate != 0 {
		t.Fatalf("one of four: %+v", c.Target)
	}
	// Two of four: 85% of what the sender sends.
	if c = c.step(4, 2, 6_000_000, at(4)); c.Bitrate != 5_100_000 || c.Congested != 2 {
		t.Fatalf("two of four: %+v", c.Target)
	}
	// Not again until the sender had time to apply it.
	if c = c.step(4, 2, 6_000_000, at(6)); c.Bitrate != 5_100_000 {
		t.Fatalf("within the holdoff: %+v", c.Target)
	}
	// Then from the lower of the target and what arrives.
	if c = c.step(4, 2, 5_000_000, at(8)); c.Bitrate != 4_250_000 {
		t.Fatalf("second fall: %+v", c.Target)
	}
	// Never below the minimum.
	for i := range 20 {
		c = c.step(1, 1, 0, at(12+4*float64(i)))
	}
	if c.Bitrate != MinTargetBitrate {
		t.Fatalf("floor: %+v", c.Target)
	}

	// Recovery: a rise after 10 calm seconds, and again 10 seconds later.
	calm := 100.0
	if c = c.step(1, 0, 1_000_000, at(calm)); c.Bitrate != MinTargetBitrate {
		t.Fatalf("calm at once: %+v", c.Target)
	}
	if c = c.step(1, 0, 1_000_000, at(calm+9)); c.Bitrate != MinTargetBitrate {
		t.Fatalf("calm 9 s: %+v", c.Target)
	}
	if c = c.step(1, 0, 1_000_000, at(calm+10)); c.Bitrate != 1_150_000 {
		t.Fatalf("calm 10 s: %+v", c.Target)
	}
	if c = c.step(1, 0, 1_000_000, at(calm+15)); c.Bitrate != 1_150_000 {
		t.Fatalf("calm 15 s: %+v", c.Target)
	}
	if c = c.step(1, 0, 1_000_000, at(calm+20)); c.Bitrate != 1_322_500 {
		t.Fatalf("calm 20 s: %+v", c.Target)
	}
	// A sender far below its target is not limited by it: lifted.
	for s := calm + 30; c.Bitrate != 0 && s < calm+300; s += 10 {
		c = c.step(1, 0, 1_000_000, at(s))
	}
	if c.Bitrate != 0 {
		t.Fatalf("never lifted: %+v", c.Target)
	}

	// Without a measured bitrate there is nothing to go by.
	if c := (bitrateControl{}).step(1, 1, 0, at(0)); c.Bitrate != 0 {
		t.Fatalf("no measurement: %+v", c.Target)
	}
}

// refresh advances the clock and updates statistics.
func (f *fixture) refresh(t *testing.T, d time.Duration) {
	t.Helper()
	f.clock = f.clock.Add(d)
	if err := f.gw.RefreshStats(context.Background()); err != nil {
		t.Fatal(err)
	}
}

func (f *fixture) target(t *testing.T, st Started) Target {
	t.Helper()
	tg, ok := f.gw.BitrateTarget(st.Stream.ID)
	if !ok {
		t.Fatal("no target")
	}
	return tg
}

// sending makes the publisher of st report a steady 6 Mbps.
func (f *fixture) sending(st Started) {
	for _, s := range f.mtx.Sessions("s/" + st.Stream.ID) {
		if s.State == "publish" {
			f.mtx.Update(s.ID, func(s *mtxtest.Session) { s.InboundBytes += 1_500_000 })
		}
	}
}

func TestBitrateTarget(t *testing.T) {
	f := newFixture(t)
	f.gw.DetectFeatures(context.Background())
	f.setVisibility(t, access.Everyone)
	st := f.publish(t)
	f.reconcile(t)

	var viewers []viewerSession
	for _, tab := range []string{"tab-aaaaaaaa", "tab-bbbbbbbb", "tab-cccccccc"} {
		viewers = append(viewers, f.play(t, st, "", "tab="+tab, http.StatusCreated))
	}
	setLoss := func(v viewerSession, loss float64) {
		f.mtx.Update(v.mtxID, func(s *mtxtest.Session) { s.OutboundLossRatio = loss })
	}
	tick := func() {
		f.sending(st)
		f.refresh(t, 2*time.Second)
	}
	tick()
	if tg := f.target(t, st); tg.Bitrate != 0 || tg.Viewers != 3 || tg.Congested != 0 {
		t.Fatalf("calm: %+v", tg)
	}

	// One of three viewers losing 20% is more than a quarter: the target
	// falls below the 6 Mbps the sender sends.
	setLoss(viewers[0], 0.2)
	tick()
	if tg := f.target(t, st); tg.Bitrate != 5_100_000 || tg.Congested != 1 {
		t.Fatalf("congested: %+v", tg)
	}

	// Growing delay counts too, measured against the lowest round trip.
	setLoss(viewers[0], 0)
	for range 4 {
		tick() // the smoothed loss fades
	}
	f.mtx.Update(viewers[1].mtxID, func(s *mtxtest.Session) { s.OutboundRTT = 0.04 })
	tick()
	if tg := f.target(t, st); tg.Congested != 0 {
		t.Fatalf("a low round trip: %+v", tg)
	}
	f.mtx.Update(viewers[1].mtxID, func(s *mtxtest.Session) { s.OutboundRTT = 0.4 })
	tick()
	if tg := f.target(t, st); tg.Congested != 1 {
		t.Fatalf("growing delay: %+v", tg)
	}

	// Paused viewers are not viewers.
	if code := f.control(t, st, viewers[1], "pause", ""); code != http.StatusNoContent {
		t.Fatalf("pause = %d", code)
	}
	tick()
	if tg := f.target(t, st); tg.Viewers != 2 || tg.Congested != 0 {
		t.Fatalf("after pausing: %+v", tg)
	}
}

// A viewer who struggles with every stream they watch is over their own
// capacity and does not lower anyone's quality.
func TestBitrateTargetIgnoresViewersOverCapacity(t *testing.T) {
	f := newFixture(t)
	f.setVisibility(t, access.Everyone)
	a := f.publish(t)
	f.user = f.signIn(t, "bob")
	f.setVisibility(t, access.Everyone)
	b := f.publish(t)
	f.reconcile(t)

	// The same tab watches both streams and loses packets on both.
	va := f.play(t, a, "", "tab=tab-aaaaaaaa", http.StatusCreated)
	vb := f.play(t, b, "", "tab=tab-aaaaaaaa", http.StatusCreated)
	for _, v := range []viewerSession{va, vb} {
		f.mtx.Update(v.mtxID, func(s *mtxtest.Session) { s.OutboundLossRatio = 0.3 })
	}
	tick := func() {
		f.sending(a)
		f.sending(b)
		f.refresh(t, 2*time.Second)
	}
	tick()
	tick()
	for _, st := range []Started{a, b} {
		if tg := f.target(t, st); tg.Bitrate != 0 || tg.Viewers != 0 {
			t.Fatalf("over capacity: %+v", tg)
		}
	}

	// When only one of their streams suffers, that stream is the problem.
	f.mtx.Update(vb.mtxID, func(s *mtxtest.Session) { s.OutboundLossRatio = 0 })
	for range 4 {
		tick() // its smoothed loss fades
	}
	if tg := f.target(t, a); tg.Bitrate == 0 || tg.Viewers != 1 || tg.Congested != 1 {
		t.Fatalf("stream a: %+v", tg)
	}
	if tg := f.target(t, b); tg.Bitrate != 0 {
		t.Fatalf("stream b: %+v", tg)
	}
}

func TestBitrateTargetNotForOBS(t *testing.T) {
	f := newFixture(t)
	st, err := f.gw.Start(context.Background(), StartRequest{User: f.user, Source: SourceOBS, LinkFallback: true})
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := f.gw.BitrateTarget(st.Stream.ID); ok {
		t.Fatal("OBS streams cannot be controlled")
	}
}
