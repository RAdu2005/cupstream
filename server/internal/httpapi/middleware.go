package httpapi

import (
	"log/slog"
	"net"
	"net/http"
	"runtime/debug"
	"time"
)

type statusRecorder struct {
	http.ResponseWriter
	status int
}

func (r *statusRecorder) WriteHeader(code int) {
	if r.status == 0 {
		r.status = code
	}
	r.ResponseWriter.WriteHeader(code)
}

func (r *statusRecorder) Write(b []byte) (int, error) {
	if r.status == 0 {
		r.status = http.StatusOK
	}
	return r.ResponseWriter.Write(b)
}

// Unwrap lets http.ResponseController reach Flush and Hijack (WebSockets,
// streamed proxy responses) on the underlying writer.
func (r *statusRecorder) Unwrap() http.ResponseWriter { return r.ResponseWriter }

func logRequests(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		rec := &statusRecorder{ResponseWriter: w}
		next.ServeHTTP(rec, r)
		level := slog.LevelInfo
		switch {
		case rec.status >= 500:
			level = slog.LevelError
		case r.URL.Path == "/healthz" || (rec.status < 400 && r.Method == http.MethodGet):
			level = slog.LevelDebug
		}
		slog.Log(r.Context(), level, "http request",
			"method", r.Method,
			"path", r.URL.Path,
			"status", rec.status,
			"duration_ms", time.Since(start).Milliseconds(),
		)
	})
}

func recoverPanics(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer func() {
			if v := recover(); v != nil {
				if v == http.ErrAbortHandler {
					panic(v)
				}
				slog.ErrorContext(r.Context(), "handler panic", "panic", v, "stack", string(debug.Stack()))
				writeError(w, http.StatusInternalServerError, "internal error")
			}
		}()
		next.ServeHTTP(w, r)
	})
}

// clientIPFrom replaces the request's remote address with the one in header
// when the request comes from this machine, i.e. from a reverse proxy or
// tunnel connector running next to the server. Everything downstream (the
// development login's loopback check, MediaMTX's X-Forwarded-For) then sees
// the real client.
func clientIPFrom(header string, next http.Handler) http.Handler {
	if header == "" {
		return next
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if isLoopback(r) {
			if ip := net.ParseIP(r.Header.Get(header)); ip != nil {
				r2 := r.Clone(r.Context())
				r2.RemoteAddr = net.JoinHostPort(ip.String(), "0")
				r = r2
			}
		}
		next.ServeHTTP(w, r)
	})
}

// redirectToHTTPS sends visitors who reached the proxy in front of the server
// over plain HTTP to the HTTPS address. The proxy reports the visitor's
// scheme in X-Forwarded-Proto, trusted only on connections from this machine.
func redirectToHTTPS(enabled bool, next http.Handler) http.Handler {
	if !enabled {
		return next
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if isLoopback(r) && r.Header.Get("X-Forwarded-Proto") == "http" {
			u := *r.URL
			u.Scheme, u.Host = "https", r.Host
			http.Redirect(w, r, u.String(), http.StatusMovedPermanently)
			return
		}
		next.ServeHTTP(w, r)
	})
}

// contentSecurityPolicy allows only the site's own scripts, styles and API,
// plus Google and Discord avatars and server icons. WebRTC media is not
// covered by CSP; video elements play it through srcObject.
const contentSecurityPolicy = "default-src 'self'; " +
	"img-src 'self' data: https://*.googleusercontent.com https://cdn.discordapp.com; " +
	"object-src 'none'; base-uri 'none'; form-action 'self'; frame-ancestors 'none'"

// securityHeaders sets browser protections on every response: the content
// security policy, no framing by other sites (clickjacking), no MIME
// sniffing, origin-only referrers to other sites, and, when the site is
// served over HTTPS, HSTS for this host only (no includeSubDomains: other
// subdomains of the domain are not ours to decide for).
func securityHeaders(hsts bool, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h := w.Header()
		h.Set("Content-Security-Policy", contentSecurityPolicy)
		h.Set("X-Frame-Options", "DENY")
		h.Set("X-Content-Type-Options", "nosniff")
		h.Set("Referrer-Policy", "strict-origin-when-cross-origin")
		h.Set("Cross-Origin-Opener-Policy", "same-origin")
		if hsts {
			h.Set("Strict-Transport-Security", "max-age=31536000")
		}
		next.ServeHTTP(w, r)
	})
}
