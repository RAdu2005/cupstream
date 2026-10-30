package rooms

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"errors"
	"time"

	"github.com/RAdu2005/cupstream/server/internal/access"
	"github.com/RAdu2005/cupstream/server/internal/auth"
	"github.com/RAdu2005/cupstream/server/internal/store"
)

// Invite links (overview, 4.2): anyone signed in who opens a valid link may
// watch, and stream if the link allows it, while the link is valid. They
// get a pass (room_passes), never an allowed-list entry. Revoking the link
// ends every pass it gave.

// Expiry is how long an invite link works.
type Expiry string

const (
	ExpiryHour      Expiry = "1h"
	ExpiryDay       Expiry = "24h"
	ExpiryStreamEnd Expiry = "stream" // until the room has no stream left
	ExpiryNever     Expiry = "never"
)

var (
	ErrInvalidExpiry = errors.New("expiry is 1h, 24h, stream or never")
	// ErrInviteInvalid means the link is unknown, revoked, expired or used up.
	ErrInviteInvalid = errors.New("this invite link is no longer valid")
	ErrNoInvite      = errors.New("no such invite link")
)

type NewInvite struct {
	Expiry    Expiry
	MaxUses   int64 // 0: no limit
	CanStream bool
}

// CreateInvite creates an invite link to a link room and returns it with
// its token, which is only stored hashed.
func (s *Service) CreateInvite(ctx context.Context, room store.Room, createdBy string, req NewInvite) (store.Invite, string, error) {
	if room.Kind != string(access.Link) {
		return store.Invite{}, "", ErrNotLinkRoom
	}
	now := s.now()
	p := store.CreateInviteParams{
		ID: newID(), RoomID: room.ID, CreatedBy: createdBy, CanStream: req.CanStream, CreatedAt: now.Unix(),
	}
	switch req.Expiry {
	case ExpiryHour:
		p.ExpiresAt = sql.NullInt64{Int64: now.Add(time.Hour).Unix(), Valid: true}
	case ExpiryDay:
		p.ExpiresAt = sql.NullInt64{Int64: now.Add(24 * time.Hour).Unix(), Valid: true}
	case ExpiryStreamEnd:
		p.UntilStreamEnd = true
	case ExpiryNever:
	default:
		return store.Invite{}, "", ErrInvalidExpiry
	}
	if req.MaxUses > 0 {
		p.MaxUses = sql.NullInt64{Int64: req.MaxUses, Valid: true}
	}
	token, hash := auth.NewToken()
	p.TokenHash = hash
	inv, err := s.db.CreateInvite(ctx, p)
	return inv, token, err
}

// ActiveInvites lists a room's links that are neither revoked nor expired,
// newest first; used-up ones are included, since their passes still work.
func (s *Service) ActiveInvites(ctx context.Context, room store.Room) ([]store.Invite, error) {
	return s.db.ListActiveInvites(ctx, store.ListActiveInvitesParams{RoomID: room.ID, Now: s.now().Unix()})
}

// RevokeInvite revokes a link and ends the passes it gave. Callers apply
// the access change to sessions and subscriptions.
func (s *Service) RevokeInvite(ctx context.Context, room store.Room, id string) error {
	return s.db.Tx(ctx, func(q *store.Queries) error {
		n, err := q.RevokeInvite(ctx, store.RevokeInviteParams{Now: sql.NullInt64{Int64: s.now().Unix(), Valid: true}, ID: id, RoomID: room.ID})
		if err != nil {
			return err
		}
		if n == 0 {
			return ErrNoInvite
		}
		return q.DeleteInvitePasses(ctx, sql.NullString{String: id, Valid: true})
	})
}

// EndStreamInvites revokes the room's links that last until the stream
// ends, once its last stream ended, and reports whether there were any.
func (s *Service) EndStreamInvites(ctx context.Context, roomID string) (bool, error) {
	var ended bool
	err := s.db.Tx(ctx, func(q *store.Queries) error {
		ids, err := q.RevokeStreamEndInvites(ctx, store.RevokeStreamEndInvitesParams{Now: sql.NullInt64{Int64: s.now().Unix(), Valid: true}, RoomID: roomID})
		if err != nil {
			return err
		}
		for _, id := range ids {
			if err := q.DeleteInvitePasses(ctx, sql.NullString{String: id, Valid: true}); err != nil {
				return err
			}
		}
		ended = len(ids) > 0
		return nil
	})
	return ended, err
}

// valid reports whether a link still works.
func (s *Service) valid(inv store.Invite) bool {
	now := s.now().Unix()
	return !inv.RevokedAt.Valid &&
		(!inv.ExpiresAt.Valid || inv.ExpiresAt.Int64 > now) &&
		(!inv.MaxUses.Valid || inv.Uses < inv.MaxUses.Int64)
}

// InviteByToken returns a valid link and its room.
func (s *Service) InviteByToken(ctx context.Context, token string) (store.Invite, store.Room, error) {
	inv, err := s.db.GetInviteByToken(ctx, auth.HashToken(token))
	if errors.Is(err, sql.ErrNoRows) {
		return inv, store.Room{}, ErrInviteInvalid
	}
	if err != nil {
		return inv, store.Room{}, err
	}
	if !s.valid(inv) {
		return inv, store.Room{}, ErrInviteInvalid
	}
	room, err := s.db.GetRoom(ctx, inv.RoomID)
	return inv, room, err
}

// RefusedError means the user may not use the link, e.g. they are banned.
type RefusedError struct{ Reason access.Reason }

func (e *RefusedError) Error() string { return "you cannot join this room" }

// Redeem gives userID a pass from the invite link with this token and
// returns the room. A use is only counted when the link gives them access
// they did not have: opening it again, or as the owner, changes nothing.
func (s *Service) Redeem(ctx context.Context, token, userID string) (store.Room, error) {
	inv, room, err := s.InviteByToken(ctx, token)
	if err != nil {
		return room, err
	}
	sub, err := s.Subject(ctx, room, userID)
	if err != nil {
		return room, err
	}
	if sub.Banned {
		return room, &RefusedError{access.Banned}
	}
	if userID == room.OwnerID.String || sub.Allowed && (sub.AllowedToStream || !inv.CanStream) {
		return room, nil
	}
	// In one transaction, so that opening the link twice at once counts once.
	err = s.db.Tx(ctx, func(q *store.Queries) error {
		pass, err := q.GetActivePass(ctx, store.GetActivePassParams{RoomID: room.ID, UserID: userID, Now: s.now().Unix()})
		switch {
		case err == nil:
			if pass.InviteID.String == inv.ID || passCovers(pass, inv) {
				return nil
			}
		case !errors.Is(err, sql.ErrNoRows):
			return err
		}
		n, err := q.UseInvite(ctx, store.UseInviteParams{ID: inv.ID, Now: s.now().Unix()})
		if err != nil {
			return err
		}
		if n == 0 {
			return ErrInviteInvalid // used up or revoked meanwhile
		}
		return q.UpsertPass(ctx, store.UpsertPassParams{
			RoomID: room.ID, UserID: userID, InviteID: sql.NullString{String: inv.ID, Valid: true},
			CanStream: inv.CanStream, ExpiresAt: inv.ExpiresAt, CreatedAt: s.now().Unix(),
		})
	})
	return room, err
}

// passCovers reports whether an existing pass gives at least what the
// invite would: a link never takes access away.
func passCovers(p store.RoomPass, inv store.Invite) bool {
	if inv.CanStream && !p.CanStream {
		return false
	}
	if !p.ExpiresAt.Valid {
		// Until revoked or until the stream ends; a link that never expires
		// replaces it.
		return inv.ExpiresAt.Valid || inv.UntilStreamEnd
	}
	return inv.ExpiresAt.Valid && !inv.UntilStreamEnd && p.ExpiresAt.Int64 >= inv.ExpiresAt.Int64
}

func newID() string {
	b := make([]byte, 16)
	rand.Read(b)
	return hex.EncodeToString(b)
}
