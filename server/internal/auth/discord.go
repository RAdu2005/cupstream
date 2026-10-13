package auth

import (
	"context"
	"database/sql"
	"errors"
	"log/slog"
	"strings"
	"time"

	"golang.org/x/oauth2"

	"github.com/RAdu2005/cupstream/server/internal/oauth"
	"github.com/RAdu2005/cupstream/server/internal/seal"
	"github.com/RAdu2005/cupstream/server/internal/store"
)

const (
	sealDiscordToken = "discord-refresh-token"

	// Servers larger than this are off for friend suggestions by default.
	LargeGuildMembers = 1000

	guildSyncInterval = 24 * time.Hour
	activeWithin      = 30 * 24 * time.Hour
)

// DiscordSync keeps users' Discord refresh tokens (sealed) and server lists.
type DiscordSync struct {
	db       *store.DB
	sealer   *seal.Sealer
	provider *oauth.DiscordProvider
	now      func() time.Time
}

func NewDiscordSync(db *store.DB, sealer *seal.Sealer, provider *oauth.DiscordProvider) *DiscordSync {
	return &DiscordSync{db: db, sealer: sealer, provider: provider, now: time.Now}
}

// Store saves the token's refresh token and syncs the server list with its
// access token. A failed sync is logged, not returned: sign-in still works.
func (d *DiscordSync) Store(ctx context.Context, userID string, tok *oauth2.Token) error {
	if tok.RefreshToken != "" {
		if err := d.db.UpsertDiscordToken(ctx, store.UpsertDiscordTokenParams{
			UserID:                userID,
			EncryptedRefreshToken: d.sealer.Seal(sealDiscordToken+":"+userID, []byte(tok.RefreshToken)),
			UpdatedAt:             d.now().Unix(),
		}); err != nil {
			return err
		}
	}
	if err := d.syncGuilds(ctx, userID, tok.AccessToken); err != nil {
		slog.WarnContext(ctx, "discord server sync failed", "user", userID, "err", err)
	}
	return nil
}

// RefreshToken returns the user's stored refresh token, if any.
func (d *DiscordSync) RefreshToken(ctx context.Context, userID string) (string, error) {
	row, err := d.db.GetDiscordToken(ctx, userID)
	if errors.Is(err, sql.ErrNoRows) {
		return "", nil
	}
	if err != nil {
		return "", err
	}
	b, err := d.sealer.Open(sealDiscordToken+":"+userID, row.EncryptedRefreshToken)
	return string(b), err
}

func (d *DiscordSync) syncGuilds(ctx context.Context, userID, accessToken string) error {
	guilds, err := d.provider.Guilds(ctx, accessToken)
	if err != nil {
		return err
	}
	return d.db.Tx(ctx, func(q *store.Queries) error {
		old, err := q.ListUserGuilds(ctx, userID)
		if err != nil {
			return err
		}
		seen := map[string]bool{}
		for _, g := range guilds {
			seen[g.ID] = true
			if err := q.UpsertUserGuild(ctx, store.UpsertUserGuildParams{
				UserID:             userID,
				GuildID:            g.ID,
				Name:               g.Name,
				Icon:               g.IconURL(),
				ApproxMembers:      g.ApproximateMemberCount,
				SuggestionsEnabled: g.ApproximateMemberCount <= LargeGuildMembers,
			}); err != nil {
				return err
			}
		}
		for _, g := range old {
			if !seen[g.GuildID] {
				if err := q.DeleteUserGuild(ctx, store.DeleteUserGuildParams{UserID: userID, GuildID: g.GuildID}); err != nil {
					return err
				}
			}
		}
		return q.MarkGuildsSynced(ctx, store.MarkGuildsSyncedParams{GuildsSyncedAt: d.now().Unix(), UserID: userID})
	})
}

// Resync refreshes the user's token pair and server list.
func (d *DiscordSync) Resync(ctx context.Context, userID string) error {
	refresh, err := d.RefreshToken(ctx, userID)
	if err != nil || refresh == "" {
		return err
	}
	tok, err := d.provider.Refresh(ctx, refresh)
	if err != nil {
		if strings.Contains(err.Error(), "invalid_grant") {
			// The user revoked CupStream in Discord; stop trying.
			slog.InfoContext(ctx, "discord authorization revoked", "user", userID)
			return d.db.DeleteDiscordToken(ctx, userID)
		}
		return err
	}
	return d.Store(ctx, userID, tok)
}

// SyncLoop refreshes the server lists of active users once a day.
func (d *DiscordSync) SyncLoop(ctx context.Context) {
	t := time.NewTicker(time.Hour)
	defer t.Stop()
	for {
		now := d.now()
		due, err := d.db.ListGuildSyncDue(ctx, store.ListGuildSyncDueParams{
			SyncBefore:  now.Add(-guildSyncInterval).Unix(),
			ActiveSince: now.Add(-activeWithin).Unix(),
		})
		if err != nil && ctx.Err() == nil {
			slog.Warn("listing discord syncs failed", "err", err)
		}
		for _, userID := range due {
			if err := d.Resync(ctx, userID); err != nil {
				slog.Warn("discord resync failed", "user", userID, "err", err)
				// Try again tomorrow rather than every hour.
				_ = d.db.MarkGuildsSynced(ctx, store.MarkGuildsSyncedParams{GuildsSyncedAt: now.Unix(), UserID: userID})
			}
		}
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
	}
}
