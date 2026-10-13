package httpapi

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/RAdu2005/cupstream/server/internal/api"
	"github.com/RAdu2005/cupstream/server/internal/auth"
	"github.com/RAdu2005/cupstream/server/internal/config"
	"github.com/RAdu2005/cupstream/server/internal/gateway"
	"github.com/RAdu2005/cupstream/server/internal/mediamtx"
	"github.com/RAdu2005/cupstream/server/internal/mediamtx/mtxtest"
	"github.com/RAdu2005/cupstream/server/internal/oauth"
	"github.com/RAdu2005/cupstream/server/internal/rooms"
	"github.com/RAdu2005/cupstream/server/internal/seal"
	"github.com/RAdu2005/cupstream/server/internal/store"
)

type testEnv struct {
	srv     *httptest.Server
	db      *store.DB
	mtx     *mtxtest.Fake
	gw      *gateway.Gateway
	google  *mockGoogle
	discord *mockDiscord
}

type envOptions struct {
	devLogin     bool
	mediamtxDown bool
	oauth        bool
}

func newTestEnv(t *testing.T, o envOptions) *testEnv {
	t.Helper()
	ctx := context.Background()
	db, err := store.Open(ctx, ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })

	e := &testEnv{db: db, mtx: mtxtest.New(t)}
	mtxURL := e.mtx.URL
	if o.mediamtxDown {
		mtxURL, _ = url.Parse("http://127.0.0.1:1")
	}
	sealer, _ := seal.New(bytes.Repeat([]byte{1}, 32))
	media := mediamtx.New(mtxURL, mtxURL)
	rs := rooms.New(db)

	// The server's own URL is only known once it starts; handlers read it
	// through cfg at request time.
	cfg := &config.Config{PublicURL: &url.URL{Scheme: "http", Host: "localhost"}, DevLogin: o.devLogin}
	deps := Deps{
		Config: cfg, DB: db, Sealer: sealer, Sessions: auth.NewSessions(db, false),
		Rooms: rs, MediaMTX: media, Providers: map[string]oauth.Provider{},
		Web: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { io.WriteString(w, "website") }),
	}
	if o.oauth {
		e.google = newMockGoogle(t)
		e.discord = newMockDiscord(t)
		deps.Providers[oauth.Google] = oauth.NewGoogle(e.google.URL, "google-client", "secret", "http://localhost/api/auth/google/callback")
		dp := oauth.NewDiscord(e.discord.URL, e.discord.URL, "discord-client", "secret", "http://localhost/api/auth/discord/callback")
		deps.Providers[oauth.Discord] = dp
		deps.Discord = auth.NewDiscordSync(db, sealer, dp)
	}
	deps.Accounts = auth.NewAccounts(db, deps.Discord)
	e.gw = gateway.New(db, media, rs, cfg.PublicURL)
	deps.Gateway = e.gw

	e.srv = httptest.NewServer(New(deps).Handler())
	t.Cleanup(e.srv.Close)
	cfg.PublicURL, _ = url.Parse(e.srv.URL)
	return e
}

// browser is one user agent with its own cookies.
type browser struct {
	t   *testing.T
	e   *testEnv
	hc  *http.Client
	jar *cookiejar.Jar
}

func (e *testEnv) browser(t *testing.T) *browser {
	jar, _ := cookiejar.New(nil)
	return &browser{t: t, e: e, jar: jar, hc: &http.Client{
		Jar:           jar,
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
	}}
}

type response struct {
	status int
	body   string
	header http.Header
}

func (b *browser) do(method, path, body string, header ...string) response {
	b.t.Helper()
	target := path
	if strings.HasPrefix(path, "/") {
		target = b.e.srv.URL + path
	}
	req, _ := http.NewRequest(method, target, strings.NewReader(body))
	if body != "" && !strings.HasPrefix(body, "v=0") {
		req.Header.Set("Content-Type", "application/json")
	}
	for i := 0; i+1 < len(header); i += 2 {
		if header[i] == "Host" {
			req.Host = header[i+1]
		} else {
			req.Header.Set(header[i], header[i+1])
		}
	}
	resp, err := b.hc.Do(req)
	if err != nil {
		b.t.Fatal(err)
	}
	defer resp.Body.Close()
	out, _ := io.ReadAll(resp.Body)
	return response{resp.StatusCode, string(out), resp.Header}
}

func (b *browser) json(method, path string, in, out any, want int) {
	b.t.Helper()
	body := ""
	if in != nil {
		raw, _ := json.Marshal(in)
		body = string(raw)
	}
	r := b.do(method, path, body)
	if r.status != want {
		b.t.Fatalf("%s %s = %d %s, want %d", method, path, r.status, r.body, want)
	}
	if out != nil {
		if err := json.Unmarshal([]byte(r.body), out); err != nil {
			b.t.Fatalf("%s %s: decode %q: %v", method, path, r.body, err)
		}
	}
}

func (b *browser) devLogin(handle string) api.User {
	b.t.Helper()
	var me api.Me
	b.json("POST", "/api/auth/dev/login", api.DevLoginRequest{Handle: handle}, &me, http.StatusOK)
	return me.User
}

func decode[T any](t *testing.T, s string) T {
	t.Helper()
	var v T
	if err := json.Unmarshal([]byte(s), &v); err != nil {
		t.Fatalf("decode %q: %v", s, err)
	}
	return v
}

func TestHealth(t *testing.T) {
	b := newTestEnv(t, envOptions{}).browser(t)
	r := b.do("GET", "/healthz", "")
	h := decode[api.Health](t, r.body)
	if r.status != 200 || h.Status != "ok" || !h.Database || h.MediaMTX.Version != "v1.21.2-cupstream.1" {
		t.Fatalf("got %d %+v", r.status, h)
	}

	b = newTestEnv(t, envOptions{mediamtxDown: true}).browser(t)
	r = b.do("GET", "/healthz", "")
	h = decode[api.Health](t, r.body)
	if r.status != 200 || h.Status != "degraded" || h.MediaMTX.Reachable {
		t.Fatalf("got %d %+v", r.status, h)
	}
}

func TestDevLoginFlow(t *testing.T) {
	b := newTestEnv(t, envOptions{devLogin: true}).browser(t)

	var providers api.AuthProviders
	b.json("GET", "/api/auth/providers", nil, &providers, 200)
	if len(providers.Providers) != 1 || providers.Providers[0] != "dev" {
		t.Fatalf("providers = %v", providers.Providers)
	}
	b.json("GET", "/api/me", nil, nil, http.StatusUnauthorized)

	first := b.devLogin("Alice")
	if first.Handle != "alice" {
		t.Fatalf("handle = %q", first.Handle)
	}
	var me api.Me
	b.json("GET", "/api/me", nil, &me, 200)
	if me.User.ID != first.ID {
		t.Fatal("/api/me returned another user")
	}
	if again := b.devLogin("alice"); again.ID != first.ID {
		t.Fatal("second login created a new user")
	}
	b.json("POST", "/api/auth/logout", nil, nil, http.StatusNoContent)
	b.json("GET", "/api/me", nil, nil, http.StatusUnauthorized)
}

func TestDevLoginRefusals(t *testing.T) {
	b := newTestEnv(t, envOptions{devLogin: true}).browser(t)
	for name, tc := range map[string]struct {
		body   string
		header []string
		want   int
	}{
		"invalid handle": {`{"handle":"a b"}`, nil, http.StatusBadRequest},
		"unknown field":  {`{"handle":"bob","admin":true}`, nil, http.StatusBadRequest},
		"public host":    {`{"handle":"bob"}`, []string{"Host", "cup.example.com"}, http.StatusForbidden},
		"cross-site":     {`{"handle":"bob"}`, []string{"Sec-Fetch-Site", "cross-site", "Origin", "https://evil.example"}, http.StatusForbidden},
		"foreign origin": {`{"handle":"bob"}`, []string{"Origin", "https://evil.example"}, http.StatusForbidden},
	} {
		t.Run(name, func(t *testing.T) {
			if r := b.do("POST", "/api/auth/dev/login", tc.body, tc.header...); r.status != tc.want {
				t.Fatalf("got %d %s, want %d", r.status, r.body, tc.want)
			}
		})
	}
}

func TestDevLoginDisabled(t *testing.T) {
	b := newTestEnv(t, envOptions{}).browser(t)
	if r := b.do("POST", "/api/auth/dev/login", `{"handle":"bob"}`); r.status != http.StatusNotFound {
		t.Fatalf("got %d", r.status)
	}
	var providers api.AuthProviders
	b.json("GET", "/api/auth/providers", nil, &providers, 200)
	if len(providers.Providers) != 0 {
		t.Fatalf("providers = %v", providers.Providers)
	}
}

func TestRouting(t *testing.T) {
	b := newTestEnv(t, envOptions{}).browser(t)
	if r := b.do("GET", "/api/nope", ""); r.status != 404 || !strings.Contains(r.body, `"error"`) {
		t.Fatalf("unknown API path = %d %s", r.status, r.body)
	}
	if r := b.do("GET", "/rooms/x", ""); r.status != 200 || r.body != "website" {
		t.Fatalf("website path = %d %s", r.status, r.body)
	}
}

func TestProfileAndSettings(t *testing.T) {
	e := newTestEnv(t, envOptions{devLogin: true})
	a, b := e.browser(t), e.browser(t)
	a.devLogin("alice")
	b.devLogin("bob")

	var me api.Me
	a.json("PATCH", "/api/account", api.UpdateProfileRequest{Handle: "Alice_2", DisplayName: "Alice"}, &me, 200)
	if me.User.Handle != "alice_2" {
		t.Fatalf("handle = %q", me.User.Handle)
	}
	a.json("PATCH", "/api/account", api.UpdateProfileRequest{Handle: "bob", DisplayName: "Alice"}, nil, http.StatusConflict)

	a.json("PATCH", "/api/account/settings", api.Settings{StartMode: "link", ObsFallback: "refuse"}, nil, 200)
	a.json("PATCH", "/api/account/settings", api.Settings{StartMode: "elsewhere", ObsFallback: "link"}, nil, http.StatusBadRequest)
	var acc api.Account
	a.json("GET", "/api/account", nil, &acc, 200)
	if acc.Settings.StartMode != "link" || acc.Settings.ObsFallback != "refuse" || acc.RoomSlug == "" || len(acc.Identities) != 1 {
		t.Fatalf("account = %+v", acc)
	}

	// Sessions: a second browser for alice, revoked from the first.
	a2 := e.browser(t)
	a2.devLogin("alice") // the dev identity keeps its subject after the handle change
	var sessions []api.Session
	a.json("GET", "/api/account/sessions", nil, &sessions, 200)
	if len(sessions) != 2 {
		t.Fatalf("%d sessions", len(sessions))
	}
	for _, s := range sessions {
		if !s.Current {
			a.json("DELETE", "/api/account/sessions/"+s.ID, nil, nil, http.StatusNoContent)
		}
	}
	a2.json("GET", "/api/me", nil, nil, http.StatusUnauthorized)
	a.json("GET", "/api/me", nil, nil, 200)
	b.json("DELETE", "/api/account/sessions/"+sessions[0].ID, nil, nil, http.StatusNotFound)

	// Unlinking the only sign-in method is refused.
	a.json("DELETE", "/api/account/identities/dev", nil, nil, http.StatusConflict)
}
