package oauth

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"time"

	"golang.org/x/oauth2"
)

const (
	DiscordWeb = "https://discord.com"
	DiscordAPI = "https://discord.com/api/v10"
)

type DiscordProvider struct {
	api  string
	cfg  oauth2.Config
	http *http.Client
}

// NewDiscord returns the Discord provider. web and api are DiscordWeb and
// DiscordAPI except in tests.
func NewDiscord(web, api, clientID, clientSecret, redirectURL string) *DiscordProvider {
	return &DiscordProvider{
		api: api,
		cfg: oauth2.Config{
			ClientID:     clientID,
			ClientSecret: clientSecret,
			RedirectURL:  redirectURL,
			Scopes:       []string{"identify", "guilds"},
			Endpoint: oauth2.Endpoint{
				AuthURL:   web + "/oauth2/authorize",
				TokenURL:  api + "/oauth2/token",
				AuthStyle: oauth2.AuthStyleInParams,
			},
		},
		http: &http.Client{Timeout: 10 * time.Second},
	}
}

func (d *DiscordProvider) Name() string { return Discord }

func (d *DiscordProvider) AuthURL(_ context.Context, state, verifier, _ string) (string, error) {
	return d.cfg.AuthCodeURL(state, oauth2.S256ChallengeOption(verifier), oauth2.SetAuthURLParam("prompt", "none")), nil
}

type discordUser struct {
	ID         string `json:"id"`
	Username   string `json:"username"`
	GlobalName string `json:"global_name"`
	Avatar     string `json:"avatar"`
}

func (d *DiscordProvider) Exchange(ctx context.Context, code, verifier, _ string) (Identity, error) {
	tok, err := d.cfg.Exchange(ctx, code, oauth2.VerifierOption(verifier))
	if err != nil {
		return Identity{}, fmt.Errorf("discord token exchange: %w", err)
	}
	var u discordUser
	if err := d.get(ctx, tok.AccessToken, "/users/@me", &u); err != nil {
		return Identity{}, err
	}
	name := u.GlobalName
	if name == "" {
		name = u.Username
	}
	id := Identity{
		Provider:    Discord,
		Subject:     u.ID,
		Username:    u.Username,
		DisplayName: name,
		Token:       tok,
	}
	if u.Avatar != "" {
		id.AvatarURL = fmt.Sprintf("https://cdn.discordapp.com/avatars/%s/%s.png?size=128", u.ID, u.Avatar)
	}
	return id, nil
}

// Refresh exchanges a refresh token for a new token pair. Discord rotates
// refresh tokens, so the returned one must replace the stored one.
func (d *DiscordProvider) Refresh(ctx context.Context, refreshToken string) (*oauth2.Token, error) {
	tok, err := d.cfg.TokenSource(ctx, &oauth2.Token{RefreshToken: refreshToken}).Token()
	if err != nil {
		return nil, fmt.Errorf("discord token refresh: %w", err)
	}
	return tok, nil
}

// Guild is one of the user's Discord servers.
type Guild struct {
	ID                     string `json:"id"`
	Name                   string `json:"name"`
	Icon                   string `json:"icon"`
	ApproximateMemberCount int64  `json:"approximate_member_count"`
}

func (g Guild) IconURL() string {
	if g.Icon == "" {
		return ""
	}
	return fmt.Sprintf("https://cdn.discordapp.com/icons/%s/%s.png?size=64", g.ID, g.Icon)
}

// Guilds lists the user's servers (up to 200, Discord's page size).
func (d *DiscordProvider) Guilds(ctx context.Context, accessToken string) ([]Guild, error) {
	var gs []Guild
	err := d.get(ctx, accessToken, "/users/@me/guilds?"+url.Values{"with_counts": {"true"}}.Encode(), &gs)
	return gs, err
}

func (d *DiscordProvider) get(ctx context.Context, accessToken, path string, out any) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, d.api+path, nil)
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+accessToken)
	resp, err := d.http.Do(req)
	if err != nil {
		return fmt.Errorf("discord %s: %w", path, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("discord %s: %s", path, resp.Status)
	}
	return json.NewDecoder(resp.Body).Decode(out)
}
