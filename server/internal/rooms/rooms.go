// Package rooms loads rooms and the facts that access checks need, and
// manages link-room settings.
package rooms

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"errors"
	"time"

	"github.com/google/uuid"

	"github.com/RAdu2005/cupstream/server/internal/access"
	"github.com/RAdu2005/cupstream/server/internal/store"
)

var ErrNotLinkRoom = errors.New("only link rooms have this setting")

type Service struct {
	db    *store.DB
	now   func() time.Time
	voice Voice
}

func New(db *store.DB) *Service { return &Service{db: db, now: time.Now} }

// Voice is the Discord bot's view of voice channels (package discord).
type Voice interface {
	// Member reports what the bot knows about a Discord user in a voice
	// channel.
	Member(ctx context.Context, guildID, channelID, discordUserID string) VoiceMember
	// Policy is the server's rule for who may stream in its rooms.
	Policy(guildID string) access.StreamPolicy
	// Stale reports whether the bot's view of the server is out of date.
	Stale(guildID string) bool
	// Channel returns the voice channel a Discord user is in, if it has a
	// Discord room. guilds are the user's servers, to look in.
	Channel(ctx context.Context, discordUserID string, guilds []string) (guildID, channelID string, ok bool)
}

// VoiceMember is a Discord user as the bot sees them in one voice channel.
type VoiceMember struct {
	// InChannel: in the channel, or left it within the viewers' delay.
	InChannel bool
	// InChannelToStream: in the channel, or left it within the streamers'
	// grace period.
	InChannelToStream bool
	// Discord permissions in the channel. CanVideo includes the streamers'
	// grace period after losing it.
	CanVideo, CanMoveMembers, CanManageMessages bool
	// Stale: the bot is reconnecting, so this is what it saw last.
	Stale bool
}

// SetVoice connects the Discord bot. Without it, nobody is in voice.
func (s *Service) SetVoice(v Voice) { s.voice = v }

// AccessRoom converts a stored room for the access package.
func (s *Service) AccessRoom(r store.Room) access.Room {
	if r.Kind == string(access.Discord) {
		policy := access.VideoPermission
		if s.voice != nil {
			policy = s.voice.Policy(r.GuildID.String)
		}
		return access.Room{Kind: access.Discord, Policy: policy}
	}
	return access.Room{Kind: access.Link, OwnerID: r.OwnerID.String, Visibility: access.Visibility(r.Visibility.String)}
}

// Stale reports whether access to room cannot be told reliably right now:
// it is a Discord room and the bot is reconnecting. Nobody is paused,
// disconnected or ended because of access while it is.
func (s *Service) Stale(room store.Room) bool {
	return room.Kind == string(access.Discord) && s.voice != nil && s.voice.Stale(room.GuildID.String)
}

// DiscordRoom returns the room of a voice channel, creating it on first use.
func (s *Service) DiscordRoom(ctx context.Context, guildID, channelID string) (store.Room, error) {
	key := store.GetDiscordRoomParams{GuildID: nullString(guildID), ChannelID: nullString(channelID)}
	room, err := s.db.GetDiscordRoom(ctx, key)
	if !errors.Is(err, sql.ErrNoRows) {
		return room, err
	}
	b := make([]byte, 5)
	rand.Read(b)
	if err := s.db.CreateDiscordRoom(ctx, store.CreateDiscordRoomParams{
		ID: uuid.Must(uuid.NewV7()).String(), Slug: hex.EncodeToString(b),
		GuildID: key.GuildID, ChannelID: key.ChannelID, CreatedAt: s.now().Unix(),
	}); err != nil {
		return room, err
	}
	return s.db.GetDiscordRoom(ctx, key)
}

// CurrentDiscordRoom returns the room of the voice channel userID is in,
// creating it on first use, or ok false when they are in none that has a
// room (not in voice, or in a server without the bot).
func (s *Service) CurrentDiscordRoom(ctx context.Context, userID string) (room store.Room, ok bool, err error) {
	if s.voice == nil {
		return room, false, nil
	}
	ident, err := s.db.GetDiscordIdentity(ctx, userID)
	if errors.Is(err, sql.ErrNoRows) {
		return room, false, nil
	}
	if err != nil {
		return room, false, err
	}
	rows, err := s.db.ListUserGuilds(ctx, userID)
	if err != nil {
		return room, false, err
	}
	guilds := make([]string, len(rows))
	for i, g := range rows {
		guilds[i] = g.GuildID
	}
	guildID, channelID, ok := s.voice.Channel(ctx, ident.Subject, guilds)
	if !ok {
		return room, false, nil
	}
	room, err = s.DiscordRoom(ctx, guildID, channelID)
	return room, err == nil, err
}

func nullString(s string) sql.NullString { return sql.NullString{String: s, Valid: true} }

// discordSubject fills in a Discord room's facts from the bot's voice state.
func (s *Service) discordSubject(ctx context.Context, room store.Room, sub access.Subject) (access.Subject, error) {
	ident, err := s.db.GetDiscordIdentity(ctx, sub.UserID)
	if errors.Is(err, sql.ErrNoRows) {
		return sub, nil
	}
	if err != nil {
		return sub, err
	}
	sub.DiscordLinked = true
	if s.voice == nil {
		return sub, nil
	}
	m := s.voice.Member(ctx, room.GuildID.String, room.ChannelID.String, ident.Subject)
	sub.InVoiceChannel, sub.InVoiceToStream, sub.VoiceStale = m.InChannel, m.InChannelToStream, m.Stale
	sub.CanVideo, sub.CanMoveMembers, sub.CanManageMessages = m.CanVideo, m.CanMoveMembers, m.CanManageMessages
	return sub, nil
}

// Subject gathers what access checks need to know about userID ("" for
// anonymous visitors) in room.
func (s *Service) Subject(ctx context.Context, room store.Room, userID string) (access.Subject, error) {
	sub := access.Subject{UserID: userID}
	if userID != "" && room.Kind == string(access.Discord) {
		return s.discordSubject(ctx, room, sub)
	}
	if userID == "" || room.Kind != string(access.Link) {
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
	return access.Check(s.AccessRoom(room), sub, action), nil
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
	ar := s.AccessRoom(room)
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
