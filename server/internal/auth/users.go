package auth

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/RAdu2005/cupstream/server/internal/store"
)

const ProviderDev = "dev"

var handlePattern = regexp.MustCompile(`^[a-z0-9_]{2,32}$`)

// ErrInvalidHandle is returned for handles that are not 2–32 characters of
// a–z, 0–9 and underscore.
var ErrInvalidHandle = errors.New("a handle is 2–32 characters: lowercase letters, digits and underscores")

// ErrHandleTaken is returned when a new user's handle belongs to another user.
var ErrHandleTaken = errors.New("this handle is taken")

// NormalizeHandle lowercases and validates a handle.
func NormalizeHandle(h string) (string, error) {
	h = strings.ToLower(strings.TrimSpace(h))
	if !handlePattern.MatchString(h) {
		return "", ErrInvalidHandle
	}
	return h, nil
}

// Identity is a sign-in identity from a provider.
type Identity struct {
	Provider    string
	Subject     string
	Username    string
	Handle      string // preferred handle; must already be normalized
	DisplayName string
	AvatarURL   string
}

// SignIn returns the user that owns id, creating the user on first sign-in.
func SignIn(ctx context.Context, db *store.DB, id Identity) (store.User, error) {
	u, err := db.GetUserByIdentity(ctx, store.GetUserByIdentityParams{Provider: id.Provider, Subject: id.Subject})
	if err == nil || !errors.Is(err, sql.ErrNoRows) {
		return u, err
	}
	// TODO(phase 1): pick a free handle when the preferred one is taken.
	err = db.Tx(ctx, func(q *store.Queries) error {
		now := time.Now().Unix()
		u, err = q.CreateUser(ctx, store.CreateUserParams{
			ID:          uuid.Must(uuid.NewV7()).String(),
			Handle:      id.Handle,
			DisplayName: id.DisplayName,
			AvatarURL:   sql.NullString{String: id.AvatarURL, Valid: id.AvatarURL != ""},
			CreatedAt:   now,
		})
		if store.IsUniqueViolation(err) {
			return ErrHandleTaken
		}
		if err != nil {
			return fmt.Errorf("create user: %w", err)
		}
		return q.CreateIdentity(ctx, store.CreateIdentityParams{
			Provider:  id.Provider,
			Subject:   id.Subject,
			UserID:    u.ID,
			Username:  id.Username,
			CreatedAt: now,
		})
	})
	return u, err
}
