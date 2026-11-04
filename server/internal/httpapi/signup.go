package httpapi

import (
	"errors"
	"net/http"
	"time"

	"golang.org/x/oauth2"

	"github.com/RAdu2005/cupstream/server/internal/api"
	"github.com/RAdu2005/cupstream/server/internal/auth"
	"github.com/RAdu2005/cupstream/server/internal/oauth"
	"github.com/RAdu2005/cupstream/server/internal/seal"
	"github.com/RAdu2005/cupstream/server/internal/view"
)

const (
	signUpCookie = "cupstream_signup"
	signUpTTL    = 30 * time.Minute
)

// pendingSignUp is kept in a sealed cookie while a new user, who signed in
// with a provider, chooses their handle on /signup.
type pendingSignUp struct {
	Provider    string        `json:"p"`
	Subject     string        `json:"s"`
	Username    string        `json:"u"`
	DisplayName string        `json:"d"`
	AvatarURL   string        `json:"a,omitempty"`
	Token       *oauth2.Token `json:"t,omitempty"` // Discord's token pair
	Next        string        `json:"x"`
}

func (p pendingSignUp) identity() oauth.Identity {
	return oauth.Identity{
		Provider: p.Provider, Subject: p.Subject, Username: p.Username,
		DisplayName: p.DisplayName, AvatarURL: p.AvatarURL, Token: p.Token,
	}
}

func (s *Server) signUpRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/auth/signup", s.pendingSignUp)
	mux.HandleFunc("POST /api/auth/signup", s.signUp)
	mux.HandleFunc("DELETE /api/auth/signup", s.cancelSignUp)
	mux.HandleFunc("GET /api/auth/handle", s.checkHandle)
}

// startSignUp keeps id for the sign-up page and sends the browser there.
func (s *Server) startSignUp(w http.ResponseWriter, r *http.Request, id oauth.Identity, next string) {
	sealed, err := seal.SealJSON(s.Sealer, signUpCookie, pendingSignUp{
		Provider: id.Provider, Subject: id.Subject, Username: id.Username,
		DisplayName: id.DisplayName, AvatarURL: id.AvatarURL, Token: id.Token, Next: next,
	}, signUpTTL)
	if err != nil {
		internalError(w, r, err)
		return
	}
	s.setCookie(w, signUpCookie, sealed, signUpTTL)
	http.Redirect(w, r, withQuery("/signup", "next", next), http.StatusFound)
}

func (s *Server) readSignUp(r *http.Request) (pendingSignUp, bool) {
	c, err := r.Cookie(s.cookieName(signUpCookie))
	if err != nil {
		return pendingSignUp{}, false
	}
	p, err := seal.OpenJSON[pendingSignUp](s.Sealer, signUpCookie, c.Value)
	return p, err == nil
}

// pendingSignUp serves GET /api/auth/signup.
func (s *Server) pendingSignUp(w http.ResponseWriter, r *http.Request) {
	p, ok := s.readSignUp(r)
	if !ok {
		writeError(w, http.StatusNotFound, "no sign-up is pending")
		return
	}
	handle, err := s.Accounts.SuggestHandle(r.Context(), p.identity())
	if err != nil {
		internalError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, api.PendingSignUp{
		Provider: p.Provider, Username: p.Username, DisplayName: p.DisplayName, AvatarURL: p.AvatarURL, Handle: handle,
	})
}

// signUp serves POST /api/auth/signup: it creates the account with the
// chosen handle and signs the new user in.
func (s *Server) signUp(w http.ResponseWriter, r *http.Request) {
	p, ok := s.readSignUp(r)
	if !ok {
		writeErrorCode(w, http.StatusNotFound, "expired", "the sign-up expired; sign in again")
		return
	}
	var req api.SignUpRequest
	if !decodeJSON(w, r, &req) {
		return
	}
	user, err := s.Accounts.SignUp(r.Context(), p.identity(), req.Handle, req.DisplayName)
	switch {
	case errors.Is(err, auth.ErrHandleTaken):
		writeError(w, http.StatusConflict, err.Error())
		return
	case errors.Is(err, auth.ErrInvalidHandle), errors.Is(err, auth.ErrInvalidName):
		writeError(w, http.StatusBadRequest, err.Error())
		return
	case errors.Is(err, auth.ErrBanned):
		s.setCookie(w, signUpCookie, "", 0)
		writeErrorCode(w, http.StatusForbidden, "banned", err.Error())
		return
	case err != nil:
		internalError(w, r, err)
		return
	}
	if err := s.switchSession(w, r, user.ID); err != nil {
		internalError(w, r, err)
		return
	}
	s.setCookie(w, signUpCookie, "", 0)
	writeJSON(w, http.StatusOK, api.Me{User: view.User(user)})
}

func (s *Server) cancelSignUp(w http.ResponseWriter, r *http.Request) {
	s.setCookie(w, signUpCookie, "", 0)
	w.WriteHeader(http.StatusNoContent)
}

// checkHandle serves GET /api/auth/handle?handle=, for the sign-up page and
// settings, which say whether a handle is free as it is typed.
func (s *Server) checkHandle(w http.ResponseWriter, r *http.Request) {
	h, err := auth.NormalizeHandle(r.URL.Query().Get("handle"))
	if err != nil {
		writeJSON(w, http.StatusOK, api.HandleCheck{Error: err.Error()})
		return
	}
	ok, err := s.Accounts.HandleAvailable(r.Context(), userID(r), h)
	if err != nil {
		internalError(w, r, err)
		return
	}
	out := api.HandleCheck{Available: ok}
	if !ok {
		out.Error = auth.ErrHandleTaken.Error()
	}
	writeJSON(w, http.StatusOK, out)
}
