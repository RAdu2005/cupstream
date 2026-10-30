package httpapi

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestRateLimits(t *testing.T) {
	now := time.Unix(1000, 0)
	h := rateLimits(true, func() time.Time { return now }, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	do := func(method, path, remote string) *httptest.ResponseRecorder {
		r := httptest.NewRequest(method, path, nil)
		r.RemoteAddr = remote
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		return w
	}
	// allowed sends n requests and reports how many got through.
	allowed := func(n int, method, path, remote string) int {
		ok := 0
		for range n {
			if do(method, path, remote).Code == http.StatusOK {
				ok++
			}
		}
		return ok
	}

	// OAuth start and callback share one limit per address.
	if n := allowed(signInBurst/2, "GET", "/api/auth/google/start", "203.0.113.1:1"); n != signInBurst/2 {
		t.Fatalf("%d sign-in starts allowed", n)
	}
	if n := allowed(signInBurst, "GET", "/api/auth/discord/callback?code=x", "203.0.113.1:2"); n != signInBurst-signInBurst/2 {
		t.Fatalf("%d callbacks allowed after the starts, want %d", n, signInBurst-signInBurst/2)
	}
	w := do("POST", "/api/auth/dev/login", "203.0.113.1:3")
	if w.Code != http.StatusTooManyRequests || w.Header().Get("Retry-After") != "3" {
		t.Fatalf("dev login over the limit: %d, Retry-After %q", w.Code, w.Header().Get("Retry-After"))
	}
	if body := w.Body.String(); body == "" || decode[struct{ Code string }](t, body).Code != "rate_limited" {
		t.Fatalf("body %q", body)
	}
	if do("GET", "/api/auth/google/start", "203.0.113.2:1").Code != http.StatusOK {
		t.Fatal("another address is limited too")
	}
	// The limit applies to whole IPv6 /64 networks.
	allowed(signInBurst, "GET", "/api/auth/google/start", "[2001:db8:0:1::1]:1")
	if do("GET", "/api/auth/google/start", "[2001:db8:0:1:ffff::2]:1").Code != http.StatusTooManyRequests {
		t.Fatal("another address in the same /64 is not limited")
	}

	// Going live: the API, OBS and token WHIP share one limit.
	allowed(publishBurst-2, "POST", "/whip", "203.0.113.3:1")
	allowed(1, "POST", "/api/streams/abcdefghijklmnop/whip", "203.0.113.3:1")
	if n := allowed(3, "POST", "/api/streams", "203.0.113.3:1"); n != 1 {
		t.Fatalf("%d stream starts allowed, want 1", n)
	}

	for i, tc := range []struct {
		method, path string
		limit        int // 0: not limited
	}{
		{"POST", "/api/streams/abcdefghijklmnop/whep", playBurst},
		{"GET", "/api/realtime?tab=abcdefgh", connectBurst},
		{"PATCH", "/api/account", writeBurst},
		{"PUT", "/api/rooms/r/allowed/bob", writeBurst},
		{"DELETE", "/api/streams/abcdefghijklmnop", writeBurst},
		{"GET", "/api/rooms/r", 0},
		{"GET", "/", 0},
		{"PATCH", "/api/media/abcdefghijklmnop/whep/s", 0},
		{"DELETE", "/api/media/abcdefghijklmnop/whep/s", 0},
		{"POST", "/api/media/abcdefghijklmnop/whep/s/pause", 0},
	} {
		remote := fmt.Sprintf("[2001:db8:ffff:%d::1]:1", i) // fresh limits per case
		n, want := allowed(200, tc.method, tc.path, remote), tc.limit
		if want == 0 {
			want = 200
		}
		if n != want {
			t.Errorf("%s %s: %d of 200 allowed, want %d", tc.method, tc.path, n, want)
		}
	}

	// One more request per refill.
	now = now.Add(publishRefill)
	if n := allowed(3, "POST", "/whip", "203.0.113.3:1"); n != 1 {
		t.Fatalf("%d allowed after one refill, want 1", n)
	}
}

func TestRateLimitsOff(t *testing.T) {
	h := rateLimits(false, time.Now, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	for range 2 * signInBurst {
		w := httptest.NewRecorder()
		h.ServeHTTP(w, httptest.NewRequest("GET", "/api/auth/google/start", nil))
		if w.Code != http.StatusOK {
			t.Fatal("limited although rate limits are off")
		}
	}
}

// Behind the tunnel, each visitor has their own limits.
func TestRateLimitsPerVisitor(t *testing.T) {
	b := newTestEnv(t, envOptions{clientIPHeader: "CF-Connecting-IP"}).browser(t)
	for i := range signInBurst {
		if r := b.do("GET", "/api/auth/nope/start", "", "CF-Connecting-IP", "203.0.113.9"); r.status == http.StatusTooManyRequests {
			t.Fatalf("request %d limited", i+1)
		}
	}
	if r := b.do("GET", "/api/auth/nope/start", "", "CF-Connecting-IP", "203.0.113.9"); r.status != http.StatusTooManyRequests {
		t.Fatalf("got %d over the limit", r.status)
	}
	if r := b.do("GET", "/api/auth/nope/start", "", "CF-Connecting-IP", "203.0.113.10"); r.status == http.StatusTooManyRequests {
		t.Fatal("another visitor is limited")
	}
}
