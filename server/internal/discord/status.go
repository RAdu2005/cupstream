package discord

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/bwmarrin/discordgo"

	"github.com/RAdu2005/cupstream/server/internal/gateway"
	"github.com/RAdu2005/cupstream/server/internal/store"
)

const (
	// Status messages are brought up to date this often, which also bounds
	// how often one is edited: Discord rate-limits edits.
	statusInterval = 5 * time.Second
	// After Discord refuses a status message (missing permissions), the
	// channel is left alone this long.
	statusBackoff = 10 * time.Minute
	statusColor   = 0xe5484d
)

// status is the content of a status message.
type status struct {
	url   string
	lines string
}

func (s status) message() *discordgo.MessageSend {
	return &discordgo.MessageSend{
		Embeds: []*discordgo.MessageEmbed{{
			Title:       "Live on CupStream",
			URL:         s.url,
			Description: s.lines,
			Color:       statusColor,
			Footer:      &discordgo.MessageEmbedFooter{Text: "Everyone in this voice channel who signs in with Discord can watch."},
		}},
		Components: []discordgo.MessageComponent{discordgo.ActionsRow{Components: []discordgo.MessageComponent{
			discordgo.Button{Label: "Watch", Style: discordgo.LinkButton, URL: s.url},
		}}},
	}
}

// statusOf describes a room's live streams, or ok false when it has none.
func (b *Bot) statusOf(ctx context.Context, room store.Room) (s status, ok bool) {
	var lines []string
	for _, ls := range b.Streams(room.ID) {
		if ls.State == gateway.Starting {
			continue
		}
		u, err := b.db.GetUser(ctx, ls.Stream.UserID)
		if err != nil {
			continue
		}
		lines = append(lines, "🔴 **"+escapeMarkdown(u.DisplayName)+"** is live")
	}
	if len(lines) == 0 {
		return s, false
	}
	return status{url: b.PublicURL.JoinPath("r", room.Slug).String(), lines: strings.Join(lines, "\n")}, true
}

var markdown = strings.NewReplacer(`\`, `\\`, `*`, `\*`, `_`, `\_`, "`", "\\`", `~`, `\~`, `|`, `\|`, `>`, `\>`, `#`, `\#`, `[`, `\[`, `]`, `\]`, `@`, "@​")

func escapeMarkdown(s string) string { return markdown.Replace(s) }

// syncStatus posts, edits and deletes status messages so that each tracked
// voice channel whose room is live has one listing its streams, in servers
// that want them.
func (b *Bot) syncStatus(ctx context.Context) {
	posted, err := b.db.ListStatusMessages(ctx)
	if err != nil {
		slog.Error("listing status messages failed", "err", err)
		return
	}
	want := map[string]status{} // by channel ID
	keep := map[string]bool{}   // channels whose server's view is stale: leave them be
	guildOf := map[string]string{}
	for _, room := range b.liveRooms() {
		guildID, channelID := room.GuildID.String, room.ChannelID.String
		b.mu.Lock()
		g := b.guilds[guildID]
		stale := g == nil && !b.readyOnce || g != nil && g.stale()
		announce := g != nil && b.settingsLocked(guildID).Announce && b.trackedLocked(g, channelID)
		b.mu.Unlock()
		if stale {
			keep[channelID] = true
			continue
		}
		if !announce {
			continue
		}
		if s, ok := b.statusOf(ctx, room); ok {
			want[channelID], guildOf[channelID] = s, guildID
		}
	}

	now := b.now()
	for _, p := range posted {
		if _, ok := want[p.ChannelID]; ok || keep[p.ChannelID] {
			continue
		}
		if err := b.api.DeleteMessage(ctx, p.ChannelID, p.MessageID); err != nil && !isNotFound(err) {
			slog.Warn("deleting a status message failed", "channel", p.ChannelID, "err", err)
		}
		if err := b.db.DeleteStatusMessage(ctx, p.ChannelID); err != nil {
			slog.Error("forgetting a status message failed", "err", err)
		}
		delete(b.lastStatus, p.ChannelID)
	}
	byChannel := map[string]store.DiscordStatusMessage{}
	for _, p := range posted {
		byChannel[p.ChannelID] = p
	}
	for channelID, s := range want {
		if b.lastStatus[channelID] == s || now.Before(b.statusRetry[channelID]) {
			continue
		}
		p, exists := byChannel[channelID]
		err := errors.New("not posted")
		if exists {
			err = b.api.EditMessage(ctx, channelID, p.MessageID, s.message())
		}
		if !exists || isNotFound(err) {
			var id string
			if id, err = b.api.SendMessage(ctx, channelID, s.message()); err == nil {
				err = b.db.UpsertStatusMessage(ctx, store.UpsertStatusMessageParams{ChannelID: channelID, GuildID: guildOf[channelID], MessageID: id, CreatedAt: now.Unix()})
			}
		}
		if err != nil {
			slog.Warn("posting a status message failed", "channel", channelID, "err", err)
			b.statusRetry[channelID] = now.Add(statusBackoff)
			continue
		}
		b.lastStatus[channelID] = s
	}
}

func isNotFound(err error) bool {
	var re *discordgo.RESTError
	return errors.As(err, &re) && re.Response != nil && re.Response.StatusCode == http.StatusNotFound
}

func (a sessionAPI) SendMessage(ctx context.Context, channelID string, m *discordgo.MessageSend) (string, error) {
	msg, err := a.s.ChannelMessageSendComplex(channelID, m, discordgo.WithContext(ctx))
	if err != nil {
		return "", err
	}
	return msg.ID, nil
}

func (a sessionAPI) EditMessage(ctx context.Context, channelID, messageID string, m *discordgo.MessageSend) error {
	_, err := a.s.ChannelMessageEditComplex(&discordgo.MessageEdit{
		ID: messageID, Channel: channelID, Embeds: &m.Embeds, Components: &m.Components,
	}, discordgo.WithContext(ctx))
	return err
}

func (a sessionAPI) DeleteMessage(ctx context.Context, channelID, messageID string) error {
	return a.s.ChannelMessageDelete(channelID, messageID, discordgo.WithContext(ctx))
}

func (a sessionAPI) RegisterCommands(ctx context.Context, appID string, cmds []*discordgo.ApplicationCommand) error {
	_, err := a.s.ApplicationCommandBulkOverwrite(appID, "", cmds, discordgo.WithContext(ctx))
	return err
}

func (a sessionAPI) Respond(ctx context.Context, i *discordgo.Interaction, r *discordgo.InteractionResponse) error {
	return a.s.InteractionRespond(i, r, discordgo.WithContext(ctx))
}
