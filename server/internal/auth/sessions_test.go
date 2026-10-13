package auth

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/RAdu2005/cupstream/server/internal/store"
)

func TestSessionLifecycle(t *testing.T) {
	ctx := context.Background()
	db, err := store.Open(ctx, ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	user, err := SignIn(ctx, db, Identity{Provider: ProviderDev, Subject: "alice", Username: "alice", Handle: "alice", DisplayName: "Alice"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := SignIn(ctx, db, Identity{Provider: "google", Subject: "1", Handle: "ALICE", DisplayName: "Other"}); err != ErrHandleTaken {
		t.Fatalf("duplicate handle: got %v, want ErrHandleTaken", err)
	}

	now := time.Unix(1_800_000_000, 0)
	s := NewSessions(db, true)
	s.now = func() time.Time { return now }

	rec := httptest.NewRecorder()
	if err := s.Start(ctx, rec, user.ID, KindWeb, ""); err != nil {
		t.Fatal(err)
	}
	cookie := rec.Result().Cookies()[0]
	if !cookie.HttpOnly || !cookie.Secure || cookie.SameSite != http.SameSiteLaxMode {
		t.Fatalf("cookie attributes: %+v", cookie)
	}

	whoami := func() (Current, bool) {
		var got Current
		var ok bool
		req := httptest.NewRequest("GET", "/", nil)
		req.AddCookie(cookie)
		s.Middleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			got, ok = FromContext(r.Context())
		})).ServeHTTP(httptest.NewRecorder(), req)
		return got, ok
	}

	if cur, ok := whoami(); !ok || cur.User.ID != user.ID {
		t.Fatalf("session not resolved: %+v %v", cur, ok)
	}

	// Activity slides the expiry forward.
	now = now.Add(20 * 24 * time.Hour)
	if _, ok := whoami(); !ok {
		t.Fatal("session expired too early")
	}
	now = now.Add(29 * 24 * time.Hour)
	if _, ok := whoami(); !ok {
		t.Fatal("sliding expiry did not extend the session")
	}

	// Without activity it expires and is cleaned up.
	now = now.Add(31 * 24 * time.Hour)
	if _, ok := whoami(); ok {
		t.Fatal("expired session still valid")
	}
	if n, err := db.DeleteExpiredSessions(ctx, now.Unix()); err != nil || n != 1 {
		t.Fatalf("cleanup deleted %d sessions, err %v", n, err)
	}
}

func TestNormalizeHandle(t *testing.T) {
	for in, want := range map[string]string{" Alice ": "alice", "bob_2": "bob_2"} {
		if got, err := NormalizeHandle(in); err != nil || got != want {
			t.Errorf("NormalizeHandle(%q) = %q, %v", in, got, err)
		}
	}
	for _, in := range []string{"a", "has space", "ünïcode", "dash-ed", "waytoolong_waytoolong_waytoolong_x"} {
		if _, err := NormalizeHandle(in); err == nil {
			t.Errorf("NormalizeHandle(%q) should fail", in)
		}
	}
}
