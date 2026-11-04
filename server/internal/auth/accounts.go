package auth

import (
	"context"
	"crypto/rand"
	"database/sql"
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
	// ErrNoAccount is a new user signing in, who chooses a handle first.
	ErrNoAccount = errors.New("no account for this identity")
)

const (
	// A user may change their handle this often.
	handleChangeInterval = 7 * 24 * time.Hour
	// The handle a user gave up stays theirs this long: nobody else may take
	// it, and they may switch back to it.
	handleReserved = 24 * time.Hour
)

// HandleCooldownError refuses a change of handle within a week of the last.
type HandleCooldownError struct{ Until time.Time }

func (e *HandleCooldownError) Error() string {
	return "you can change your handle once a week"
}

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

// SignIn returns the user that owns id, or ErrNoAccount for a new user, who
// signs up first (SignUp).
func (a *Accounts) SignIn(ctx context.Context, id oauth.Identity) (store.User, error) {
	u, err := a.db.GetUserByIdentity(ctx, store.GetUserByIdentityParams{Provider: id.Provider, Subject: id.Subject})
	switch {
	case errors.Is(err, sql.ErrNoRows):
		return u, ErrNoAccount
	case err != nil:
		return u, err
	case u.BannedAt.Valid:
		return u, ErrBanned
	}
	return u, a.refreshIdentity(ctx, u.ID, id)
}

// SignUp creates the account of id, with its settings and link room, under
// the handle and display name the user chose. If id already has an account
// (signing up twice), it signs in to it instead.
func (a *Accounts) SignUp(ctx context.Context, id oauth.Identity, handle, displayName string) (store.User, error) {
	h, err := NormalizeHandle(handle)
	if err != nil {
		return store.User{}, err
	}
	name, err := normalizeName(displayName)
	if err != nil {
		return store.User{}, err
	}
	var u store.User
	created := false
	err = a.db.Tx(ctx, func(q *store.Queries) error {
		_, err := q.GetUserByIdentity(ctx, store.GetUserByIdentityParams{Provider: id.Provider, Subject: id.Subject})
		if !errors.Is(err, sql.ErrNoRows) {
			return err
		}
		taken, err := q.HandleTaken(ctx, store.HandleTakenParams{Handle: h, Since: a.now().Add(-handleReserved).Unix()})
		if err != nil {
			return err
		}
		if taken {
			return ErrHandleTaken
		}
		u, err = a.createUser(ctx, q, id, h, name)
		created = err == nil
		return err
	})
	switch {
	case store.IsUniqueViolation(err):
		return u, ErrHandleTaken
	case err != nil:
		return u, err
	case !created:
		return a.SignIn(ctx, id)
	}
	return u, a.afterIdentity(ctx, u.ID, id)
}

func (a *Accounts) createUser(ctx context.Context, q *store.Queries, id oauth.Identity, handle, name string) (store.User, error) {
	now := a.now().Unix()
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
		ID: uuid.Must(uuid.NewV7()).String(), Slug: handle, OwnerID: nullString(u.ID), CreatedAt: now,
	})
	return u, err
}

// SuggestHandle proposes a free handle for a new user from their name at
// the provider: the name itself, or with a random number when it is taken.
func (a *Accounts) SuggestHandle(ctx context.Context, id oauth.Identity) (string, error) {
	base := handleBase(id.Username)
	h := base
	for range 20 {
		ok, err := a.HandleAvailable(ctx, "", h)
		if err != nil {
			return "", err
		}
		if ok {
			return h, nil
		}
		n, _ := rand.Int(rand.Reader, big.NewInt(10000))
		h = fmt.Sprintf("%s_%d", base, n.Int64())
	}
	return "", errors.New("could not find a free handle")
}

// HandleAvailable reports whether userID ("" for a new user) may take
// handle, which is normalized.
func (a *Accounts) HandleAvailable(ctx context.Context, userID, handle string) (bool, error) {
	taken, err := a.db.HandleTaken(ctx, store.HandleTakenParams{
		Handle: handle, UserID: userID, Since: a.now().Add(-handleReserved).Unix(),
	})
	return !taken, err
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

// HandleStatus is what the renaming rules allow a user now.
type HandleStatus struct {
	// NextChange is when the user may next change their handle; zero: now.
	NextChange time.Time
	// Previous is the handle they gave up last, which they may switch back to
	// until PreviousUntil; empty when they cannot.
	Previous      string
	PreviousUntil time.Time
}

// HandleStatus returns what the renaming rules allow userID now.
func (a *Accounts) HandleStatus(ctx context.Context, userID string) (HandleStatus, error) {
	var st HandleStatus
	last, err := a.db.LastHandleChange(ctx, userID)
	if errors.Is(err, sql.ErrNoRows) {
		return st, nil
	}
	if err != nil {
		return st, err
	}
	now := a.now()
	changed := time.Unix(last.ChangedAt, 0)
	if next := changed.Add(handleChangeInterval); now.Before(next) {
		st.NextChange = next
	}
	if until := changed.Add(handleReserved); now.Before(until) {
		st.Previous, st.PreviousUntil = last.OldHandle, until
	}
	return st, nil
}

// UpdateProfile changes the user's handle, and with it their link room's
// slug, and display name. A user may change their handle once a week; the
// handle they gave up stays reserved to them for a day, and switching back
// to it within that day undoes the change, which then does not count.
func (a *Accounts) UpdateProfile(ctx context.Context, userID, handle, displayName string) (store.User, error) {
	h, err := NormalizeHandle(handle)
	if err != nil {
		return store.User{}, err
	}
	name, err := normalizeName(displayName)
	if err != nil {
		return store.User{}, err
	}
	var u store.User
	err = a.db.Tx(ctx, func(q *store.Queries) error {
		cur, err := q.GetUser(ctx, userID)
		if err != nil {
			return err
		}
		if h != cur.Handle {
			if err := a.changeHandle(ctx, q, cur, h); err != nil {
				return err
			}
		}
		u, err = q.UpdateProfile(ctx, store.UpdateProfileParams{Handle: h, DisplayName: name, ID: userID})
		return err
	})
	if store.IsUniqueViolation(err) {
		return u, ErrHandleTaken
	}
	return u, err
}

// changeHandle applies the renaming rules to cur's change of handle to h,
// records it and moves their link room to h.
func (a *Accounts) changeHandle(ctx context.Context, q *store.Queries, cur store.User, h string) error {
	now := a.now()
	last, err := q.LastHandleChange(ctx, cur.ID)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return err
	}
	recent := err == nil
	changed := time.Unix(last.ChangedAt, 0)
	switch {
	case recent && last.OldHandle == h && last.NewHandle == cur.Handle && now.Before(changed.Add(handleReserved)):
		if err := q.UndoHandleChange(ctx, store.UndoHandleChangeParams{
			UndoneAt: sql.NullInt64{Int64: now.Unix(), Valid: true}, ID: last.ID,
		}); err != nil {
			return err
		}
	case recent && now.Before(changed.Add(handleChangeInterval)):
		return &HandleCooldownError{Until: changed.Add(handleChangeInterval)}
	default:
		taken, err := q.HandleTaken(ctx, store.HandleTakenParams{Handle: h, UserID: cur.ID, Since: now.Add(-handleReserved).Unix()})
		if err != nil {
			return err
		}
		if taken {
			return ErrHandleTaken
		}
		if err := q.CreateHandleChange(ctx, store.CreateHandleChangeParams{
			UserID: cur.ID, OldHandle: cur.Handle, NewHandle: h, ChangedAt: now.Unix(),
		}); err != nil {
			return err
		}
	}
	return q.SetLinkRoomSlug(ctx, store.SetLinkRoomSlugParams{Slug: h, OwnerID: nullString(cur.ID)})
}

func normalizeName(s string) (string, error) {
	name := strings.TrimSpace(s)
	if name == "" || utf8.RuneCountInString(name) > 48 {
		return "", ErrInvalidName
	}
	return name, nil
}

func nullString(s string) sql.NullString { return sql.NullString{String: s, Valid: s != ""} }
