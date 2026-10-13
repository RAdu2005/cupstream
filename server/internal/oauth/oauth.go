// Package oauth implements sign-in with Google (OpenID Connect) and Discord
// (OAuth 2), both with PKCE.
package oauth

import (
	"context"
	"errors"

	"golang.org/x/oauth2"
)

const (
	Google  = "google"
	Discord = "discord"
)

// Identity is what a provider tells us about the person who signed in.
type Identity struct {
	Provider    string
	Subject     string // stable provider user ID
	Username    string // shown in settings, e.g. the Discord username
	DisplayName string
	AvatarURL   string

	// Discord only: the token pair, used to read the user's server list.
	// Google tokens are never kept.
	Token *oauth2.Token
}

// Provider runs one provider's authorization-code flow.
type Provider interface {
	Name() string
	// AuthURL is where to send the browser. verifier is the PKCE verifier and
	// nonce the OpenID Connect nonce (ignored by Discord).
	AuthURL(ctx context.Context, state, verifier, nonce string) (string, error)
	// Exchange turns the callback's code into an identity.
	Exchange(ctx context.Context, code, verifier, nonce string) (Identity, error)
}

var ErrNonce = errors.New("ID token nonce does not match")
