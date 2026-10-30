// Package httpapi is the public HTTP entry point: the JSON API, OAuth
// redirects, WHIP/WHEP signaling (handed to the gateway), the realtime
// WebSocket (handed to the hub), the health check and the website.
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
	"github.com/RAdu2005/cupstream/server/internal/gateway"
	"github.com/RAdu2005/cupstream/server/internal/mediamtx"
	"github.com/RAdu2005/cupstream/server/internal/oauth"
	"github.com/RAdu2005/cupstream/server/internal/realtime"
	"github.com/RAdu2005/cupstream/server/internal/rooms"
	"github.com/RAdu2005/cupstream/server/internal/seal"
	"github.com/RAdu2005/cupstream/server/internal/store"
	"github.com/RAdu2005/cupstream/server/internal/view"
)

// Deps are the services the HTTP layer uses.
type Deps struct {
	Config    *config.Config
	Version   string // the build's commit, reported by /healthz
	DB        *store.DB
	Sealer    *seal.Sealer
	Sessions  *auth.Sessions
	Accounts  *auth.Accounts
	AppCodes  *auth.AppCodes
	Discord   *auth.DiscordSync // nil without Discord sign-in
	Providers map[string]oauth.Provider
	Rooms     *rooms.Service
	Gateway   *gateway.Gateway
	Realtime  *realtime.Hub
	MediaMTX  *mediamtx.Client
	Web       http.Handler
}

type Server struct{ Deps }

func New(d Deps) *Server { return &Server{d} }

// Handler returns the root handler with all middleware applied.
func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", s.health)

	// Sign-in.
	mux.HandleFunc("GET /api/auth/providers", s.authProviders)
	mux.HandleFunc("POST /api/auth/logout", s.logout)
	if s.Config.DevLogin {
		mux.HandleFunc("POST /api/auth/dev/login", s.devLogin)
	}
	mux.HandleFunc("GET /api/auth/{provider}/start", s.oauthStart)
	mux.HandleFunc("GET /api/auth/{provider}/callback", s.oauthCallback)
	mux.Handle("GET /api/me", requireUser(s.me))
	mux.Handle("POST /api/app/authorize", requireUser(s.appAuthorize))
	mux.HandleFunc("POST /api/app/token", s.appToken)

	// Account settings.
	mux.Handle("GET /api/account", requireUser(s.account))
	mux.Handle("PATCH /api/account", requireUser(s.updateProfile))
	mux.Handle("PATCH /api/account/settings", requireUser(s.updateSettings))
	mux.Handle("DELETE /api/account/identities/{provider}", requireUser(s.unlink))
	mux.Handle("GET /api/account/sessions", requireUser(s.listSessions))
	mux.Handle("DELETE /api/account/sessions/{id}", requireUser(s.revokeSession))
	mux.Handle("GET /api/account/guilds", requireUser(s.listGuilds))
	mux.Handle("PATCH /api/account/guilds/{id}", requireUser(s.updateGuild))
	mux.Handle("GET /api/account/merge", requireUser(s.pendingMerge))
	mux.Handle("POST /api/account/merge", requireUser(s.confirmMerge))
	mux.Handle("DELETE /api/account/merge", requireUser(s.cancelMerge))
	mux.Handle("GET /api/account/stream-key", requireUser(s.streamKey))
	mux.Handle("POST /api/account/stream-key", requireUser(s.regenerateStreamKey))

	// Rooms and streams.
	mux.HandleFunc("GET /api/rooms/{slug}", s.getRoom)
	mux.Handle("PATCH /api/rooms/{slug}", requireUser(s.updateRoom))
	mux.Handle("GET /api/rooms/{slug}/allowed", requireUser(s.listAllowed))
	mux.Handle("PUT /api/rooms/{slug}/allowed/{handle}", requireUser(s.setAllowed))
	mux.Handle("DELETE /api/rooms/{slug}/allowed/{handle}", requireUser(s.removeAllowed))
	mux.Handle("POST /api/streams", requireUser(s.startStream))
	mux.Handle("DELETE /api/streams/{id}", requireUser(s.endStream))

	// Media signaling.
	mux.HandleFunc("POST /whip", s.Gateway.HandleOBSPublish)
	mux.HandleFunc("POST /api/streams/{id}/whip", s.Gateway.HandleTokenPublish)
	mux.HandleFunc("POST /api/streams/{id}/whep", s.play)
	mux.HandleFunc("PATCH /api/media/{stream}/{kind}/{secret}", s.Gateway.HandleSession)
	mux.HandleFunc("DELETE /api/media/{stream}/{kind}/{secret}", s.Gateway.HandleSession)
	mux.HandleFunc("POST /api/media/{stream}/whep/{secret}/{action}", s.Gateway.HandleSessionControl)

	// Realtime: stream events, presence and chat. The hub checks the Origin
	// header itself, since the cross-origin protection below skips GET.
	mux.HandleFunc("GET /api/realtime", s.Realtime.Serve)

	mux.HandleFunc("/api/", func(w http.ResponseWriter, r *http.Request) {
		writeError(w, http.StatusNotFound, "not found")
	})
	mux.Handle("/", s.Web)

	// Rejects cross-origin state-changing requests from browsers
	// (Sec-Fetch-Site / Origin vs Host); OBS sends neither header.
	csrf := http.NewCrossOriginProtection()
	h := csrf.Handler(s.Sessions.Middleware(mux))
	h = logRequests(h)
	// Behind a tunnel or reverse proxy (CUPSTREAM_CLIENT_IP_HEADER): use the
	// visitor's address, and send plain-HTTP visitors to HTTPS.
	h = clientIPFrom(s.Config.ClientIPHeader, h)
	h = redirectToHTTPS(s.Config.ClientIPHeader != "" && s.Config.SecureCookies(), h)
	h = securityHeaders(s.Config.SecureCookies(), h)
	return recoverPanics(h)
}

func (s *Server) health(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), 2*time.Second)
	defer cancel()

	h := api.Health{Status: "ok", Version: s.Version, Database: s.DB.SQL.PingContext(ctx) == nil}
	if info, err := s.MediaMTX.Info(ctx); err == nil {
		f := mediamtx.FeaturesOf(info.Version)
		h.MediaMTX = api.MediaMTXHealth{Reachable: true, Version: info.Version, Pause: f.Pause, Events: f.Events}
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
	writeJSON(w, http.StatusOK, api.Me{User: view.User(cur.User)})
}

// userID returns the signed-in user's ID, or "".
func userID(r *http.Request) string {
	if cur, ok := auth.FromContext(r.Context()); ok {
		return cur.User.ID
	}
	return ""
}

// requireUser answers 401 unless the request has a signed-in user.
func requireUser(h func(http.ResponseWriter, *http.Request, auth.Current)) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		cur, ok := auth.FromContext(r.Context())
		if !ok {
			writeErrorCode(w, http.StatusUnauthorized, "sign_in_required", "sign in required")
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

func writeErrorCode(w http.ResponseWriter, status int, code, msg string) {
	writeJSON(w, status, api.ErrorResponse{Error: msg, Code: code})
}

func internalError(w http.ResponseWriter, r *http.Request, err error) {
	slog.ErrorContext(r.Context(), "request failed", "method", r.Method, "path", r.URL.Path, "err", err)
	writeError(w, http.StatusInternalServerError, "internal error")
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
