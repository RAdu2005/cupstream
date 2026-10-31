package httpapi

import (
	"context"
	"net/http"

	"github.com/RAdu2005/cupstream/server/internal/access"
	"github.com/RAdu2005/cupstream/server/internal/api"
	"github.com/RAdu2005/cupstream/server/internal/auth"
	"github.com/RAdu2005/cupstream/server/internal/store"
)

// DiscordNames gives the names of the Discord servers and channels the bot
// is in (package discord).
type DiscordNames interface {
	Names(guildID, channelID string) (guildName, channelName, iconURL string, ok bool)
}

// discordChannel describes a Discord room's voice channel to userID. Names
// are only for members of the server: its members per Discord sign-in, or
// people the bot sees in the channel.
func (s *Server) discordChannel(ctx context.Context, room store.Room, userID string, inChannel bool) (*api.DiscordChannel, error) {
	guildID, channelID := room.GuildID.String, room.ChannelID.String
	out := &api.DiscordChannel{URL: "https://discord.com/channels/" + guildID + "/" + channelID}
	if s.Bot == nil || userID == "" {
		return out, nil
	}
	member := inChannel
	if !member {
		var err error
		member, err = s.DB.IsGuildMember(ctx, store.IsGuildMemberParams{UserID: userID, GuildID: guildID})
		if err != nil {
			return nil, err
		}
	}
	if member {
		if g, c, icon, ok := s.Bot.Names(guildID, channelID); ok {
			out.GuildName, out.ChannelName, out.GuildIconURL = g, c, icon
		}
	}
	return out, nil
}

// roomDiscord serves GET /api/rooms/{slug}/discord: where to go to enter a
// Discord room, for someone it refused.
func (s *Server) roomDiscord(w http.ResponseWriter, r *http.Request, cur auth.Current) {
	room, ok := s.loadRoom(w, r)
	if !ok {
		return
	}
	if room.Kind != string(access.Discord) {
		writeError(w, http.StatusNotFound, "this is not a Discord room")
		return
	}
	out, err := s.discordChannel(r.Context(), room, cur.User.ID, false)
	if err != nil {
		internalError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, out)
}
