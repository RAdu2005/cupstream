// Package httpapi is the public HTTP entry point: the JSON API, health check
// and the website.
package httpapi

import (
	"context"
	"encoding/json"
	"log/slog"
	"net"
	"net/http"
	"time"

	"github.com/RAdu2005/cupstream/server/internal/api"
	"github.com/RAdu2005/cupstream/server/internal/auth"
	"github.com/RAdu2005/cupstream/server/internal/config"
	"github.com/RAdu2005/cupstream/server/internal/mediamtx"
	"github.com/RAdu2005/cupstream/server/internal/store"
)

type Server struct {
	cfg      *config.Config
	db       *store.DB
	sessions *auth.Sessions
	media    *mediamtx.Client
	web      http.Handler
}

func New(cfg *config.Config, db *store.DB, sessions *auth.Sessions, media *mediamtx.Client, web http.Handler) *Server {
	return &Server{cfg: cfg, db: db, sessions: sessions, media: media, web: web}
}

// Handler returns the root handler with all middleware applied.
func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", s.health)

	mux.HandleFunc("GET /api/auth/providers", s.authProviders)
	mux.HandleFunc("POST /api/auth/logout", s.logout)
	if s.cfg.DevLogin {
		mux.HandleFunc("POST /api/auth/dev/login", s.devLogin)
	}
	mux.Handle("GET /api/me", requireUser(s.me))

	mux.HandleFunc("/api/", func(w http.ResponseWriter, r *http.Request) {
		writeError(w, http.StatusNotFound, "not found")
	})
	mux.Handle("/", s.web)

	// Rejects cross-origin state-changing requests (Sec-Fetch-Site / Origin vs Host).
	csrf := http.NewCrossOriginProtection()
	return recoverPanics(logRequests(csrf.Handler(s.sessions.Middleware(mux))))
}

func (s *Server) health(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), 2*time.Second)
	defer cancel()

	h := api.Health{Status: "ok", Database: s.db.SQL.PingContext(ctx) == nil}
	if info, err := s.media.Info(ctx); err == nil {
		h.MediaMTX = api.MediaMTXHealth{Reachable: true, Version: info.Version}
	}
	status := http.StatusOK
	if !h.Database || !h.MediaMTX.Reachable {
		h.Status = "degraded"
	}
	if !h.Database {
		status = http.StatusServiceUnavailable
	}
	writeJSON(w, status, h)
}

func (s *Server) me(w http.ResponseWriter, r *http.Request, cur auth.Current) {
	writeJSON(w, http.StatusOK, api.Me{User: userJSON(cur.User)})
}

func userJSON(u store.User) api.User {
	return api.User{ID: u.ID, Handle: u.Handle, DisplayName: u.DisplayName, AvatarURL: u.AvatarURL.String}
}

// requireUser answers 401 unless the request has a signed-in user.
func requireUser(h func(http.ResponseWriter, *http.Request, auth.Current)) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		cur, ok := auth.FromContext(r.Context())
		if !ok {
			writeError(w, http.StatusUnauthorized, "sign in required")
			return
		}
		h(w, r, cur)
	})
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	if err := json.NewEncoder(w).Encode(v); err != nil {
		slog.Warn("writing JSON response failed", "err", err)
	}
}

func writeError(w http.ResponseWriter, status int, msg string) {
	writeJSON(w, status, api.ErrorResponse{Error: msg})
}

// decodeJSON reads a small JSON body into v, answering 400 on failure.
func decodeJSON(w http.ResponseWriter, r *http.Request, v any) bool {
	dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, 64<<10))
	dec.DisallowUnknownFields()
	if err := dec.Decode(v); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON body")
		return false
	}
	return true
}

// isLoopback reports whether the request comes from this machine.
func isLoopback(r *http.Request) bool {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return false
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}
