package auth

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/RAdu2005/cupstream/server/internal/oauth"
	"github.com/RAdu2005/cupstream/server/internal/store"
)

func TestSessionLifecycle(t *testing.T) {
	ctx := context.Background()
	db, err := store.Open(ctx, ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	user, _, err := NewAccounts(db, nil).SignIn(ctx, oauth.Identity{Provider: ProviderDev, Subject: "alice", Username: "alice", DisplayName: "Alice"})
	if err != nil {
		t.Fatal(err)
	}

	now := time.Unix(1_800_000_000, 0)
	s := NewSessions(db, true)
	s.now = func() time.Time { return now }

	rec := httptest.NewRecorder()
	if err := s.Start(ctx, rec, user.ID); err != nil {
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

func TestListAndRevoke(t *testing.T) {
	ctx := context.Background()
	db, err := store.Open(ctx, ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	acc := NewAccounts(db, nil)
	alice, _, _ := acc.SignIn(ctx, oauth.Identity{Provider: ProviderDev, Subject: "alice", Username: "alice"})
	bob, _, _ := acc.SignIn(ctx, oauth.Identity{Provider: ProviderDev, Subject: "bob", Username: "bob"})

	s := NewSessions(db, false)
	for range 2 {
		if err := s.Start(ctx, httptest.NewRecorder(), alice.ID); err != nil {
			t.Fatal(err)
		}
	}
	list, err := s.List(ctx, alice.ID)
	if err != nil || len(list) != 2 {
		t.Fatalf("List = %d sessions, %v", len(list), err)
	}
	if ok, _ := s.Revoke(ctx, bob.ID, list[0].ID); ok {
		t.Fatal("bob revoked alice's session")
	}
	if ok, _ := s.Revoke(ctx, alice.ID, list[0].ID); !ok {
		t.Fatal("alice could not revoke her session")
	}
	if list, _ := s.List(ctx, alice.ID); len(list) != 1 {
		t.Fatalf("%d sessions left, want 1", len(list))
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

func TestAppSession(t *testing.T) {
	ctx := context.Background()
	db, err := store.Open(ctx, ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	user, _, err := NewAccounts(db, nil).SignIn(ctx, oauth.Identity{Provider: ProviderDev, Subject: "alice", Username: "alice", DisplayName: "Alice"})
	if err != nil {
		t.Fatal(err)
	}
	s := NewSessions(db, true)
	token, err := s.StartApp(ctx, user.ID, "Alice's PC")
	if err != nil {
		t.Fatal(err)
	}
	rec := httptest.NewRecorder()
	if err := s.Start(ctx, rec, user.ID); err != nil {
		t.Fatal(err)
	}
	webToken := rec.Result().Cookies()[0].Value

	whoami := func(path string, edit func(*http.Request)) (Current, bool) {
		var got Current
		var ok bool
		req := httptest.NewRequest("GET", path, nil)
		edit(req)
		s.Middleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			got, ok = FromContext(r.Context())
		})).ServeHTTP(httptest.NewRecorder(), req)
		return got, ok
	}
	withBearer := func(tok string) func(*http.Request) {
		return func(r *http.Request) { r.Header.Set("Authorization", "Bearer "+tok) }
	}
	withCookie := func(tok string) func(*http.Request) {
		return func(r *http.Request) { r.AddCookie(&http.Cookie{Name: "__Host-cupstream_session", Value: tok}) }
	}

	if cur, ok := whoami("/api/me", withBearer(token)); !ok || cur.User.ID != user.ID || cur.Kind != KindApp {
		t.Fatalf("app token not resolved: %+v %v", cur, ok)
	}
	// Each kind of token only works the way it is sent, and Bearer tokens
	// only on the API (elsewhere they are OBS stream keys).
	for name, edit := range map[string]func(*http.Request){
		"app token as cookie":   withCookie(token),
		"web token as bearer":   withBearer(webToken),
		"unknown bearer":        withBearer("nope"),
		"bearer scheme missing": func(r *http.Request) { r.Header.Set("Authorization", token) },
	} {
		if _, ok := whoami("/api/me", edit); ok {
			t.Errorf("%s: accepted", name)
		}
	}
	if _, ok := whoami("/whip", withBearer(token)); ok {
		t.Error("app token accepted outside /api/")
	}

	list, err := s.List(ctx, user.ID)
	if err != nil || len(list) != 2 {
		t.Fatalf("sessions = %v, %v", list, err)
	}
	for _, se := range list {
		if se.Kind == string(KindApp) && se.DeviceName != "Alice's PC" {
			t.Fatalf("device name = %q", se.DeviceName)
		}
	}

	// Signing out with the token ends only the app session.
	req := httptest.NewRequest("POST", "/api/auth/logout", nil)
	withBearer(token)(req)
	if err := s.End(ctx, httptest.NewRecorder(), req); err != nil {
		t.Fatal(err)
	}
	if _, ok := whoami("/api/me", withBearer(token)); ok {
		t.Fatal("app session still valid after sign-out")
	}
	if _, ok := whoami("/api/me", withCookie(webToken)); !ok {
		t.Fatal("web session ended too")
	}
}
