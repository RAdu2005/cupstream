package auth

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"math/big"
	"regexp"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/google/uuid"

	"github.com/RAdu2005/cupstream/server/internal/friends"
	"github.com/RAdu2005/cupstream/server/internal/oauth"
	"github.com/RAdu2005/cupstream/server/internal/store"
)

const ProviderDev = "dev"

var (
	handlePattern     = regexp.MustCompile(`^[a-z0-9_]{2,32}$`)
	handleInvalid     = regexp.MustCompile(`[^a-z0-9_]+`)
	ErrInvalidHandle  = errors.New("a handle is 2–32 characters: lowercase letters, digits and underscores")
	ErrHandleTaken    = errors.New("this handle is taken")
	ErrInvalidName    = errors.New("a display name is 1–48 characters")
	ErrProviderLinked = errors.New("an account of this provider is already linked; unlink it first")
	ErrLastIdentity   = errors.New("this is the only way to sign in to your account")
	// ErrBanned refuses signing in as, linking or merging an account that an
	// admin banned.
	ErrBanned = errors.New("this account is banned")
)

// OwnedByOtherError is returned when linking an identity that already
// belongs to another account; the user can merge the accounts.
type OwnedByOtherError struct{ OtherUserID string }

func (e *OwnedByOtherError) Error() string {
	return "this identity belongs to another CupStream account"
}

// NormalizeHandle lowercases and validates a handle.
func NormalizeHandle(h string) (string, error) {
	h = strings.ToLower(strings.TrimSpace(h))
	if !handlePattern.MatchString(h) {
		return "", ErrInvalidHandle
	}
	return h, nil
}

// handleBase turns a provider name into a handle candidate.
func handleBase(name string) string {
	h := handleInvalid.ReplaceAllString(strings.ToLower(name), "_")
	h = strings.Trim(h, "_")
	if len(h) > 26 {
		h = strings.TrimRight(h[:26], "_")
	}
	if len(h) < 2 {
		h = "user"
	}
	return h
}

// Accounts manages users, their identities and their Discord data.
type Accounts struct {
	db      *store.DB
	discord *DiscordSync // nil when Discord sign-in is not configured
	now     func() time.Time
}

func NewAccounts(db *store.DB, discord *DiscordSync) *Accounts {
	return &Accounts{db: db, discord: discord, now: time.Now}
}

// SignIn returns the user that owns id, creating the account (with its
// settings and link room) on first sign-in.
func (a *Accounts) SignIn(ctx context.Context, id oauth.Identity) (u store.User, created bool, err error) {
	u, err = a.db.GetUserByIdentity(ctx, store.GetUserByIdentityParams{Provider: id.Provider, Subject: id.Subject})
	switch {
	case err == nil:
		if u.BannedAt.Valid {
			return u, false, ErrBanned
		}
		if err := a.refreshIdentity(ctx, u.ID, id); err != nil {
			return u, false, err
		}
		return u, false, nil
	case !errors.Is(err, sql.ErrNoRows):
		return u, false, err
	}

	err = a.db.Tx(ctx, func(q *store.Queries) error {
		u, err = a.createUser(ctx, q, id)
		return err
	})
	if err == nil {
		err = a.afterIdentity(ctx, u.ID, id)
	}
	return u, true, err
}

func (a *Accounts) createUser(ctx context.Context, q *store.Queries, id oauth.Identity) (store.User, error) {
	now := a.now().Unix()
	handle, err := freeHandle(ctx, q, handleBase(id.Username))
	if err != nil {
		return store.User{}, err
	}
	name := truncateName(id.DisplayName)
	if name == "" {
		name = handle
	}
	u, err := q.CreateUser(ctx, store.CreateUserParams{
		ID:          uuid.Must(uuid.NewV7()).String(),
		Handle:      handle,
		DisplayName: name,
		AvatarURL:   sql.NullString{String: id.AvatarURL, Valid: id.AvatarURL != ""},
		CreatedAt:   now,
	})
	if err != nil {
		return u, fmt.Errorf("create user: %w", err)
	}
	if err := q.CreateIdentity(ctx, store.CreateIdentityParams{
		Provider: id.Provider, Subject: id.Subject, UserID: u.ID, Username: id.Username, CreatedAt: now,
	}); err != nil {
		return u, err
	}
	if err := q.CreateUserSettings(ctx, u.ID); err != nil {
		return u, err
	}
	_, err = q.CreateLinkRoom(ctx, store.CreateLinkRoomParams{
		ID: uuid.Must(uuid.NewV7()).String(), Slug: randomHex(5), OwnerID: nullString(u.ID), CreatedAt: now,
	})
	return u, err
}

// freeHandle returns base, or base with a random number when it is taken.
func freeHandle(ctx context.Context, q *store.Queries, base string) (string, error) {
	h := base
	for range 20 {
		taken, err := q.HandleTaken(ctx, h)
		if err != nil {
			return "", err
		}
		if !taken {
			return h, nil
		}
		n, _ := rand.Int(rand.Reader, big.NewInt(10000))
		h = fmt.Sprintf("%s_%d", base, n.Int64())
	}
	return "", errors.New("could not find a free handle")
}

// refreshIdentity updates what we show from the provider on each sign-in.
func (a *Accounts) refreshIdentity(ctx context.Context, userID string, id oauth.Identity) error {
	if err := a.db.UpdateIdentityUsername(ctx, store.UpdateIdentityUsernameParams{
		Username: id.Username, Provider: id.Provider, Subject: id.Subject,
	}); err != nil {
		return err
	}
	return a.afterIdentity(ctx, userID, id)
}

// afterIdentity fills an empty avatar and stores Discord's token.
func (a *Accounts) afterIdentity(ctx context.Context, userID string, id oauth.Identity) error {
	if id.AvatarURL != "" {
		if err := a.db.SetAvatarIfEmpty(ctx, store.SetAvatarIfEmptyParams{
			AvatarURL: sql.NullString{String: id.AvatarURL, Valid: true}, ID: userID,
		}); err != nil {
			return err
		}
	}
	if id.Provider == oauth.Discord && id.Token != nil && a.discord != nil {
		return a.discord.Store(ctx, userID, id.Token)
	}
	return nil
}

// Link adds id to the user's account.
func (a *Accounts) Link(ctx context.Context, userID string, id oauth.Identity) error {
	existing, err := a.db.GetIdentity(ctx, store.GetIdentityParams{Provider: id.Provider, Subject: id.Subject})
	switch {
	case err == nil && existing.UserID == userID:
		return a.refreshIdentity(ctx, userID, id)
	case err == nil:
		owner, err := a.db.GetUser(ctx, existing.UserID)
		if err != nil {
			return err
		}
		if owner.BannedAt.Valid {
			return ErrBanned
		}
		return &OwnedByOtherError{OtherUserID: existing.UserID}
	case !errors.Is(err, sql.ErrNoRows):
		return err
	}

	err = a.db.CreateIdentity(ctx, store.CreateIdentityParams{
		Provider: id.Provider, Subject: id.Subject, UserID: userID, Username: id.Username, CreatedAt: a.now().Unix(),
	})
	if store.IsUniqueViolation(err) {
		return ErrProviderLinked
	}
	if err != nil {
		return err
	}
	return a.afterIdentity(ctx, userID, id)
}

// Unlink removes the user's identity of provider, keeping at least one.
func (a *Accounts) Unlink(ctx context.Context, userID, provider string) error {
	ids, err := a.db.ListIdentities(ctx, userID)
	if err != nil {
		return err
	}
	if len(ids) <= 1 {
		return ErrLastIdentity
	}
	return a.db.Tx(ctx, func(q *store.Queries) error {
		n, err := q.DeleteIdentity(ctx, store.DeleteIdentityParams{UserID: userID, Provider: provider})
		if err != nil || n == 0 {
			return err
		}
		if provider == oauth.Discord {
			if err := q.DeleteDiscordToken(ctx, userID); err != nil {
				return err
			}
			return q.DeleteUserGuilds(ctx, userID)
		}
		return nil
	})
}

// Merge moves otherID's identities, room memberships, friends, friend
// requests and blocks into userID and deletes otherID with everything it
// owns (its link room, stream keys and stream history). Identities of
// providers userID already has are dropped. The caller ends otherID's live
// streams first.
func (a *Accounts) Merge(ctx context.Context, userID, otherID string) error {
	if userID == otherID {
		return nil
	}
	return a.db.Tx(ctx, func(q *store.Queries) error {
		// Merging a banned account in would move its identities out of the ban.
		other, err := q.GetUser(ctx, otherID)
		if err != nil {
			return err
		}
		if other.BannedAt.Valid {
			return ErrBanned
		}
		mine, err := q.ListIdentities(ctx, userID)
		if err != nil {
			return err
		}
		have := map[string]bool{}
		for _, id := range mine {
			have[id.Provider] = true
		}
		theirs, err := q.ListIdentities(ctx, otherID)
		if err != nil {
			return err
		}
		for _, id := range theirs {
			if have[id.Provider] {
				continue
			}
			if err := q.MoveIdentity(ctx, store.MoveIdentityParams{ToUser: userID, Provider: id.Provider, Subject: id.Subject}); err != nil {
				return err
			}
			if id.Provider == oauth.Discord {
				if err := a.moveDiscordData(ctx, q, otherID, userID); err != nil {
					return err
				}
			}
		}
		if err := q.MoveAllowed(ctx, store.MoveAllowedParams{ToUser: userID, FromUser: otherID}); err != nil {
			return err
		}
		if err := friends.Merge(ctx, q, otherID, userID); err != nil {
			return err
		}
		return q.DeleteUser(ctx, otherID)
	})
}

func (a *Accounts) moveDiscordData(ctx context.Context, q *store.Queries, from, to string) error {
	tok, err := q.GetDiscordToken(ctx, from)
	if errors.Is(err, sql.ErrNoRows) {
		return nil
	}
	if err != nil {
		return err
	}
	if err := q.UpsertDiscordToken(ctx, store.UpsertDiscordTokenParams{
		UserID: to, EncryptedRefreshToken: tok.EncryptedRefreshToken, UpdatedAt: tok.UpdatedAt,
	}); err != nil {
		return err
	}
	guilds, err := q.ListUserGuilds(ctx, from)
	if err != nil {
		return err
	}
	for _, g := range guilds {
		if err := q.UpsertUserGuild(ctx, store.UpsertUserGuildParams{
			UserID: to, GuildID: g.GuildID, Name: g.Name, Icon: g.Icon, ApproxMembers: g.ApproxMembers, SuggestionsEnabled: g.SuggestionsEnabled,
		}); err != nil {
			return err
		}
	}
	return nil
}

// UpdateProfile changes the user's handle and display name.
func (a *Accounts) UpdateProfile(ctx context.Context, userID, handle, displayName string) (store.User, error) {
	h, err := NormalizeHandle(handle)
	if err != nil {
		return store.User{}, err
	}
	name := strings.TrimSpace(displayName)
	if name == "" || utf8.RuneCountInString(name) > 48 {
		return store.User{}, ErrInvalidName
	}
	u, err := a.db.UpdateProfile(ctx, store.UpdateProfileParams{Handle: h, DisplayName: name, ID: userID})
	if store.IsUniqueViolation(err) {
		return u, ErrHandleTaken
	}
	return u, err
}

func truncateName(s string) string {
	s = strings.TrimSpace(s)
	if utf8.RuneCountInString(s) > 48 {
		s = string([]rune(s)[:48])
	}
	return s
}

func randomHex(n int) string {
	b := make([]byte, n)
	rand.Read(b)
	return hex.EncodeToString(b)
}

func nullString(s string) sql.NullString { return sql.NullString{String: s, Valid: s != ""} }
