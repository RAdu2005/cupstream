package gateway

import (
	"context"
	"net/http"
	"testing"
)

// fullAt admits viewers while it has room.
type fullAt struct{ room int }

func (a *fullAt) Admit(int64) bool {
	if a.room <= 0 {
		return false
	}
	a.room--
	return true
}

func TestAdmission(t *testing.T) {
	f := newFixture(t)
	f.gw.DetectFeatures(context.Background())
	adm := &fullAt{room: 1}
	f.gw.Admission = adm
	st := f.publish(t)
	f.reconcile(t)

	f.play(t, st, f.user.ID, "", http.StatusCreated)
	// Pre-warming costs nothing and is always allowed...
	v := f.play(t, st, f.user.ID, "paused=true&preview=false", http.StatusCreated)
	// ...but a viewer who would receive media is refused.
	f.play(t, st, f.user.ID, "", http.StatusServiceUnavailable)
	if c := f.control(t, st, v, "resume", ""); c != http.StatusServiceUnavailable {
		t.Fatalf("resuming at the limit = %d, want 503", c)
	}
	// Audio alone is not checked.
	if c := f.control(t, st, v, "resume", `{"audio":true}`); c != http.StatusNoContent {
		t.Fatalf("resuming audio = %d", c)
	}
	if s := f.mtxSession(t, v.mtxID); !s.VideoPaused || s.AudioPaused {
		t.Fatalf("after resuming audio: %+v", s)
	}

	adm.room = 1
	if c := f.control(t, st, v, "resume", ""); c != http.StatusNoContent {
		t.Fatalf("resume with room = %d", c)
	}
	// Resuming what plays already does not count again.
	if c := f.control(t, st, v, "resume", ""); c != http.StatusNoContent || adm.room != 0 {
		t.Fatalf("second resume = %d, room %d", c, adm.room)
	}
}

// A viewer refused for watching too many streams reserves no bandwidth.
func TestAdmissionAfterOtherChecks(t *testing.T) {
	f := newFixture(t)
	f.gw.DetectFeatures(context.Background())
	adm := &fullAt{room: 100}
	f.gw.Admission = adm
	st := f.publish(t)
	f.reconcile(t)
	for range MaxWatching {
		f.play(t, st, f.user.ID, "", http.StatusCreated)
	}
	f.play(t, st, f.user.ID, "", http.StatusConflict)
	v := f.play(t, st, f.user.ID, "paused=true&preview=false", http.StatusCreated)
	if c := f.control(t, st, v, "resume", ""); c != http.StatusConflict {
		t.Fatalf("resume over the stream limit = %d, want 409", c)
	}
	if adm.room != 100-MaxWatching {
		t.Fatalf("%d admissions counted, want %d", 100-adm.room, MaxWatching)
	}
}
