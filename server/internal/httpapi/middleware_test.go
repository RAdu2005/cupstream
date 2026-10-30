package httpapi

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestClientIPFrom(t *testing.T) {
	var got string
	h := clientIPFrom("CF-Connecting-IP", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got = r.RemoteAddr
	}))
	for _, tc := range []struct {
		remote, header, want string
	}{
		{"127.0.0.1:5000", "203.0.113.7", "203.0.113.7:0"},
		{"[::1]:5000", "2001:db8::1", "[2001:db8::1]:0"},
		{"127.0.0.1:5000", "", "127.0.0.1:5000"},
		{"127.0.0.1:5000", "not an ip", "127.0.0.1:5000"},
		// Only the local connector is trusted.
		{"198.51.100.2:5000", "203.0.113.7", "198.51.100.2:5000"},
	} {
		r := httptest.NewRequest(http.MethodGet, "/", nil)
		r.RemoteAddr = tc.remote
		if tc.header != "" {
			r.Header.Set("CF-Connecting-IP", tc.header)
		}
		h.ServeHTTP(httptest.NewRecorder(), r)
		if got != tc.want {
			t.Errorf("remote %s, header %q: RemoteAddr = %s, want %s", tc.remote, tc.header, got, tc.want)
		}
	}
}

func TestRedirectToHTTPS(t *testing.T) {
	h := redirectToHTTPS(true, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	for _, tc := range []struct {
		remote, proto string
		status        int
	}{
		{"127.0.0.1:5000", "http", http.StatusMovedPermanently},
		{"127.0.0.1:5000", "https", http.StatusOK},
		{"127.0.0.1:5000", "", http.StatusOK},
		{"198.51.100.2:5000", "http", http.StatusOK},
	} {
		r := httptest.NewRequest(http.MethodGet, "http://cupstream.example/privacy?x=1", nil)
		r.RemoteAddr = tc.remote
		if tc.proto != "" {
			r.Header.Set("X-Forwarded-Proto", tc.proto)
		}
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, r)
		if rec.Code != tc.status {
			t.Errorf("remote %s, proto %q: status %d, want %d", tc.remote, tc.proto, rec.Code, tc.status)
		}
		if tc.status == http.StatusMovedPermanently && rec.Header().Get("Location") != "https://cupstream.example/privacy?x=1" {
			t.Errorf("Location = %q", rec.Header().Get("Location"))
		}
	}
}
