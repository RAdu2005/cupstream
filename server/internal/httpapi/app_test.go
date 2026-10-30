package httpapi

import (
	"crypto/sha256"
	"encoding/base64"
	"net/http"
	"net/url"
	"strings"
	"testing"

	"github.com/RAdu2005/cupstream/server/internal/api"
)

func TestAppSignIn(t *testing.T) {
	e := newTestEnv(t, envOptions{devLogin: true})
	web := e.browser(t)
	alice := web.devLogin("alice")
	app := e.browser(t) // no cookies: the app's own HTTP client

	verifier := strings.Repeat("x", 64)
	sum := sha256.Sum256([]byte(verifier))
	authReq := api.AppAuthorizeRequest{
		RedirectURI: "http://127.0.0.1:43210/callback", State: "st4te",
		CodeChallenge: base64.RawURLEncoding.EncodeToString(sum[:]), CodeChallengeMethod: "S256",
		DeviceName: "alice-desktop",
	}

	// Approving needs a website session.
	app.json("POST", "/api/app/authorize", authReq, nil, http.StatusUnauthorized)
	var approved api.AppAuthorizeResponse
	web.json("POST", "/api/app/authorize", authReq, &approved, http.StatusOK)
	back, err := url.Parse(approved.Redirect)
	if err != nil || back.Host != "127.0.0.1:43210" || back.Path != "/callback" || back.Query().Get("state") != "st4te" {
		t.Fatalf("redirect = %q", approved.Redirect)
	}
	code := back.Query().Get("code")

	var tok api.AppToken
	app.json("POST", "/api/app/token", api.AppTokenRequest{Code: code, CodeVerifier: verifier, RedirectURI: authReq.RedirectURI}, &tok, http.StatusOK)
	if tok.Token == "" || tok.User.ID != alice.ID {
		t.Fatalf("token response = %+v", tok)
	}
	// The code works once.
	app.json("POST", "/api/app/token", api.AppTokenRequest{Code: code, CodeVerifier: verifier, RedirectURI: authReq.RedirectURI}, nil, http.StatusBadRequest)

	bearer := []string{"Authorization", "Bearer " + tok.Token}
	if r := app.do("GET", "/api/me", "", bearer...); r.status != http.StatusOK || decode[api.Me](t, r.body).User.ID != alice.ID {
		t.Fatalf("/api/me with the app token = %d %s", r.status, r.body)
	}
	// The app can go live with its session.
	if r := app.do("POST", "/api/streams", `{"source":"app","linkFallback":true}`, bearer...); r.status != http.StatusCreated {
		t.Fatalf("start stream = %d %s", r.status, r.body)
	}
	// An app session cannot approve another app.
	raw := `{"redirectUri":"http://127.0.0.1:1/","state":"","codeChallenge":"` + authReq.CodeChallenge + `","codeChallengeMethod":"S256","deviceName":""}`
	if r := app.do("POST", "/api/app/authorize", raw, bearer...); r.status != http.StatusForbidden {
		t.Fatalf("app approving an app = %d", r.status)
	}

	// It shows in the user's sessions with its device name, and signing out
	// ends it.
	var sessions []api.Session
	web.json("GET", "/api/account/sessions", nil, &sessions, http.StatusOK)
	found := false
	for _, se := range sessions {
		found = found || se.Kind == "app" && se.DeviceName == "alice-desktop"
	}
	if !found {
		t.Fatalf("sessions = %+v", sessions)
	}
	if r := app.do("POST", "/api/auth/logout", "", bearer...); r.status != http.StatusNoContent {
		t.Fatalf("logout = %d", r.status)
	}
	if r := app.do("GET", "/api/me", "", bearer...); r.status != http.StatusUnauthorized {
		t.Fatalf("/api/me after sign-out = %d", r.status)
	}
	web.json("GET", "/api/me", nil, nil, http.StatusOK)
}

func TestAppAuthorizeRefusals(t *testing.T) {
	e := newTestEnv(t, envOptions{devLogin: true})
	web := e.browser(t)
	web.devLogin("alice")
	sum := sha256.Sum256([]byte("v"))
	challenge := base64.RawURLEncoding.EncodeToString(sum[:])
	for name, req := range map[string]api.AppAuthorizeRequest{
		"remote redirect": {RedirectURI: "http://evil.example:80/", CodeChallenge: challenge, CodeChallengeMethod: "S256"},
		"localhost":       {RedirectURI: "http://localhost:80/", CodeChallenge: challenge, CodeChallengeMethod: "S256"},
		"plain PKCE":      {RedirectURI: "http://127.0.0.1:80/", CodeChallenge: challenge, CodeChallengeMethod: "plain"},
		"no challenge":    {RedirectURI: "http://127.0.0.1:80/", CodeChallengeMethod: "S256"},
	} {
		t.Run(name, func(t *testing.T) {
			web.json("POST", "/api/app/authorize", req, nil, http.StatusBadRequest)
		})
	}
	// The website's cross-origin protection applies: another site cannot
	// approve an app with the visitor's cookie.
	raw := `{"redirectUri":"http://127.0.0.1:80/","state":"","codeChallenge":"` + challenge + `","codeChallengeMethod":"S256","deviceName":""}`
	if r := web.do("POST", "/api/app/authorize", raw, "Origin", "https://evil.example", "Sec-Fetch-Site", "cross-site"); r.status != http.StatusForbidden {
		t.Fatalf("cross-site approval = %d", r.status)
	}
}
