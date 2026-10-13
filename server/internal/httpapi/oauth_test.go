package httpapi

import (
	"crypto/rand"
	"crypto/rsa"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"testing"
	"time"

	jose "github.com/go-jose/go-jose/v4"
	"github.com/go-jose/go-jose/v4/jwt"

	"github.com/RAdu2005/cupstream/server/internal/api"
)

// mockGoogle is an OpenID Connect provider. The authorization code is the
// subject of the user who signs in.
type mockGoogle struct {
	*httptest.Server
	URL string
	key *rsa.PrivateKey

	mu     sync.Mutex
	nonces map[string]string // code -> nonce
}

func newMockGoogle(t *testing.T) *mockGoogle {
	key, _ := rsa.GenerateKey(rand.Reader, 2048)
	m := &mockGoogle{key: key, nonces: map[string]string{}}
	mux := http.NewServeMux()
	mux.HandleFunc("/.well-known/openid-configuration", func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(map[string]any{
			"issuer":                                m.URL,
			"authorization_endpoint":                m.URL + "/auth",
			"token_endpoint":                        m.URL + "/token",
			"jwks_uri":                              m.URL + "/jwks",
			"id_token_signing_alg_values_supported": []string{"RS256"},
		})
	})
	mux.HandleFunc("/jwks", func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(jose.JSONWebKeySet{Keys: []jose.JSONWebKey{{Key: &key.PublicKey, KeyID: "k1", Algorithm: "RS256", Use: "sig"}}})
	})
	mux.HandleFunc("/token", func(w http.ResponseWriter, r *http.Request) {
		r.ParseForm()
		code := r.Form.Get("code")
		if r.Form.Get("code_verifier") == "" {
			http.Error(w, `{"error":"invalid_request"}`, http.StatusBadRequest)
			return
		}
		m.mu.Lock()
		nonce := m.nonces[code]
		m.mu.Unlock()
		signer, _ := jose.NewSigner(jose.SigningKey{Algorithm: jose.RS256, Key: key}, (&jose.SignerOptions{}).WithHeader("kid", "k1"))
		idt, _ := jwt.Signed(signer).Claims(map[string]any{
			"iss": m.URL, "aud": "google-client", "sub": code, "nonce": nonce,
			"exp": time.Now().Add(time.Hour).Unix(), "iat": time.Now().Unix(),
			"name": "Google " + code, "picture": "https://example.com/" + code + ".png",
		}).Serialize()
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]any{"access_token": "g-at", "token_type": "Bearer", "expires_in": 3600, "id_token": idt})
	})
	m.Server = httptest.NewServer(mux)
	m.URL = m.Server.URL
	t.Cleanup(m.Close)
	return m
}

// mockDiscord is Discord's OAuth and REST API. The authorization code is the
// user's Discord ID.
type mockDiscord struct {
	*httptest.Server
	URL string
}

func newMockDiscord(t *testing.T) *mockDiscord {
	m := &mockDiscord{}
	mux := http.NewServeMux()
	mux.HandleFunc("POST /oauth2/token", func(w http.ResponseWriter, r *http.Request) {
		r.ParseForm()
		sub := r.Form.Get("code")
		if r.Form.Get("grant_type") == "refresh_token" {
			sub = strings.TrimPrefix(r.Form.Get("refresh_token"), "rt-")
		} else if r.Form.Get("code_verifier") == "" {
			http.Error(w, `{"error":"invalid_request"}`, http.StatusBadRequest)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]any{"access_token": "at-" + sub, "refresh_token": "rt-" + sub, "token_type": "Bearer", "expires_in": 604800})
	})
	mux.HandleFunc("GET /users/@me", func(w http.ResponseWriter, r *http.Request) {
		sub := strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer at-")
		json.NewEncoder(w).Encode(map[string]any{"id": sub, "username": "d_" + sub, "global_name": "Discord " + sub, "avatar": "abc"})
	})
	mux.HandleFunc("GET /users/@me/guilds", func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode([]map[string]any{
			{"id": "100", "name": "Friends", "icon": "", "approximate_member_count": 12},
			{"id": "200", "name": "Huge Community", "icon": "ic", "approximate_member_count": 250000},
		})
	})
	m.Server = httptest.NewServer(mux)
	m.URL = m.Server.URL
	t.Cleanup(m.Close)
	return m
}

// oauthSignIn runs the redirect flow as a browser would, with code as the
// provider's authorization code, and returns the final redirect target.
func (b *browser) oauth(provider, mode, code string) string {
	b.t.Helper()
	r := b.do("GET", "/api/auth/"+provider+"/start?mode="+mode, "")
	if r.status != http.StatusFound {
		b.t.Fatalf("start = %d %s", r.status, r.body)
	}
	authURL, _ := url.Parse(r.header.Get("Location"))
	q := authURL.Query()
	if q.Get("code_challenge_method") != "S256" || q.Get("code_challenge") == "" {
		b.t.Fatalf("no PKCE in %s", authURL)
	}
	if provider == "google" {
		if q.Get("nonce") == "" {
			b.t.Fatal("no nonce")
		}
		b.e.google.mu.Lock()
		b.e.google.nonces[code] = q.Get("nonce")
		b.e.google.mu.Unlock()
	}
	cb := b.do("GET", "/api/auth/"+provider+"/callback?"+url.Values{"code": {code}, "state": {q.Get("state")}}.Encode(), "")
	if cb.status != http.StatusFound {
		b.t.Fatalf("callback = %d %s", cb.status, cb.body)
	}
	return cb.header.Get("Location")
}

func TestOAuthSignInLinkAndMerge(t *testing.T) {
	e := newTestEnv(t, envOptions{oauth: true})
	ana, mara := e.browser(t), e.browser(t)

	var providers api.AuthProviders
	ana.json("GET", "/api/auth/providers", nil, &providers, 200)
	if strings.Join(providers.Providers, ",") != "google,discord" {
		t.Fatalf("providers = %v", providers.Providers)
	}

	// Ana signs up with Discord; her server list is synced.
	if to := ana.oauth("discord", "login", "111"); to != "/" {
		t.Fatalf("redirect to %q", to)
	}
	var acc api.Account
	ana.json("GET", "/api/account", nil, &acc, 200)
	if acc.User.Handle != "d_111" || acc.User.DisplayName != "Discord 111" || !strings.Contains(acc.User.AvatarURL, "/avatars/111/abc") {
		t.Fatalf("account = %+v", acc.User)
	}
	var guilds []api.Guild
	ana.json("GET", "/api/account/guilds", nil, &guilds, 200)
	if len(guilds) != 2 || !guilds[0].SuggestionsEnabled || guilds[1].SuggestionsEnabled {
		t.Fatalf("guilds = %+v", guilds)
	}
	ana.json("PATCH", "/api/account/guilds/100", api.UpdateGuildRequest{SuggestionsEnabled: false}, nil, 200)

	// The refresh token is stored sealed, never in the clear.
	tok, err := e.db.GetDiscordToken(t.Context(), acc.User.ID)
	if err != nil || strings.Contains(string(tok.EncryptedRefreshToken), "rt-111") {
		t.Fatalf("stored token: %q, %v", tok.EncryptedRefreshToken, err)
	}

	// She links Google.
	if to := ana.oauth("google", "link", "g-ana"); to != "/settings?linked=google" {
		t.Fatalf("link redirect %q", to)
	}
	ana.json("GET", "/api/account", nil, &acc, 200)
	if len(acc.Identities) != 2 {
		t.Fatalf("identities = %+v", acc.Identities)
	}

	// Mara has a separate account through Google; Ana links it: merge.
	mara.oauth("google", "login", "g-mara")
	var maraMe api.Me
	mara.json("GET", "/api/me", nil, &maraMe, 200)
	ana.json("DELETE", "/api/account/identities/google", nil, nil, http.StatusNoContent)
	if to := ana.oauth("google", "link", "g-mara"); to != "/settings?merge=1" {
		t.Fatalf("link redirect %q", to)
	}
	var pending api.PendingMerge
	ana.json("GET", "/api/account/merge", nil, &pending, 200)
	if pending.Other.ID != maraMe.User.ID || pending.Provider != "google" {
		t.Fatalf("pending merge = %+v", pending)
	}
	mara.json("GET", "/api/account/merge", nil, nil, http.StatusNotFound) // not hers
	ana.json("POST", "/api/account/merge", nil, nil, http.StatusNoContent)
	mara.json("GET", "/api/me", nil, nil, http.StatusUnauthorized)

	// Signing in with Mara's Google identity now opens Ana's account.
	other := e.browser(t)
	other.oauth("google", "login", "g-mara")
	var me api.Me
	other.json("GET", "/api/me", nil, &me, 200)
	if me.User.ID != acc.User.ID {
		t.Fatal("merged identity does not sign in to the surviving account")
	}
}

func TestOAuthRefusals(t *testing.T) {
	e := newTestEnv(t, envOptions{oauth: true})
	b := e.browser(t)

	// A callback without the flow cookie (e.g. a forged link) is refused.
	if r := b.do("GET", "/api/auth/discord/callback?code=1&state=x", ""); r.header.Get("Location") != "/login?error=expired" {
		t.Fatalf("redirect %q", r.header.Get("Location"))
	}
	// A wrong state is refused.
	b.do("GET", "/api/auth/discord/start", "")
	if r := b.do("GET", "/api/auth/discord/callback?code=1&state=wrong", ""); r.header.Get("Location") != "/login?error=expired" {
		t.Fatalf("redirect %q", r.header.Get("Location"))
	}
	// The user cancelled at the provider.
	start := b.do("GET", "/api/auth/discord/start", "")
	u, _ := url.Parse(start.header.Get("Location"))
	if r := b.do("GET", "/api/auth/discord/callback?error=access_denied&state="+u.Query().Get("state"), ""); r.header.Get("Location") != "/login?error=cancelled" {
		t.Fatalf("redirect %q", r.header.Get("Location"))
	}
	// Linking requires being signed in.
	if r := b.do("GET", "/api/auth/google/start?mode=link", ""); r.header.Get("Location") != "/login" {
		t.Fatalf("redirect %q", r.header.Get("Location"))
	}
	// next must stay on this site.
	if got := safeNext("//evil.example/x", "/"); got != "/" {
		t.Fatalf("safeNext = %q", got)
	}
	// Unknown provider.
	if r := b.do("GET", "/api/auth/github/start", ""); r.status != http.StatusNotFound {
		t.Fatalf("got %d", r.status)
	}
}
