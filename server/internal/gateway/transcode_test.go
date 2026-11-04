package gateway

import (
	"context"
	"net/http"
	"slices"
	"strings"
	"testing"
	"time"
)

// newTranscodeFixture runs a fake MediaMTX whose transcode paths start
// FFmpeg when read, with room for max transcodes.
func newTranscodeFixture(t *testing.T, max int) *fixture {
	f := newLowFixture(t)
	f.mtx.OnDemand = func(path string) bool { return strings.HasSuffix(path, "/transcode") }
	f.gw.SetMaxTranscodes(max)
	return f
}

func TestTranscodesOff(t *testing.T) {
	f := newTranscodeFixture(t, 0)
	st := f.publish(t)
	f.reconcile(t)
	if got := f.low(t, st); got != LowNone {
		t.Fatalf("without transcodes: %s", got)
	}
	f.play(t, st, "", "quality=low", http.StatusConflict)
}

func TestTranscodeCap(t *testing.T) {
	f := newTranscodeFixture(t, 1)
	ss := f.costreams(t, 2)
	a, b := ss[0], ss[1]
	if f.low(t, a) != LowTranscoded || f.low(t, b) != LowTranscoded {
		t.Fatalf("before any transcode: %s, %s", f.low(t, a), f.low(t, b))
	}

	// A viewer starts a's transcode, which takes the only place.
	v := f.play(t, a, "", "quality=low", http.StatusCreated)
	if !strings.Contains(v.location, "/api/media/"+a.Stream.ID+"/transcode/whep/") {
		t.Fatalf("session URL %s", v.location)
	}
	if f.low(t, b) != LowNone {
		t.Fatalf("b with the cap reached: %s", f.low(t, b))
	}
	f.play(t, b, "", "quality=low", http.StatusConflict)
	// a's transcode serves more viewers.
	f.play(t, a, "", "quality=low", http.StatusCreated)
	if u := f.gw.Transcodes(); u.Running != 1 || u.Max != 1 {
		t.Fatalf("usage %+v", u)
	}
	// They count as viewers of the stream, like any other.
	if n := f.viewers(t, a); n != 2 {
		t.Fatalf("viewers = %d", n)
	}

	// A sender's own low-quality version needs no transcode.
	f.postLow(t, b, http.StatusCreated)
	f.reconcile(t)
	if f.low(t, b) != LowSent {
		t.Fatalf("b with a sent low version: %s", f.low(t, b))
	}

	// Once a's viewers left and MediaMTX stopped FFmpeg, the place is free.
	for _, s := range f.mtx.Sessions("s/" + a.Stream.ID + "/transcode") {
		f.gw.kickSession(context.Background(), s.ID)
	}
	f.mtx.StopOnDemand("s/" + a.Stream.ID + "/transcode")
	f.reconcile(t)
	if u := f.gw.Transcodes(); u.Running != 1 {
		t.Fatalf("within the reservation: %+v", u)
	}
	f.clock = f.clock.Add(transcodeReserve)
	f.reconcile(t)
	if u := f.gw.Transcodes(); u.Running != 0 {
		t.Fatalf("after FFmpeg stopped: %+v", u)
	}
}

func TestTranscodeOnlyWhileLive(t *testing.T) {
	f := newTranscodeFixture(t, 4)
	st, err := f.gw.Start(context.Background(), StartRequest{User: f.user, Source: SourceOBS, LinkFallback: true})
	if err != nil {
		t.Fatal(err)
	}
	if got := f.low(t, st); got != LowNone {
		t.Fatalf("starting: %s", got)
	}
}

func TestLoweringTheCapKeepsTranscodes(t *testing.T) {
	f := newTranscodeFixture(t, 2)
	ss := f.costreams(t, 2)
	a, b := ss[0], ss[1]
	f.play(t, a, "", "quality=low", http.StatusCreated)
	f.play(t, b, "", "quality=low", http.StatusCreated)
	f.reconcile(t)

	f.gw.SetMaxTranscodes(1)
	if f.low(t, a) != LowTranscoded || f.low(t, b) != LowTranscoded {
		t.Fatal("running transcodes were withdrawn")
	}
	f.play(t, a, "", "quality=low", http.StatusCreated)
	if k := f.mtx.Kicked(); len(k) != 0 {
		t.Fatalf("kicked %v", k)
	}
	f.gw.SetMaxTranscodes(0)
	if f.low(t, a) != LowNone {
		t.Fatal("transcodes offered while off")
	}
}

func TestIdleTranscodeViewersAreClosed(t *testing.T) {
	f := newTranscodeFixture(t, 1)
	st := f.publish(t)
	f.reconcile(t)
	watching := f.play(t, st, "", "quality=low", http.StatusCreated)
	paused := f.play(t, st, "", "quality=low", http.StatusCreated)
	if c := f.control(t, st, paused, "pause", ""); c != http.StatusNoContent {
		t.Fatalf("pause = %d", c)
	}
	f.reconcile(t)
	f.clock = f.clock.Add(transcodeIdleTimeout - time.Second)
	f.reconcile(t)
	if len(f.mtx.Kicked()) != 0 {
		t.Fatal("closed too early")
	}
	f.clock = f.clock.Add(2 * time.Second)
	f.reconcile(t)
	if k := f.mtx.Kicked(); !slices.Equal(k, []string{paused.mtxID}) {
		t.Fatalf("kicked %v, want the paused viewer %s (not %s)", k, paused.mtxID, watching.mtxID)
	}
}
