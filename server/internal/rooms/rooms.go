// Package rooms loads rooms and the facts that access checks need, and
// manages link-room settings.
package rooms

import (
	"context"
	"database/sql"
	"errors"
	"time"

	"github.com/RAdu2005/cupstream/server/internal/access"
	"github.com/RAdu2005/cupstream/server/internal/store"
)

var ErrNotLinkRoom = errors.New("only link rooms have this setting")

type Service struct {
	db  *store.DB
	now func() time.Time
}

func New(db *store.DB) *Service { return &Service{db: db, now: time.Now} }

// AccessRoom converts a stored room for the access package.
func AccessRoom(r store.Room) access.Room {
	if r.Kind == string(access.Discord) {
		// TODO(phase 6): the server's stream policy from discord_guilds.
		return access.Room{Kind: access.Discord, Policy: access.VideoPermission}
	}
	return access.Room{Kind: access.Link, OwnerID: r.OwnerID.String, Visibility: access.Visibility(r.Visibility.String)}
}

// Subject gathers what access checks need to know about userID ("" for
// anonymous visitors) in room.
func (s *Service) Subject(ctx context.Context, room store.Room, userID string) (access.Subject, error) {
	sub := access.Subject{UserID: userID}
	if userID == "" || room.Kind != string(access.Link) {
		// TODO(phase 6): Discord rooms read the bot's voice state.
		return sub, nil
	}
	banned, err := s.db.IsBanned(ctx, store.IsBannedParams{RoomID: room.ID, UserID: userID})
	if err != nil {
		return sub, err
	}
	sub.Banned = banned

	allowed, err := s.db.GetAllowed(ctx, store.GetAllowedParams{RoomID: room.ID, UserID: userID})
	switch {
	case err == nil:
		sub.Allowed, sub.AllowedToStream = true, allowed.CanStream
	case !errors.Is(err, sql.ErrNoRows):
		return sub, err
	}

	pass, err := s.db.GetActivePass(ctx, store.GetActivePassParams{RoomID: room.ID, UserID: userID, Now: s.now().Unix()})
	switch {
	case err == nil:
		sub.Pass, sub.PassToStream = true, pass.CanStream
	case !errors.Is(err, sql.ErrNoRows):
		return sub, err
	}
	return sub, nil
}

// Check reports whether userID may perform action in room.
func (s *Service) Check(ctx context.Context, room store.Room, userID string, action access.Action) (access.Reason, error) {
	sub, err := s.Subject(ctx, room, userID)
	if err != nil {
		return access.NotAllowed, err
	}
	return access.Check(AccessRoom(room), sub, action), nil
}

// Permissions is what userID may do in room.
type Permissions struct {
	Watch, Stream, Chat, ModerateStreams, ModerateChat access.Reason
}

func (s *Service) Permissions(ctx context.Context, room store.Room, userID string) (Permissions, error) {
	sub, err := s.Subject(ctx, room, userID)
	if err != nil {
		return Permissions{}, err
	}
	ar := AccessRoom(room)
	return Permissions{
		Watch:           access.Check(ar, sub, access.Watch),
		Stream:          access.Check(ar, sub, access.Stream),
		Chat:            access.Check(ar, sub, access.Chat),
		ModerateStreams: access.Check(ar, sub, access.ModerateStreams),
		ModerateChat:    access.Check(ar, sub, access.ModerateChat),
	}, nil
}

func (s *Service) SetVisibility(ctx context.Context, room store.Room, v access.Visibility) error {
	if room.Kind != string(access.Link) {
		return ErrNotLinkRoom
	}
	return s.db.SetRoomVisibility(ctx, store.SetRoomVisibilityParams{Visibility: sql.NullString{String: string(v), Valid: true}, ID: room.ID})
}

func (s *Service) SetAllowed(ctx context.Context, room store.Room, userID string, canStream bool) error {
	if room.Kind != string(access.Link) {
		return ErrNotLinkRoom
	}
	return s.db.UpsertAllowed(ctx, store.UpsertAllowedParams{RoomID: room.ID, UserID: userID, CanStream: canStream, CreatedAt: s.now().Unix()})
}

func (s *Service) RemoveAllowed(ctx context.Context, room store.Room, userID string) (bool, error) {
	n, err := s.db.DeleteAllowed(ctx, store.DeleteAllowedParams{RoomID: room.ID, UserID: userID})
	return n > 0, err
}
