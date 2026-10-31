package gateway

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/RAdu2005/cupstream/server/internal/access"
	"github.com/RAdu2005/cupstream/server/internal/auth"
	"github.com/RAdu2005/cupstream/server/internal/store"
)

// AlreadyLiveError means the user is live elsewhere; a user can only be live
// in one place. Web and app senders may retry with Replace after asking.
type AlreadyLiveError struct{ Current store.Stream }

func (e *AlreadyLiveError) Error() string {
	return fmt.Sprintf("you are already live (from %s); end that stream first", sourceName(Source(e.Current.Source)))
}

// DestinationError means there is nowhere this stream may go.
type DestinationError struct {
	Code    string // an access.Reason, or "discord_unavailable"
	Message string
}

func (e *DestinationError) Error() string { return e.Message }

const (
	CodeDiscordUnavailable = "discord_unavailable"
	CodeRoomFull           = "room_full"
	// OBS reconnected soon after its stream in a Discord room ended because
	// the streamer left voice (see obsBlock).
	CodeLeftVoice = "left_voice"
)

// obsBlock is how long after a stream in a Discord room ended because the
// streamer left voice OBS may not quietly reconnect into the link room.
const obsBlock = 2 * time.Minute

// discordDestination finds the Discord room for a stream with the start
// mode Discord (auto): the room of the voice channel the user is in, if
// they may stream there. Otherwise why says why not.
func (g *Gateway) discordDestination(ctx context.Context, userID string) (room store.Room, why string, err error) {
	room, ok, err := g.rooms.CurrentDiscordRoom(ctx, userID)
	if err != nil {
		return room, "", err
	}
	if !ok {
		return room, "you are not in a Discord voice channel on a server with the CupStream bot", nil
	}
	switch reason, err := g.rooms.Check(ctx, room, userID, access.Stream); {
	case err != nil:
		return room, "", err
	case reason == access.NoVideoPerm:
		return room, "you do not have the Video permission in your voice channel", nil
	case reason != access.OK:
		return room, "you cannot stream in your voice channel right now", nil
	}
	return room, "", nil
}

// blockOBS refuses userID's OBS reconnects to anywhere but a Discord room
// for a while, after their OBS stream ended because they left voice.
func (g *Gateway) blockOBS(userID string) {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.obsBlocks[userID] = g.now().Add(obsBlock)
}

func (g *Gateway) obsBlocked(userID string) bool {
	g.mu.Lock()
	defer g.mu.Unlock()
	until, ok := g.obsBlocks[userID]
	if ok && !g.now().Before(until) {
		delete(g.obsBlocks, userID)
		return false
	}
	return ok
}

type StartRequest struct {
	User   store.User
	Source Source
	// RoomSlug picks the room explicitly (e.g. a co-streamer joining someone
	// else's link room). Empty means: follow the user's start mode.
	RoomSlug string
	// LinkFallback accepts the user's link room when the start mode is
	// Discord (auto) and no Discord room is possible.
	LinkFallback bool
	// Replace ends the user's current stream instead of failing.
	Replace bool
}

type Started struct {
	Stream store.Stream
	Room   store.Room
	// Token authorizes publishing to this stream (app and web senders).
	Token string
	// Reconnect is set when an OBS sender resumes its own stream.
	Reconnect bool
}

// Start creates a stream, or returns the user's current OBS stream when OBS
// reconnects.
func (g *Gateway) Start(ctx context.Context, req StartRequest) (Started, error) {
	if req.User.BannedAt.Valid {
		return Started{}, &DestinationError{Code: CodeBanned, Message: "your account is banned"}
	}
	room, err := g.destination(ctx, req)
	if err != nil {
		return Started{}, err
	}
	reason, err := g.rooms.Check(ctx, room, req.User.ID, access.Stream)
	if err != nil {
		return Started{}, err
	}
	if reason != access.OK {
		return Started{}, &DestinationError{Code: string(reason), Message: "you may not stream in this room"}
	}

	cur := g.activeStreamOf(req.User.ID)
	isReconnect := cur != nil && req.Source == SourceOBS && Source(cur.Source) == SourceOBS && cur.RoomID == room.ID
	if !isReconnect && g.roomStreamsExcept(room.ID, cur) >= MaxRoomStreams {
		return Started{}, &DestinationError{Code: CodeRoomFull, Message: fmt.Sprintf("this room already has %d streams, the most it can have", MaxRoomStreams)}
	}
	if cur != nil {
		switch {
		case isReconnect:
			// OBS reconnecting its own dropped stream is not a second stream.
			return Started{Stream: *cur, Room: room, Reconnect: true}, nil
		case !req.Replace || req.Source == SourceOBS:
			// OBS cannot show a prompt, so it is refused rather than asked.
			return Started{}, &AlreadyLiveError{Current: *cur}
		default:
			g.EndStream(ctx, cur.ID, EndReplaced)
		}
	}

	var token string
	var tokenHash []byte
	if req.Source != SourceOBS {
		token, tokenHash = auth.NewToken()
	}
	rec, err := g.db.CreateStream(ctx, store.CreateStreamParams{
		ID:               newStreamID(),
		RoomID:           room.ID,
		UserID:           req.User.ID,
		Source:           string(req.Source),
		PublishTokenHash: tokenHash,
		StartedAt:        g.now().Unix(),
	})
	if err != nil {
		return Started{}, err
	}
	g.mu.Lock()
	g.streams[rec.ID] = &liveStream{rec: rec, state: Starting, since: g.now()}
	g.changed()
	g.mu.Unlock()
	return Started{Stream: rec, Room: room, Token: token}, nil
}

// roomStreamsExcept counts the active streams of a room, apart from skip
// (a stream that is about to be replaced).
func (g *Gateway) roomStreamsExcept(roomID string, skip *store.Stream) int {
	g.mu.Lock()
	defer g.mu.Unlock()
	n := 0
	for id, s := range g.streams {
		if s.rec.RoomID == roomID && (skip == nil || id != skip.ID) {
			n++
		}
	}
	return n
}

func (g *Gateway) destination(ctx context.Context, req StartRequest) (store.Room, error) {
	if cur := g.activeStreamOf(req.User.ID); req.Source == SourceOBS && req.RoomSlug == "" && cur != nil && cur.Source == string(SourceOBS) {
		// OBS reconnecting continues where its stream is, which may be a
		// link room it was moved to.
		return g.db.GetRoom(ctx, cur.RoomID)
	}
	if req.RoomSlug != "" {
		room, err := g.db.GetRoomBySlug(ctx, req.RoomSlug)
		if errors.Is(err, sql.ErrNoRows) {
			return room, &DestinationError{Code: string(access.NotAllowed), Message: "this room does not exist"}
		}
		return room, err
	}

	settings, err := g.db.UserSettings(ctx, req.User.ID)
	if err != nil {
		return store.Room{}, err
	}
	if settings.StartMode == "discord_auto" {
		room, why, err := g.discordDestination(ctx, req.User.ID)
		if err != nil || why == "" {
			return room, err
		}
		if req.Source == SourceOBS && g.obsBlocked(req.User.ID) {
			return store.Room{}, &DestinationError{Code: CodeLeftVoice, Message: "your stream ended because you left the voice channel; rejoin it to go on streaming there"}
		}
		switch {
		case req.Source == SourceOBS && settings.ObsFallback == "refuse":
			return store.Room{}, &DestinationError{Code: CodeDiscordUnavailable, Message: why + ", and your OBS setting refuses other rooms"}
		case req.Source != SourceOBS && !req.LinkFallback:
			return store.Room{}, &DestinationError{Code: CodeDiscordUnavailable, Message: why}
		}
	}
	return g.db.GetLinkRoomByOwner(ctx, sql.NullString{String: req.User.ID, Valid: true})
}

func sourceName(s Source) string {
	switch s {
	case SourceOBS:
		return "OBS"
	case SourceApp:
		return "the CupStream app"
	}
	return "a browser"
}
