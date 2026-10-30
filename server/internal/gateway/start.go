package gateway

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

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

const CodeDiscordUnavailable = "discord_unavailable"

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

	if cur := g.activeStreamOf(req.User.ID); cur != nil {
		switch {
		case req.Source == SourceOBS && Source(cur.Source) == SourceOBS && cur.RoomID == room.ID:
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

func (g *Gateway) destination(ctx context.Context, req StartRequest) (store.Room, error) {
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
		// TODO(phase 6): a Discord room when the user is in a voice channel on a
		// server with the bot and may stream there.
		why := "you are not in a Discord voice channel on a server with the CupStream bot"
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
