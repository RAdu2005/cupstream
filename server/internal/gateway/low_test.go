package gateway

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"

	"github.com/RAdu2005/cupstream/server/internal/access"
	"github.com/RAdu2005/cupstream/server/internal/api"
	"github.com/RAdu2005/cupstream/server/internal/mediamtx"
	"github.com/RAdu2005/cupstream/server/internal/mediamtx/mtxtest"
)

// postLow publishes the low-quality version of st through the gateway.
func (f *fixture) postLow(t *testing.T, st Started, want int) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, "/api/streams/"+st.Stream.ID+"/whip?quality=low", strings.NewReader(testOffer))
	req.SetPathValue("id", st.Stream.ID)
	req.Header.Set("Content-Type", "application/sdp")
	req.Header.Set("Authorization", "Bearer "+st.Token)
	rec := httptest.NewRecorder()
	f.gw.HandleTokenPublish(rec, req)
	if rec.Code != want {
		t.Fatalf("publish low = %d %s, want %d", rec.Code, rec.Body, want)
	}
	return rec
}

func errorCode(t *testing.T, rec *httptest.ResponseRecorder) string {
	t.Helper()
	var e api.ErrorResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &e); err != nil {
		t.Fatal(err)
	}
	return e.Code
}

// deleteSession sends DELETE to a session URL from a Location header.
func (f *fixture) deleteSession(t *testing.T, location string) {
	t.Helper()
	parts := strings.Split(strings.TrimPrefix(location, "http://example.com/api/media/"), "/")
	req := httptest.NewRequest(http.MethodDelete, "/", nil)
	req.SetPathValue("stream", parts[0])
	if len(parts) == 4 {
		req.SetPathValue("variant", parts[1])
		parts = append(parts[:1], parts[2:]...)
	}
	req.SetPathValue("kind", parts[1])
	req.SetPathValue("secret", parts[2])
	rec := httptest.NewRecorder()
	f.gw.HandleSession(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("DELETE %s = %d %s", location, rec.Code, rec.Body)
	}
}

func (f *fixture) low(t *testing.T, st Started) LowQuality {
	t.Helper()
	ls, ok := f.gw.Stream(st.Stream.ID)
	if !ok {
		t.Fatal("the stream ended")
	}
	return ls.Low
}

func newLowFixture(t *testing.T) *fixture {
	f := newFixture(t)
	f.gw.LowEnabled = true
	f.gw.DetectFeatures(context.Background())
	f.setVisibility(t, access.Everyone)
	return f
}

func TestLowQualityVersion(t *testing.T) {
	f := newLowFixture(t)
	st, err := f.gw.Start(context.Background(), StartRequest{User: f.user, Source: SourceApp, LinkFallback: true})
	if err != nil {
		t.Fatal(err)
	}

	// Not before the stream itself.
	if rec := f.postLow(t, st, http.StatusConflict); errorCode(t, rec) != CodeNoMainSession {
		t.Fatalf("low before main: %s", rec.Body)
	}
	f.post(t, st, "Bearer "+st.Token, http.StatusCreated)
	f.reconcile(t)
	if got := f.low(t, st); got != LowNone {
		t.Fatalf("without a low version: %s", got)
	}
	f.play(t, st, "", "quality=low", http.StatusConflict)

	rec := f.postLow(t, st, http.StatusCreated)
	lowURL := rec.Header().Get("Location")
	if !strings.Contains(lowURL, "/api/media/"+st.Stream.ID+"/low/whip/") {
		t.Fatalf("session URL %s", lowURL)
	}
	if got := len(f.mtx.Sessions("s/" + st.Stream.ID + "/low")); got != 1 {
		t.Fatalf("%d sessions on the low path", got)
	}
	f.reconcile(t)
	if got := f.low(t, st); got != LowSent {
		t.Fatalf("with a low version: %s", got)
	}

	// Viewers of either version count as viewers of the stream.
	f.play(t, st, "", "", http.StatusCreated)
	lv := f.play(t, st, "", "quality=low", http.StatusCreated)
	if n := f.viewers(t, st); n != 2 {
		t.Fatalf("viewers = %d, want 2", n)
	}
	// Viewers pause and resume a low session like any other.
	if c := f.control(t, st, lv, "pause", ""); c != http.StatusNoContent {
		t.Fatalf("pausing a low session = %d", c)
	}
	if n := f.viewers(t, st); n != 1 {
		t.Fatalf("viewers after pausing = %d, want 1", n)
	}

	// The main publisher reconnecting leaves the low version alone.
	f.post(t, st, "Bearer "+st.Token, http.StatusCreated)
	if got := len(f.mtx.Sessions("s/" + st.Stream.ID + "/low")); got != 2 {
		t.Fatalf("%d sessions on the low path after the main publisher reconnected, want the publisher and its viewer", got)
	}

	// The low version ending alone does not end the stream.
	f.deleteSession(t, lowURL)
	if got := f.state(t, st.Stream.ID); got != Live {
		t.Fatalf("after the low version ended: %s", got)
	}
	if got := f.low(t, st); got != LowNone {
		t.Fatalf("after the low version ended: %s", got)
	}

	// It can come back, and the stream ending ends it.
	f.postLow(t, st, http.StatusCreated)
	f.reconcile(t)
	lowPub := f.mtx.Sessions("s/" + st.Stream.ID + "/low")[0]
	f.gw.EndStream(context.Background(), st.Stream.ID, EndStopped)
	if !slices.Contains(f.mtx.Kicked(), lowPub.ID) {
		t.Fatal("the low version outlived its stream")
	}
}

func TestLowQualityOff(t *testing.T) {
	f := newLowFixture(t)
	f.gw.LowEnabled = false
	st := f.publish(t)
	if rec := f.postLow(t, st, http.StatusForbidden); errorCode(t, rec) != CodeLowQualityOff {
		t.Fatalf("low while off: %s", rec.Body)
	}

	// A low version published before the server turned them off is not offered.
	f.gw.LowEnabled = true
	f.postLow(t, st, http.StatusCreated)
	f.gw.LowEnabled = false
	f.reconcile(t)
	if got := f.low(t, st); got != LowNone {
		t.Fatalf("while off: %s", got)
	}
	f.play(t, st, "", "quality=low", http.StatusConflict)
}

func TestLowQualityBadParam(t *testing.T) {
	f := newLowFixture(t)
	st := f.publish(t)
	f.play(t, st, "", "quality=high", http.StatusBadRequest)
}

func TestLowQualityEvents(t *testing.T) {
	f := newLowFixture(t)
	st := f.publish(t)
	f.reconcile(t)
	<-f.gw.Changes()

	path := "s/" + st.Stream.ID + "/low"
	f.gw.HandleEvent(context.Background(), mediamtx.Event{Type: mediamtx.EventStreamOnline, Path: path})
	if got := f.low(t, st); got != LowSent {
		t.Fatalf("after stream.online: %s", got)
	}
	select {
	case <-f.gw.Changes():
	default:
		t.Fatal("no change signalled")
	}
	f.gw.HandleEvent(context.Background(), mediamtx.Event{Type: mediamtx.EventStreamOffline, Path: path})
	if got := f.low(t, st); got != LowNone {
		t.Fatalf("after stream.offline: %s", got)
	}
	// The stream itself is unaffected.
	if got := f.state(t, st.Stream.ID); got != Live {
		t.Fatalf("stream %s", got)
	}
}

func TestLowQualityAfterRestart(t *testing.T) {
	f := newLowFixture(t)
	st := f.publish(t)
	f.postLow(t, st, http.StatusCreated)
	f.reconcile(t)

	f.gw = f.newGateway()
	f.gw.LowEnabled = true
	if err := f.gw.Restore(context.Background()); err != nil {
		t.Fatal(err)
	}
	f.reconcile(t)
	if got := f.low(t, st); got != LowSent {
		t.Fatalf("after a restart: %s", got)
	}
	if !f.gw.hasPublisher(st.Stream.ID, VariantLow) || !f.gw.hasPublisher(st.Stream.ID, VariantMain) {
		t.Fatal("publishers not adopted")
	}
	if k := f.mtx.Kicked(); len(k) != 0 {
		t.Fatalf("kicked %v", k)
	}
}

// bitrates admits everyone and records the bitrates it was asked about.
type bitrates []int64

func (b *bitrates) Admit(bps int64) bool {
	*b = append(*b, bps)
	return true
}

func TestLowQualityAdmission(t *testing.T) {
	f := newLowFixture(t)
	adm := &bitrates{}
	f.gw.Admission = adm
	st := f.publish(t)
	f.postLow(t, st, http.StatusCreated)
	f.reconcile(t)

	// Not measured yet: the low version counts with the default.
	v := f.play(t, st, "", "quality=low&paused=true", http.StatusCreated)
	f.control(t, st, v, "resume", "")
	// Measured: with what the server receives of it.
	for _, s := range f.mtx.Sessions("s/" + st.Stream.ID + "/low") {
		if s.State == "publish" {
			f.mtx.Update(s.ID, func(s *mtxtest.Session) { s.InboundBytes += 250_000 })
		}
	}
	f.refresh(t, 0)
	f.refresh(t, 1e9)
	for _, s := range f.mtx.Sessions("s/" + st.Stream.ID + "/low") {
		if s.State == "publish" {
			f.mtx.Update(s.ID, func(s *mtxtest.Session) { s.InboundBytes += 250_000 })
		}
	}
	f.refresh(t, 1e9)
	f.play(t, st, "", "quality=low", http.StatusCreated)
	if want := (bitrates{LowDefaultBitrate, 2_000_000}); !slices.Equal(*adm, want) {
		t.Fatalf("admitted at %v, want %v", *adm, want)
	}
	if ls, _ := f.gw.Stream(st.Stream.ID); ls.Stats.Bitrate != 0 {
		t.Fatalf("the low version counted as the stream's own bitrate: %d", ls.Stats.Bitrate)
	}
}
