package httpapi

import (
	"database/sql"
	"errors"
	"net/http"
	"net/url"

	"github.com/RAdu2005/cupstream/server/internal/api"
	"github.com/RAdu2005/cupstream/server/internal/auth"
	"github.com/RAdu2005/cupstream/server/internal/view"
)

// The desktop app's sign-in (see package auth): the website's /app/authorize
// page asks the signed-in user to approve the app, appAuthorize issues a
// code for the app's loopback address, and the app exchanges it in appToken.

// appAuthorize serves POST /api/app/authorize.
func (s *Server) appAuthorize(w http.ResponseWriter, r *http.Request, cur auth.Current) {
	var req api.AppAuthorizeRequest
	if !decodeJSON(w, r, &req) {
		return
	}
	switch {
	case cur.Kind != auth.KindWeb:
		writeError(w, http.StatusForbidden, "approve the app from the website")
	case !auth.ValidAppRedirect(req.RedirectURI):
		writeErrorCode(w, http.StatusBadRequest, "invalid_request", "the app's address must be http://127.0.0.1:<port>")
	case req.CodeChallengeMethod != "S256" || !auth.ValidCodeChallenge(req.CodeChallenge):
		writeErrorCode(w, http.StatusBadRequest, "invalid_request", "the app's request has no valid PKCE challenge")
	case len(req.State) > 256:
		writeErrorCode(w, http.StatusBadRequest, "invalid_request", "state is too long")
	default:
		code := s.AppCodes.Issue(cur.User.ID, req.RedirectURI, req.CodeChallenge, req.DeviceName)
		if code == "" {
			writeError(w, http.StatusServiceUnavailable, "too many sign-ins at once; try again in a minute")
			return
		}
		u, _ := url.Parse(req.RedirectURI)
		q := u.Query()
		q.Set("code", code)
		q.Set("state", req.State)
		u.RawQuery = q.Encode()
		writeJSON(w, http.StatusOK, api.AppAuthorizeResponse{Redirect: u.String()})
	}
}

// appToken serves POST /api/app/token: the app exchanges its code and PKCE
// verifier for an app session.
func (s *Server) appToken(w http.ResponseWriter, r *http.Request) {
	var req api.AppTokenRequest
	if !decodeJSON(w, r, &req) {
		return
	}
	userID, device, ok := s.AppCodes.Redeem(req.Code, req.CodeVerifier, req.RedirectURI)
	if !ok {
		writeErrorCode(w, http.StatusBadRequest, "invalid_grant", "the sign-in code is invalid or expired; sign in again")
		return
	}
	user, err := s.DB.GetUser(r.Context(), userID)
	if errors.Is(err, sql.ErrNoRows) {
		writeErrorCode(w, http.StatusBadRequest, "invalid_grant", "this account no longer exists")
		return
	}
	if err != nil {
		internalError(w, r, err)
		return
	}
	token, err := s.Sessions.StartApp(r.Context(), user.ID, device)
	if err != nil {
		internalError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, api.AppToken{Token: token, User: view.User(user)})
}
