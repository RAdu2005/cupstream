package httpapi

import (
	"errors"
	"log/slog"
	"net"
	"net/http"

	"github.com/RAdu2005/cupstream/server/internal/api"
	"github.com/RAdu2005/cupstream/server/internal/auth"
	"github.com/RAdu2005/cupstream/server/internal/config"
)

func (s *Server) authProviders(w http.ResponseWriter, r *http.Request) {
	providers := []string{}
	if s.cfg.DevLogin {
		providers = append(providers, auth.ProviderDev)
	}
	writeJSON(w, http.StatusOK, api.AuthProviders{Providers: providers})
}

func (s *Server) logout(w http.ResponseWriter, r *http.Request) {
	if err := s.sessions.End(r.Context(), w, r); err != nil {
		slog.ErrorContext(r.Context(), "logout failed", "err", err)
		writeError(w, http.StatusInternalServerError, "internal error")
		return
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

	user, err := auth.SignIn(r.Context(), s.db, auth.Identity{
		Provider:    auth.ProviderDev,
		Subject:     handle,
		Username:    handle,
		Handle:      handle,
		DisplayName: handle,
	})
	if errors.Is(err, auth.ErrHandleTaken) {
		writeError(w, http.StatusConflict, "this handle belongs to a user who signs in with another provider")
		return
	}
	if err == nil {
		err = s.sessions.Start(r.Context(), w, user.ID, auth.KindWeb, "")
	}
	if err != nil {
		slog.ErrorContext(r.Context(), "dev login failed", "err", err)
		writeError(w, http.StatusInternalServerError, "internal error")
		return
	}
	writeJSON(w, http.StatusOK, api.Me{User: userJSON(user)})
}
