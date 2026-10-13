package httpapi

import (
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
	"github.com/RAdu2005/cupstream/server/internal/mediamtx"
	"github.com/RAdu2005/cupstream/server/internal/store"
)

type testEnv struct {
	srv    *httptest.Server
	client *http.Client
}

func newTestEnv(t *testing.T, devLogin bool, mediamtxUp bool) *testEnv {
	t.Helper()
	ctx := context.Background()

	db, err := store.Open(ctx, ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })

	mtx := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !mediamtxUp || r.URL.Path != "/v3/info" {
			http.Error(w, "down", http.StatusServiceUnavailable)
			return
		}
		io.WriteString(w, `{"version":"v1.21.2-cupstream.1","started":"2026-10-13T10:00:00Z"}`)
	}))
	t.Cleanup(mtx.Close)
	mtxURL, _ := url.Parse(mtx.URL)

	cfg := &config.Config{PublicURL: &url.URL{Scheme: "http", Host: "localhost"}, DevLogin: devLogin, MediaMTXAPI: mtxURL}
	web := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { io.WriteString(w, "website") })
	h := New(cfg, db, auth.NewSessions(db, false), mediamtx.New(mtxURL), web).Handler()

	srv := httptest.NewServer(h)
	t.Cleanup(srv.Close)
	jar, _ := cookiejar.New(nil)
	return &testEnv{srv: srv, client: &http.Client{Jar: jar}}
}

func (e *testEnv) do(t *testing.T, method, path, body string, header map[string]string) (int, string) {
	t.Helper()
	req, _ := http.NewRequest(method, e.srv.URL+path, strings.NewReader(body))
	for k, v := range header {
		req.Header.Set(k, v)
	}
	if k := header["Host"]; k != "" {
		req.Host = k
	}
	resp, err := e.client.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, string(b)
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
	status, body := newTestEnv(t, false, true).do(t, "GET", "/healthz", "", nil)
	h := decode[api.Health](t, body)
	if status != 200 || h.Status != "ok" || !h.Database || h.MediaMTX.Version != "v1.21.2-cupstream.1" {
		t.Fatalf("got %d %+v", status, h)
	}

	status, body = newTestEnv(t, false, false).do(t, "GET", "/healthz", "", nil)
	h = decode[api.Health](t, body)
	if status != 200 || h.Status != "degraded" || h.MediaMTX.Reachable {
		t.Fatalf("got %d %+v", status, h)
	}
}

func TestDevLoginFlow(t *testing.T) {
	e := newTestEnv(t, true, true)

	_, body := e.do(t, "GET", "/api/auth/providers", "", nil)
	if p := decode[api.AuthProviders](t, body); len(p.Providers) != 1 || p.Providers[0] != "dev" {
		t.Fatalf("providers = %v", p.Providers)
	}

	if status, _ := e.do(t, "GET", "/api/me", "", nil); status != http.StatusUnauthorized {
		t.Fatalf("anonymous /api/me = %d", status)
	}

	status, body := e.do(t, "POST", "/api/auth/dev/login", `{"handle":"Alice"}`, nil)
	if status != 200 {
		t.Fatalf("login = %d %s", status, body)
	}
	first := decode[api.Me](t, body).User
	if first.Handle != "alice" {
		t.Fatalf("handle = %q", first.Handle)
	}

	status, body = e.do(t, "GET", "/api/me", "", nil)
	if status != 200 || decode[api.Me](t, body).User.ID != first.ID {
		t.Fatalf("/api/me = %d %s", status, body)
	}

	// Signing in again returns the same user.
	_, body = e.do(t, "POST", "/api/auth/dev/login", `{"handle":"alice"}`, nil)
	if decode[api.Me](t, body).User.ID != first.ID {
		t.Fatal("second login created a new user")
	}

	if status, _ := e.do(t, "POST", "/api/auth/logout", "", nil); status != http.StatusNoContent {
		t.Fatalf("logout = %d", status)
	}
	if status, _ := e.do(t, "GET", "/api/me", "", nil); status != http.StatusUnauthorized {
		t.Fatalf("/api/me after logout = %d", status)
	}
}

func TestDevLoginRefusals(t *testing.T) {
	e := newTestEnv(t, true, true)
	for name, tc := range map[string]struct {
		body   string
		header map[string]string
		want   int
	}{
		"invalid handle": {`{"handle":"a b"}`, nil, http.StatusBadRequest},
		"unknown field":  {`{"handle":"bob","admin":true}`, nil, http.StatusBadRequest},
		"public host":    {`{"handle":"bob"}`, map[string]string{"Host": "cup.example.com"}, http.StatusForbidden},
		"cross-site":     {`{"handle":"bob"}`, map[string]string{"Sec-Fetch-Site": "cross-site", "Origin": "https://evil.example"}, http.StatusForbidden},
		"foreign origin": {`{"handle":"bob"}`, map[string]string{"Origin": "https://evil.example"}, http.StatusForbidden},
	} {
		t.Run(name, func(t *testing.T) {
			if status, body := e.do(t, "POST", "/api/auth/dev/login", tc.body, tc.header); status != tc.want {
				t.Fatalf("got %d %s, want %d", status, body, tc.want)
			}
		})
	}
}

func TestDevLoginDisabled(t *testing.T) {
	e := newTestEnv(t, false, true)
	if status, _ := e.do(t, "POST", "/api/auth/dev/login", `{"handle":"bob"}`, nil); status != http.StatusNotFound {
		t.Fatalf("got %d", status)
	}
	_, body := e.do(t, "GET", "/api/auth/providers", "", nil)
	if p := decode[api.AuthProviders](t, body); len(p.Providers) != 0 {
		t.Fatalf("providers = %v", p.Providers)
	}
}

func TestRouting(t *testing.T) {
	e := newTestEnv(t, false, true)
	if status, body := e.do(t, "GET", "/api/nope", "", nil); status != 404 || !strings.Contains(body, `"error"`) {
		t.Fatalf("unknown API path = %d %s", status, body)
	}
	if status, body := e.do(t, "GET", "/rooms/x", "", nil); status != 200 || body != "website" {
		t.Fatalf("website path = %d %s", status, body)
	}
}
