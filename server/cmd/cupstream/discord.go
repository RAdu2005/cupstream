package main

import (
	"context"
	"log/slog"
	"time"

	"github.com/RAdu2005/cupstream/server/internal/api"
	"github.com/RAdu2005/cupstream/server/internal/config"
	discordbot "github.com/RAdu2005/cupstream/server/internal/discord"
	"github.com/RAdu2005/cupstream/server/internal/gateway"
	"github.com/RAdu2005/cupstream/server/internal/httpapi"
	"github.com/RAdu2005/cupstream/server/internal/oauth"
	"github.com/RAdu2005/cupstream/server/internal/realtime"
	"github.com/RAdu2005/cupstream/server/internal/rooms"
	"github.com/RAdu2005/cupstream/server/internal/store"
)

// startDiscordBot runs the Discord bot when CUPSTREAM_DISCORD_BOT_TOKEN is
// set, and returns it for the names of servers and channels (nil without it).
func startDiscordBot(ctx context.Context, cfg *config.Config, db *store.DB, roomService *rooms.Service, gw *gateway.Gateway, hub *realtime.Hub) httpapi.DiscordNames {
	if cfg.DiscordBotToken == "" {
		return nil
	}
	bot := discordbot.New(db, roomService, cfg.DiscordBotToken)
	bot.Reevaluate = func(ctx context.Context, room store.Room) {
		gw.Reevaluate(ctx, room)
		hub.Reevaluate(ctx, room)
	}
	bot.Streams, bot.PublicURL = gw.RoomStreams, cfg.PublicURL
	bot.EndStreams = func(ctx context.Context, room store.Room, reason string) {
		gw.EndRoomStreams(ctx, room.ID, reason)
	}
	bot.StreamerGrace = func(ctx context.Context, discordID string, room store.Room, endsAt time.Time) {
		streamerGrace(ctx, db, gw, hub, discordID, room, endsAt)
	}
	roomService.SetVoice(bot)
	go func() {
		if err := bot.Run(ctx); err != nil {
			slog.Error("discord bot stopped", "err", err)
		}
	}()
	return bot
}

// streamerGrace tells a streamer who left (or rejoined) a voice channel when
// their stream in its room ends.
func streamerGrace(ctx context.Context, db *store.DB, gw *gateway.Gateway, hub *realtime.Hub, discordID string, room store.Room, endsAt time.Time) {
	u, err := db.GetUserByIdentity(ctx, store.GetUserByIdentityParams{Provider: oauth.Discord, Subject: discordID})
	if err != nil {
		return
	}
	ls, ok := gw.UserStream(u.ID)
	if !ok || ls.Stream.RoomID != room.ID {
		return
	}
	msg := api.StreamGrace{Type: "streamGrace", RoomSlug: room.Slug, StreamID: ls.Stream.ID}
	if !endsAt.IsZero() {
		msg.EndsInMs = max(1, time.Until(endsAt).Milliseconds())
	}
	hub.SendUser(u.ID, msg)
}
