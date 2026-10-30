package httpapi

import (
	"log/slog"
	"net"
	"net/http"

	"github.com/RAdu2005/cupstream/server/internal/api"
	"github.com/RAdu2005/cupstream/server/internal/auth"
	"github.com/RAdu2005/cupstream/server/internal/config"
	"github.com/RAdu2005/cupstream/server/internal/oauth"
	"github.com/RAdu2005/cupstream/server/internal/view"
)

func (s *Server) authProviders(w http.ResponseWriter, r *http.Request) {
	providers := []string{}
	for _, name := range []string{oauth.Google, oauth.Discord} {
		if s.Providers[name] != nil {
			providers = append(providers, name)
		}
	}
	if s.Config.DevLogin {
		providers = append(providers, auth.ProviderDev)
	}
	writeJSON(w, http.StatusOK, api.AuthProviders{Providers: providers})
}

func (s *Server) logout(w http.ResponseWriter, r *http.Request) {
	if err := s.Sessions.End(r.Context(), w, r); err != nil {
		internalError(w, r, err)
		return
	}
	if cur, ok := auth.FromContext(r.Context()); ok {
		s.Realtime.CloseSession(cur.SessionID)
	}
	w.WriteHeader(http.StatusNoContent)
}

// devLogin signs in as any handle without a password, creating the user on
// first use. It exists for local development and end-to-end tests, and is
// only registered when CUPSTREAM_DEV_LOGIN is set; it also refuses requests
// that are not addressed to and coming from this machine.
func (s *Server) devLogin(w http.ResponseWriter, r *http.Request) {
	host, _, err := net.SplitHostPort(r.Host)
	if err != nil {
		host = r.Host
	}
	if !config.IsLocalHost(host) || !isLoopback(r) {
		writeError(w, http.StatusForbidden, "the development login only works on a local development host")
		return
	}

	var req api.DevLoginRequest
	if !decodeJSON(w, r, &req) {
		return
	}
	handle, err := auth.NormalizeHandle(req.Handle)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}

	user, _, err := s.Accounts.SignIn(r.Context(), oauth.Identity{
		Provider: auth.ProviderDev, Subject: handle, Username: handle, DisplayName: handle,
	})
	if err == nil {
		err = s.switchSession(w, r, user.ID)
	}
	if err != nil {
		internalError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, api.Me{User: view.User(user)})
}

// switchSession ends the request's session, if any, and starts one for userID.
func (s *Server) switchSession(w http.ResponseWriter, r *http.Request, userID string) error {
	if err := s.Sessions.End(r.Context(), w, r); err != nil {
		slog.WarnContext(r.Context(), "ending previous session failed", "err", err)
	}
	return s.Sessions.Start(r.Context(), w, userID)
}
