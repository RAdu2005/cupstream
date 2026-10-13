package oauth

import (
	"context"
	"errors"
	"fmt"
	"sync"

	"github.com/coreos/go-oidc/v3/oidc"
	"golang.org/x/oauth2"
)

const GoogleIssuer = "https://accounts.google.com"

type GoogleProvider struct {
	issuer   string
	clientID string
	cfg      oauth2.Config

	mu       sync.Mutex
	provider *oidc.Provider // discovered lazily, so startup does not need Google
}

// NewGoogle returns the Google provider. issuer is GoogleIssuer except in tests.
func NewGoogle(issuer, clientID, clientSecret, redirectURL string) *GoogleProvider {
	return &GoogleProvider{
		issuer:   issuer,
		clientID: clientID,
		cfg: oauth2.Config{
			ClientID:     clientID,
			ClientSecret: clientSecret,
			RedirectURL:  redirectURL,
			Scopes:       []string{oidc.ScopeOpenID, "profile"},
		},
	}
}

func (g *GoogleProvider) Name() string { return Google }

func (g *GoogleProvider) discover(ctx context.Context) (*oidc.Provider, error) {
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.provider == nil {
		p, err := oidc.NewProvider(ctx, g.issuer)
		if err != nil {
			return nil, fmt.Errorf("google discovery: %w", err)
		}
		g.provider = p
		g.cfg.Endpoint = p.Endpoint()
	}
	return g.provider, nil
}

func (g *GoogleProvider) AuthURL(ctx context.Context, state, verifier, nonce string) (string, error) {
	if _, err := g.discover(ctx); err != nil {
		return "", err
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	return g.cfg.AuthCodeURL(state, oauth2.S256ChallengeOption(verifier), oidc.Nonce(nonce),
		oauth2.SetAuthURLParam("prompt", "select_account")), nil
}

type googleClaims struct {
	Subject string `json:"sub"`
	Name    string `json:"name"`
	Picture string `json:"picture"`
	Nonce   string `json:"nonce"`
}

func (g *GoogleProvider) Exchange(ctx context.Context, code, verifier, nonce string) (Identity, error) {
	p, err := g.discover(ctx)
	if err != nil {
		return Identity{}, err
	}
	g.mu.Lock()
	cfg := g.cfg
	g.mu.Unlock()

	tok, err := cfg.Exchange(ctx, code, oauth2.VerifierOption(verifier))
	if err != nil {
		return Identity{}, fmt.Errorf("google token exchange: %w", err)
	}
	raw, ok := tok.Extra("id_token").(string)
	if !ok {
		return Identity{}, errors.New("google returned no ID token")
	}
	idt, err := p.Verifier(&oidc.Config{ClientID: g.clientID}).Verify(ctx, raw)
	if err != nil {
		return Identity{}, fmt.Errorf("google ID token: %w", err)
	}
	var c googleClaims
	if err := idt.Claims(&c); err != nil {
		return Identity{}, err
	}
	if c.Nonce != nonce {
		return Identity{}, ErrNonce
	}
	name := c.Name
	if name == "" {
		name = "Google user"
	}
	return Identity{
		Provider:    Google,
		Subject:     c.Subject,
		Username:    name,
		DisplayName: name,
		AvatarURL:   c.Picture,
	}, nil
}
