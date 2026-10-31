package discord

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"log/slog"
	"time"

	"github.com/bwmarrin/discordgo"

	"github.com/RAdu2005/cupstream/server/internal/access"
	"github.com/RAdu2005/cupstream/server/internal/store"
)

// The /cupstream settings command, for members with Manage Server: who may
// stream, status messages on or off, and which voice channels get rooms.
// It answers with a message only the caller sees, whose menus and buttons
// change the settings.
const (
	commandName = "cupstream"
	idPolicy    = "cupstream:policy"
	idChannels  = "cupstream:channels"
	idAnnounce  = "cupstream:announce"
	idAllRooms  = "cupstream:all-channels"
	// The most channels a select menu takes.
	maxTracked = 25
)

func commands() []*discordgo.ApplicationCommand {
	manageGuild := permManageGuild
	return []*discordgo.ApplicationCommand{{
		Name:                     commandName,
		Description:              "CupStream",
		DefaultMemberPermissions: &manageGuild,
		Contexts:                 &[]discordgo.InteractionContextType{discordgo.InteractionContextGuild},
		Options: []*discordgo.ApplicationCommandOption{{
			Type:        discordgo.ApplicationCommandOptionSubCommand,
			Name:        "settings",
			Description: "Who may stream, status messages and the voice channels with rooms",
		}},
	}}
}

// registerCommands installs the slash command (globally, for every server).
func (b *Bot) registerCommands(appID string) {
	ctx, cancel := context.WithTimeout(b.ctx, 10*time.Second)
	defer cancel()
	if err := b.api.RegisterCommands(ctx, appID, commands()); err != nil {
		slog.Error("registering the discord slash command failed", "err", err)
	}
}

func (b *Bot) onInteraction(i *discordgo.Interaction) {
	ctx, cancel := context.WithTimeout(b.ctx, 10*time.Second)
	defer cancel()
	if i.GuildID == "" || i.Member == nil {
		return
	}
	respond := func(typ discordgo.InteractionResponseType, data *discordgo.InteractionResponseData) {
		if err := b.api.Respond(ctx, i, &discordgo.InteractionResponse{Type: typ, Data: data}); err != nil {
			slog.Warn("answering a discord interaction failed", "err", err)
		}
	}
	if i.Member.Permissions&(permManageGuild|permAdministrator) == 0 {
		respond(discordgo.InteractionResponseChannelMessageWithSource, &discordgo.InteractionResponseData{
			Content: "Only members with Manage Server can change CupStream's settings.", Flags: discordgo.MessageFlagsEphemeral,
		})
		return
	}

	switch i.Type {
	case discordgo.InteractionApplicationCommand:
		if i.ApplicationCommandData().Name != commandName {
			return
		}
		s, err := b.guildSettings(ctx, i.GuildID)
		if err != nil {
			slog.Error("loading discord settings failed", "err", err)
			return
		}
		respond(discordgo.InteractionResponseChannelMessageWithSource, settingsMessage(s))
	case discordgo.InteractionMessageComponent:
		d := i.MessageComponentData()
		s, err := b.updateSettings(ctx, i.GuildID, func(s *store.DiscordGuild) bool {
			switch d.CustomID {
			case idPolicy:
				if len(d.Values) != 1 || (d.Values[0] != string(access.VideoPermission) && d.Values[0] != string(access.AnyoneInChannel)) {
					return false
				}
				s.StreamPolicy = d.Values[0]
			case idAnnounce:
				s.Announce = !s.Announce
			case idChannels:
				s.TrackedChannels = trackedJSON(d.Values)
			case idAllRooms:
				s.TrackedChannels = sql.NullString{}
			default:
				return false
			}
			return true
		})
		if err != nil {
			slog.Error("saving discord settings failed", "err", err)
			return
		}
		respond(discordgo.InteractionResponseUpdateMessage, settingsMessage(s))
	}
}

func trackedJSON(ids []string) sql.NullString {
	if len(ids) == 0 {
		return sql.NullString{} // every voice channel
	}
	b, _ := json.Marshal(ids)
	return sql.NullString{String: string(b), Valid: true}
}

func (b *Bot) guildSettings(ctx context.Context, guildID string) (store.DiscordGuild, error) {
	if err := b.db.EnsureDiscordGuild(ctx, store.EnsureDiscordGuildParams{GuildID: guildID, CreatedAt: b.now().Unix()}); err != nil {
		return store.DiscordGuild{}, err
	}
	return b.db.GetDiscordGuild(ctx, guildID)
}

// updateSettings changes a server's settings with change, which reports
// whether it changed anything, and applies them: a new stream policy
// changes who may stream in its rooms.
func (b *Bot) updateSettings(ctx context.Context, guildID string, change func(*store.DiscordGuild) bool) (store.DiscordGuild, error) {
	s, err := b.guildSettings(ctx, guildID)
	if err != nil || !change(&s) {
		return s, err
	}
	if err := b.db.UpdateDiscordGuild(ctx, store.UpdateDiscordGuildParams{
		StreamPolicy: s.StreamPolicy, Announce: s.Announce, TrackedChannels: s.TrackedChannels, GuildID: guildID,
	}); err != nil {
		return s, err
	}
	b.mu.Lock()
	b.settings[guildID] = s
	b.mu.Unlock()
	b.schedule(recheck{guildID: guildID, delays: afterPermChange})
	return s, nil
}

func settingsMessage(s store.DiscordGuild) *discordgo.InteractionResponseData {
	st := settings{s}
	policy := st.policy()
	var tracked []string
	if s.TrackedChannels.Valid {
		_ = json.Unmarshal([]byte(s.TrackedChannels.String), &tracked)
	}
	rooms := "every voice channel"
	if s.TrackedChannels.Valid {
		rooms = fmt.Sprintf("%d selected voice channels", len(tracked))
	}
	streamers := "members with the Video permission in the channel"
	if policy == access.AnyoneInChannel {
		streamers = "anyone in the voice channel"
	}
	announce, toggle := "on", "Turn status messages off"
	if !s.Announce {
		announce, toggle = "off", "Turn status messages on"
	}
	defaults := make([]discordgo.SelectMenuDefaultValue, 0, len(tracked))
	for _, id := range tracked {
		defaults = append(defaults, discordgo.SelectMenuDefaultValue{ID: id, Type: discordgo.SelectMenuDefaultValueChannel})
	}
	zero := 0
	return &discordgo.InteractionResponseData{
		Flags: discordgo.MessageFlagsEphemeral,
		Content: fmt.Sprintf("**CupStream settings**\nWho may stream: %s\nStatus messages: %s\nRooms: %s",
			streamers, announce, rooms),
		Components: []discordgo.MessageComponent{
			discordgo.ActionsRow{Components: []discordgo.MessageComponent{discordgo.SelectMenu{
				CustomID: idPolicy, Placeholder: "Who may stream",
				Options: []discordgo.SelectMenuOption{
					{Label: "Members with the Video permission", Value: string(access.VideoPermission), Default: policy == access.VideoPermission},
					{Label: "Anyone in the voice channel", Value: string(access.AnyoneInChannel), Default: policy == access.AnyoneInChannel},
				},
			}}},
			discordgo.ActionsRow{Components: []discordgo.MessageComponent{discordgo.SelectMenu{
				MenuType: discordgo.ChannelSelectMenu, CustomID: idChannels,
				Placeholder:  "Voice channels with rooms (none selected: all)",
				ChannelTypes: []discordgo.ChannelType{discordgo.ChannelTypeGuildVoice, discordgo.ChannelTypeGuildStageVoice},
				MinValues:    &zero, MaxValues: maxTracked, DefaultValues: defaults,
			}}},
			discordgo.ActionsRow{Components: []discordgo.MessageComponent{
				discordgo.Button{CustomID: idAnnounce, Label: toggle, Style: discordgo.SecondaryButton},
				discordgo.Button{CustomID: idAllRooms, Label: "Rooms in every voice channel", Style: discordgo.SecondaryButton, Disabled: !s.TrackedChannels.Valid},
			}},
		},
	}
}
