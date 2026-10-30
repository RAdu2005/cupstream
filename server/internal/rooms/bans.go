package rooms

import (
	"context"

	"github.com/RAdu2005/cupstream/server/internal/access"
	"github.com/RAdu2005/cupstream/server/internal/store"
)

// Kicks and bans (overview, 4.2). Kicking ends a viewer's temporary access
// (their pass); the caller also ends their sessions. Banning does the same,
// takes them off the allowed list, and keeps them out, invite links
// included, until they are unbanned.

// Kick ends userID's pass to room.
func (s *Service) Kick(ctx context.Context, room store.Room, userID string) error {
	if room.Kind != string(access.Link) {
		return ErrNotLinkRoom
	}
	return s.db.DeletePass(ctx, store.DeletePassParams{RoomID: room.ID, UserID: userID})
}

// Ban bans userID from room.
func (s *Service) Ban(ctx context.Context, room store.Room, userID string) error {
	if room.Kind != string(access.Link) {
		return ErrNotLinkRoom
	}
	return s.db.Tx(ctx, func(q *store.Queries) error {
		if err := q.InsertBan(ctx, store.InsertBanParams{RoomID: room.ID, UserID: userID, CreatedAt: s.now().Unix()}); err != nil {
			return err
		}
		if err := q.DeletePass(ctx, store.DeletePassParams{RoomID: room.ID, UserID: userID}); err != nil {
			return err
		}
		_, err := q.DeleteAllowed(ctx, store.DeleteAllowedParams{RoomID: room.ID, UserID: userID})
		return err
	})
}

// Unban lifts a ban and reports whether there was one. The person gets no
// access back by it: they need a new invite or the allowed list.
func (s *Service) Unban(ctx context.Context, room store.Room, userID string) (bool, error) {
	n, err := s.db.DeleteBan(ctx, store.DeleteBanParams{RoomID: room.ID, UserID: userID})
	return n > 0, err
}
