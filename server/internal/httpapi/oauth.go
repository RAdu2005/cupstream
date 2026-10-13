package httpapi

import (
	"crypto/rand"
	"encoding/base64"
	"errors"
	"log/slog"
	"net/http"
	"net/url"
	"strings"
	"time"

	"golang.org/x/oauth2"

	"github.com/RAdu2005/cupstream/server/internal/api"
	"github.com/RAdu2005/cupstream/server/internal/auth"
	"github.com/RAdu2005/cupstream/server/internal/gateway"
	"github.com/RAdu2005/cupstream/server/internal/oauth"
	"github.com/RAdu2005/cupstream/server/internal/seal"
)

const (
	flowCookie  = "cupstream_oauth"
	mergeCookie = "cupstream_merge"
	flowTTL     = 10 * time.Minute
	mergeTTL    = 10 * time.Minute

	modeLogin = "login"
	modeLink  = "link"
)

// oauthFlow is kept in a sealed cookie between start and callback.
type oauthFlow struct {
	Provider string `json:"p"`
	State    string `json:"s"`
	Verifier string `json:"v"`
	Nonce    string `json:"n"`
	Mode     string `json:"m"`
	UserID   string `json:"u,omitempty"` // link mode: who started it
	Next     string `json:"x"`
}

// pendingMerge is kept in a sealed cookie until the user confirms or cancels.
type pendingMerge struct {
	UserID   string        `json:"u"`
	OtherID  string        `json:"o"`
	Provider string        `json:"p"`
	Username string        `json:"n"`
	Token    *oauth2.Token `json:"t,omitempty"` // Discord's token pair
}

func randomString() string {
	b := make([]byte, 16)
	rand.Read(b)
	return base64.RawURLEncoding.EncodeToString(b)
}

// safeNext keeps redirects on this site.
func safeNext(next, def string) string {
	if !strings.HasPrefix(next, "/") || strings.HasPrefix(next, "//") || strings.HasPrefix(next, "/\\") {
		return def
	}
	return next
}

func withQuery(path string, kv ...string) string {
	u, _ := url.Parse(path)
	q := u.Query()
	for i := 0; i+1 < len(kv); i += 2 {
		q.Set(kv[i], kv[i+1])
	}
	u.RawQuery = q.Encode()
	return u.String()
}

func (s *Server) setCookie(w http.ResponseWriter, name, path, value string, ttl time.Duration) {
	maxAge := int(ttl.Seconds())
	if value == "" {
		maxAge = -1
	}
	http.SetCookie(w, &http.Cookie{
		Name: name, Value: value, Path: path, MaxAge: maxAge,
		HttpOnly: true, Secure: s.Config.SecureCookies(), SameSite: http.SameSiteLaxMode,
	})
}

// oauthStart serves GET /api/auth/{provider}/start?mode=login|link&next=/path.
func (s *Server) oauthStart(w http.ResponseWriter, r *http.Request) {
	p := s.Providers[r.PathValue("provider")]
	if p == nil {
		writeError(w, http.StatusNotFound, "this sign-in method is not configured")
		return
	}
	flow := oauthFlow{
		Provider: p.Name(),
		State:    randomString(),
		Verifier: oauth2.GenerateVerifier(),
		Nonce:    randomString(),
		Mode:     modeLogin,
		Next:     safeNext(r.URL.Query().Get("next"), "/"),
	}
	if r.URL.Query().Get("mode") == modeLink {
		cur, ok := auth.FromContext(r.Context())
		if !ok {
			http.Redirect(w, r, "/login", http.StatusFound)
			return
		}
		flow.Mode, flow.UserID = modeLink, cur.User.ID
		flow.Next = safeNext(r.URL.Query().Get("next"), "/settings")
	}

	target, err := p.AuthURL(r.Context(), flow.State, flow.Verifier, flow.Nonce)
	if err != nil {
		slog.ErrorContext(r.Context(), "oauth start failed", "provider", p.Name(), "err", err)
		http.Redirect(w, r, withQuery(errorPage(flow), "error", "provider_unavailable"), http.StatusFound)
		return
	}
	sealed, err := seal.SealJSON(s.Sealer, flowCookie, flow, flowTTL)
	if err != nil {
		internalError(w, r, err)
		return
	}
	s.setCookie(w, flowCookie, "/api/auth/", sealed, flowTTL)
	http.Redirect(w, r, target, http.StatusFound)
}

func errorPage(f oauthFlow) string {
	if f.Mode == modeLink {
		return "/settings"
	}
	return "/login"
}

// oauthCallback serves GET /api/auth/{provider}/callback.
func (s *Server) oauthCallback(w http.ResponseWriter, r *http.Request) {
	fail := func(page, code string) { http.Redirect(w, r, withQuery(page, "error", code), http.StatusFound) }

	c, err := r.Cookie(flowCookie)
	if err != nil {
		fail("/login", "expired")
		return
	}
	s.setCookie(w, flowCookie, "/api/auth/", "", 0)
	flow, err := seal.OpenJSON[oauthFlow](s.Sealer, flowCookie, c.Value)
	q := r.URL.Query()
	if err != nil || flow.Provider != r.PathValue("provider") || flow.State != q.Get("state") {
		fail("/login", "expired")
		return
	}
	if q.Get("error") != "" {
		fail(errorPage(flow), "cancelled")
		return
	}
	p := s.Providers[flow.Provider]
	if p == nil {
		fail("/login", "provider_unavailable")
		return
	}
	id, err := p.Exchange(r.Context(), q.Get("code"), flow.Verifier, flow.Nonce)
	if err != nil {
		slog.WarnContext(r.Context(), "oauth exchange failed", "provider", flow.Provider, "err", err)
		fail(errorPage(flow), "provider_error")
		return
	}

	if flow.Mode == modeLogin {
		user, _, err := s.Accounts.SignIn(r.Context(), id)
		if err == nil {
			err = s.switchSession(w, r, user.ID)
		}
		if err != nil {
			slog.ErrorContext(r.Context(), "sign-in failed", "err", err)
			fail("/login", "internal")
			return
		}
		http.Redirect(w, r, flow.Next, http.StatusFound)
		return
	}

	// Link mode: the same user must still be signed in.
	cur, ok := auth.FromContext(r.Context())
	if !ok || cur.User.ID != flow.UserID {
		fail("/login", "session_changed")
		return
	}
	err = s.Accounts.Link(r.Context(), cur.User.ID, id)
	var owned *auth.OwnedByOtherError
	switch {
	case errors.As(err, &owned):
		sealed, err := seal.SealJSON(s.Sealer, mergeCookie, pendingMerge{
			UserID: cur.User.ID, OtherID: owned.OtherUserID, Provider: id.Provider, Username: id.Username, Token: id.Token,
		}, mergeTTL)
		if err != nil {
			internalError(w, r, err)
			return
		}
		s.setCookie(w, mergeCookie, "/api/account/merge", sealed, mergeTTL)
		http.Redirect(w, r, withQuery("/settings", "merge", "1"), http.StatusFound)
	case errors.Is(err, auth.ErrProviderLinked):
		fail("/settings", "provider_linked")
	case err != nil:
		slog.ErrorContext(r.Context(), "linking failed", "err", err)
		fail("/settings", "internal")
	default:
		http.Redirect(w, r, withQuery(flow.Next, "linked", id.Provider), http.StatusFound)
	}
}

func (s *Server) readMerge(r *http.Request, cur auth.Current) (pendingMerge, bool) {
	c, err := r.Cookie(mergeCookie)
	if err != nil {
		return pendingMerge{}, false
	}
	m, err := seal.OpenJSON[pendingMerge](s.Sealer, mergeCookie, c.Value)
	return m, err == nil && m.UserID == cur.User.ID
}

func (s *Server) pendingMerge(w http.ResponseWriter, r *http.Request, cur auth.Current) {
	m, ok := s.readMerge(r, cur)
	if !ok {
		writeError(w, http.StatusNotFound, "no account merge is pending")
		return
	}
	other, err := s.DB.GetUser(r.Context(), m.OtherID)
	if err != nil {
		writeError(w, http.StatusNotFound, "no account merge is pending")
		return
	}
	writeJSON(w, http.StatusOK, api.PendingMerge{Provider: m.Provider, Username: m.Username, Other: userJSON(other)})
}

func (s *Server) confirmMerge(w http.ResponseWriter, r *http.Request, cur auth.Current) {
	m, ok := s.readMerge(r, cur)
	if !ok {
		writeError(w, http.StatusNotFound, "no account merge is pending")
		return
	}
	s.setCookie(w, mergeCookie, "/api/account/merge", "", 0)
	s.Gateway.EndUserStreams(r.Context(), m.OtherID, gateway.EndAccountMerged)
	if err := s.Accounts.Merge(r.Context(), cur.User.ID, m.OtherID); err != nil {
		internalError(w, r, err)
		return
	}
	if m.Provider == oauth.Discord && m.Token != nil && s.Discord != nil {
		if err := s.Discord.Store(r.Context(), cur.User.ID, m.Token); err != nil {
			slog.WarnContext(r.Context(), "storing discord token after merge failed", "err", err)
		}
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) cancelMerge(w http.ResponseWriter, r *http.Request, cur auth.Current) {
	s.setCookie(w, mergeCookie, "/api/account/merge", "", 0)
	w.WriteHeader(http.StatusNoContent)
}
