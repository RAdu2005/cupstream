package discord

import (
	"context"
	"slices"
	"strings"
	"testing"

	"github.com/bwmarrin/discordgo"

	"github.com/RAdu2005/cupstream/server/internal/access"
	"github.com/RAdu2005/cupstream/server/internal/gateway"
	"github.com/RAdu2005/cupstream/server/internal/store"
)

func TestStatusMessages(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	alice := f.link("alice")
	bob := f.link("bob")
	alice.DisplayName = "Alice *the* best"
	if _, err := f.db.UpdateProfile(ctx, store.UpdateProfileParams{Handle: alice.Handle, DisplayName: alice.DisplayName, ID: alice.ID}); err != nil {
		t.Fatal(err)
	}
	room := f.room("c1")
	f.live(room, alice)
	live := map[string][]gateway.LiveStream{room.ID: {{Stream: store.Stream{ID: "s1", UserID: alice.ID}, State: gateway.Live}}}
	f.bot.Streams = func(id string) []gateway.LiveStream { return live[id] }
	f.bot.handle(ready())
	f.bot.handle(snapshot())
	sync := func(want ...string) {
		t.Helper()
		f.api.calls = nil
		f.bot.syncStatus(ctx)
		if !slices.Equal(f.api.calls, want) {
			t.Fatalf("calls = %q, want %q", f.api.calls, want)
		}
	}

	sync("send c1")
	msg := f.api.messages["m1"]
	if d := msg.Embeds[0].Description; d != `🔴 **Alice \*the\* best** is live` || !strings.HasSuffix(msg.Embeds[0].URL, "/r/"+room.Slug) {
		t.Fatalf("status = %q %q", d, msg.Embeds[0].URL)
	}
	sync() // nothing changed: no edit

	live[room.ID] = append(live[room.ID], gateway.LiveStream{Stream: store.Stream{ID: "s2", UserID: bob.ID}, State: gateway.Live})
	sync("edit c1 m1")

	// After a restart the message is edited once, then left alone.
	f.bot.lastStatus = map[string]status{}
	sync("edit c1 m1")
	sync()

	// Someone deleted it in Discord: posted again.
	delete(f.api.messages, "m1")
	live[room.ID] = live[room.ID][:1]
	sync("send c1")

	// While the bot is disconnected, messages are left as they are.
	live[room.ID] = nil
	f.bot.handle(&discordgo.Disconnect{})
	sync()
	f.bot.handle(&discordgo.Resumed{})
	sync("delete c1 m2")

	// Status messages off.
	live[room.ID] = []gateway.LiveStream{{Stream: store.Stream{ID: "s1", UserID: alice.ID}, State: gateway.Live}}
	sync("send c1")
	if _, err := f.bot.updateSettings(ctx, guildID, func(s *store.DiscordGuild) bool { s.Announce = false; return true }); err != nil {
		t.Fatal(err)
	}
	sync("delete c1 m3")
}

func interaction(typ discordgo.InteractionType, perms int64, data discordgo.InteractionData) *discordgo.Interaction {
	return &discordgo.Interaction{Type: typ, GuildID: guildID, Member: &discordgo.Member{User: &discordgo.User{ID: "admin"}, Permissions: perms}, Data: data}
}

func TestSettingsCommand(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	f.bot.handle(ready())
	f.bot.handle(snapshot())
	respond := func(i *discordgo.Interaction) *discordgo.InteractionResponse {
		t.Helper()
		f.api.responses = nil
		f.bot.onInteraction(i)
		if len(f.api.responses) != 1 {
			t.Fatalf("%d responses", len(f.api.responses))
		}
		return f.api.responses[0]
	}
	settings := func() store.DiscordGuild {
		s, err := f.db.GetDiscordGuild(ctx, guildID)
		if err != nil {
			t.Fatal(err)
		}
		return s
	}

	r := respond(interaction(discordgo.InteractionApplicationCommand, permManageGuild, discordgo.ApplicationCommandInteractionData{Name: "cupstream"}))
	if r.Data.Flags != discordgo.MessageFlagsEphemeral || !strings.Contains(r.Data.Content, "Video permission") || len(r.Data.Components) != 3 {
		t.Fatalf("settings message = %+v", r.Data)
	}

	component := func(id string, values ...string) discordgo.MessageComponentInteractionData {
		return discordgo.MessageComponentInteractionData{CustomID: id, Values: values}
	}
	respond(interaction(discordgo.InteractionMessageComponent, permManageGuild, component(idPolicy, string(access.AnyoneInChannel))))
	if f.bot.Policy(guildID) != access.AnyoneInChannel || settings().StreamPolicy != string(access.AnyoneInChannel) {
		t.Fatal("policy not saved")
	}
	r = respond(interaction(discordgo.InteractionMessageComponent, permManageGuild, component(idChannels, "c1")))
	if r.Type != discordgo.InteractionResponseUpdateMessage || settings().TrackedChannels.String != `["c1"]` {
		t.Fatalf("tracked = %+v", settings().TrackedChannels)
	}
	respond(interaction(discordgo.InteractionMessageComponent, permManageGuild, component(idAllRooms)))
	if settings().TrackedChannels.Valid {
		t.Fatal("tracked channels not reset")
	}
	respond(interaction(discordgo.InteractionMessageComponent, permAdministrator, component(idAnnounce)))
	if settings().Announce {
		t.Fatal("status messages still on")
	}

	// Without Manage Server: refused, nothing changes.
	r = respond(interaction(discordgo.InteractionMessageComponent, permViewChannel, component(idAnnounce)))
	if !strings.Contains(r.Data.Content, "Manage Server") || settings().Announce {
		t.Fatalf("refusal = %+v", r.Data)
	}
}
