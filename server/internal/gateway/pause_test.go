package gateway

import (
	"context"
	"database/sql"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/RAdu2005/cupstream/server/internal/access"
	"github.com/RAdu2005/cupstream/server/internal/auth"
	"github.com/RAdu2005/cupstream/server/internal/mediamtx/mtxtest"
	"github.com/RAdu2005/cupstream/server/internal/oauth"
	"github.com/RAdu2005/cupstream/server/internal/store"
)

type viewerSession struct {
	secret, mtxID string
}

// play opens a viewer session through the gateway; query is e.g. "paused=true".
func (f *fixture) play(t *testing.T, st Started, userID, query string, want int) viewerSession {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, "/api/streams/"+st.Stream.ID+"/whep?"+query, strings.NewReader(testOffer))
	req.SetPathValue("id", st.Stream.ID)
	req.Header.Set("Content-Type", "application/sdp")
	rec := httptest.NewRecorder()
	f.gw.Play(rec, req, userID)
	if rec.Code != want {
		t.Fatalf("play = %d %s, want %d", rec.Code, rec.Body, want)
	}
	if want != http.StatusCreated {
		return viewerSession{}
	}
	parts := strings.Split(rec.Header().Get("Location"), "/")
	secret := parts[len(parts)-1]
	for _, s := range f.mtx.Sessions("s/" + st.Stream.ID) {
		if s.Secret == secret {
			return viewerSession{secret: secret, mtxID: s.ID}
		}
	}
	t.Fatal("MediaMTX has no such session")
	return viewerSession{}
}

// control sends a viewer's pause or resume request.
func (f *fixture) control(t *testing.T, st Started, v viewerSession, action, body string) int {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, "/", strings.NewReader(body))
	req.SetPathValue("stream", st.Stream.ID)
	req.SetPathValue("secret", v.secret)
	req.SetPathValue("action", action)
	rec := httptest.NewRecorder()
	f.gw.HandleSessionControl(rec, req)
	return rec.Code
}

func (f *fixture) mtxSession(t *testing.T, id string) mtxtest.Session {
	t.Helper()
	s, ok := f.mtx.Session(id)
	if !ok {
		t.Fatalf("session %s is closed", id)
	}
	return s
}

func (f *fixture) viewers(t *testing.T, st Started) int {
	t.Helper()
	ls, _ := f.gw.Stream(st.Stream.ID)
	return ls.Viewers
}

func (f *fixture) signIn(t *testing.T, name string) store.User {
	t.Helper()
	u, _, err := auth.NewAccounts(f.db, nil).SignIn(context.Background(), oauth.Identity{Provider: "dev", Subject: name, Username: name})
	if err != nil {
		t.Fatal(err)
	}
	return u
}

func (f *fixture) room(t *testing.T) store.Room {
	t.Helper()
	room, err := f.db.GetLinkRoomByOwner(context.Background(), sql.NullString{String: f.user.ID, Valid: true})
	if err != nil {
		t.Fatal(err)
	}
	return room
}

func (f *fixture) setVisibility(t *testing.T, v access.Visibility) store.Room {
	t.Helper()
	room := f.room(t)
	if err := f.gw.rooms.SetVisibility(context.Background(), room, v); err != nil {
		t.Fatal(err)
	}
	return f.room(t)
}

func TestViewerPauseAndResume(t *testing.T) {
	f := newFixture(t)
	f.gw.DetectFeatures(context.Background())
	st := f.publish(t)
	f.reconcile(t)
	v := f.play(t, st, f.user.ID, "", http.StatusCreated)
	if n := f.viewers(t, st); n != 1 {
		t.Fatalf("viewers = %d, want 1", n)
	}

	// Audio only: video is paused, the viewer still counts.
	if c := f.control(t, st, v, "pause", `{"video":true}`); c != http.StatusNoContent {
		t.Fatalf("pause video = %d", c)
	}
	if s := f.mtxSession(t, v.mtxID); !s.VideoPaused || s.AudioPaused {
		t.Fatalf("after pausing video: %+v", s)
	}
	if n := f.viewers(t, st); n != 1 {
		t.Fatalf("viewers while listening = %d, want 1", n)
	}

	// Pausing everything: only audio is sent to MediaMTX; no longer a viewer.
	if c := f.control(t, st, v, "pause", ""); c != http.StatusNoContent {
		t.Fatalf("pause = %d", c)
	}
	calls := f.mtx.Calls()
	if last := calls[len(calls)-1]; last.Action != "pause" || last.Video || !last.Audio {
		t.Fatalf("second pause sent %+v, want audio only", last)
	}
	if n := f.viewers(t, st); n != 0 {
		t.Fatalf("viewers while paused = %d, want 0", n)
	}

	if c := f.control(t, st, v, "resume", ""); c != http.StatusNoContent {
		t.Fatalf("resume = %d", c)
	}
	if s := f.mtxSession(t, v.mtxID); s.VideoPaused || s.AudioPaused {
		t.Fatalf("after resume: %+v", s)
	}
	if n := f.viewers(t, st); n != 1 {
		t.Fatalf("viewers after resume = %d, want 1", n)
	}

	// Resuming tracks that play already sends nothing: MediaMTX would make
	// them wait for a key frame.
	before := len(f.mtx.Calls())
	if c := f.control(t, st, v, "resume", ""); c != http.StatusNoContent {
		t.Fatalf("second resume = %d", c)
	}
	if n := len(f.mtx.Calls()); n != before {
		t.Fatalf("resuming playing tracks sent %d requests", n-before)
	}

	if c := f.control(t, st, viewerSession{secret: "00000000-0000-0000-0000-000000000000"}, "pause", ""); c != http.StatusNotFound {
		t.Fatalf("unknown session = %d, want 404", c)
	}
	if c := f.control(t, st, v, "pause", `{"paused":true}`); c != http.StatusBadRequest {
		t.Fatalf("PATCH-style body = %d, want 400", c)
	}
}

func TestPrewarmedSession(t *testing.T) {
	f := newFixture(t)
	f.gw.DetectFeatures(context.Background())
	st := f.publish(t)
	f.reconcile(t)

	v := f.play(t, st, f.user.ID, "paused=true&preview=false", http.StatusCreated)
	s := f.mtxSession(t, v.mtxID)
	if !strings.Contains(s.Query, "paused=true") || !strings.Contains(s.Query, "preview=false") || !s.VideoPaused {
		t.Fatalf("MediaMTX session %+v, want it opened paused without a preview", s)
	}
	if n := f.viewers(t, st); n != 0 {
		t.Fatalf("a pre-warmed session counts as %d viewers", n)
	}
	if c := f.control(t, st, v, "resume", ""); c != http.StatusNoContent {
		t.Fatalf("resume = %d", c)
	}
	if n := f.viewers(t, st); n != 1 {
		t.Fatalf("viewers after resume = %d, want 1", n)
	}

	f.play(t, st, f.user.ID, "paused=maybe", http.StatusBadRequest)
}

func TestHeldViewer(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	f.gw.DetectFeatures(ctx)
	st := f.publish(t)
	f.reconcile(t)
	bob := f.signIn(t, "bob")
	f.setVisibility(t, access.SignedIn)
	v := f.play(t, st, bob.ID, "", http.StatusCreated)
	// Bob listens without video before losing access.
	f.control(t, st, v, "pause", `{"video":true}`)

	// The room becomes invite only: Bob is held, not disconnected.
	f.gw.Reevaluate(ctx, f.setVisibility(t, access.InviteOnly))
	if s := f.mtxSession(t, v.mtxID); !s.VideoPaused || !s.AudioPaused {
		t.Fatalf("held session %+v, want everything paused", s)
	}
	if n := f.viewers(t, st); n != 0 {
		t.Fatalf("a held viewer counts as %d viewers", n)
	}

	// Bob cannot resume himself.
	if c := f.control(t, st, v, "resume", ""); c != http.StatusForbidden {
		t.Fatalf("held viewer resume = %d, want 403", c)
	}
	if s := f.mtxSession(t, v.mtxID); !s.AudioPaused {
		t.Fatal("a held viewer resumed himself")
	}

	// Access comes back: his audio resumes, his own video pause stays.
	f.gw.Reevaluate(ctx, f.setVisibility(t, access.SignedIn))
	if s := f.mtxSession(t, v.mtxID); !s.VideoPaused || s.AudioPaused {
		t.Fatalf("released session %+v, want only video paused", s)
	}

	// Held for longer than the timeout: disconnected.
	f.gw.Reevaluate(ctx, f.setVisibility(t, access.InviteOnly))
	f.clock = f.clock.Add(holdTimeout + 1)
	f.reconcile(t)
	if _, ok := f.mtx.Session(v.mtxID); ok {
		t.Fatal("held session still open after the timeout")
	}
}

func TestWithoutPauseSessionsAreClosed(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	f.mtx.SetVersion("v1.21.2-cupstream.1")
	f.gw.DetectFeatures(ctx)
	st := f.publish(t)
	f.reconcile(t)
	bob := f.signIn(t, "bob")
	f.setVisibility(t, access.SignedIn)
	v := f.play(t, st, bob.ID, "", http.StatusCreated)

	if c := f.control(t, st, v, "pause", ""); c != http.StatusNotImplemented {
		t.Fatalf("pause = %d, want 501", c)
	}
	f.play(t, st, bob.ID, "paused=true", http.StatusNotImplemented)

	f.gw.Reevaluate(ctx, f.setVisibility(t, access.InviteOnly))
	if _, ok := f.mtx.Session(v.mtxID); ok {
		t.Fatal("without pausing, a viewer who lost access must be disconnected")
	}
}

func TestPublisherIsReplaced(t *testing.T) {
	f := newFixture(t)
	st := f.publish(t)
	f.reconcile(t)
	old := f.mtx.Sessions("s/" + st.Stream.ID)[0]

	// The publisher reconnects before MediaMTX noticed that it left.
	f.post(t, st, "Bearer "+st.Token, http.StatusCreated)
	sessions := f.mtx.Sessions("s/" + st.Stream.ID)
	if len(sessions) != 1 || sessions[0].ID == old.ID {
		t.Fatalf("sessions %+v, want only the new publisher", sessions)
	}

	// After a restart the gateway does not know the publisher yet: MediaMTX
	// refuses the new one until the old one is closed.
	f.gw = f.newGateway()
	if err := f.gw.Restore(context.Background()); err != nil {
		t.Fatal(err)
	}
	f.post(t, st, "Bearer "+st.Token, http.StatusCreated)
	if n := len(f.mtx.Sessions("s/" + st.Stream.ID)); n != 1 {
		t.Fatalf("%d publishers, want 1", n)
	}
}
