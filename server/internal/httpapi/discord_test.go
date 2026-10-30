package httpapi

import (
	"context"
	"net/http"
	"testing"
	"time"

	"github.com/RAdu2005/cupstream/server/internal/access"
	"github.com/RAdu2005/cupstream/server/internal/api"
	"github.com/RAdu2005/cupstream/server/internal/oauth"
	"github.com/RAdu2005/cupstream/server/internal/rooms"
	"github.com/RAdu2005/cupstream/server/internal/store"
)

// fakeBot has one server, "g", with one voice channel, "c".
type fakeBot struct {
	inVoice map[string]bool // by Discord user ID
}

func (b *fakeBot) Member(_ context.Context, _, _, userID string) rooms.VoiceMember {
	return rooms.VoiceMember{InChannel: b.inVoice[userID], InChannelToStream: b.inVoice[userID]}
}
func (b *fakeBot) Policy(string) access.StreamPolicy { return access.VideoPermission }
func (b *fakeBot) Stale(string) bool                 { return false }
func (b *fakeBot) Channel(_ context.Context, userID string, _ []string) (string, string, bool) {
	return "g", "c", b.inVoice[userID]
}
func (b *fakeBot) Names(_, _ string) (string, string, string, bool) {
	return "Friends", "General", "", true
}

// linkDiscord gives a user a Discord identity, optionally as a member of "g".
func (e *testEnv) linkDiscord(t *testing.T, u api.User, discordID string, member bool) {
	t.Helper()
	ctx := context.Background()
	if err := e.db.CreateIdentity(ctx, store.CreateIdentityParams{Provider: oauth.Discord, Subject: discordID, UserID: u.ID, Username: discordID, CreatedAt: time.Now().Unix()}); err != nil {
		t.Fatal(err)
	}
	if member {
		if err := e.db.UpsertUserGuild(ctx, store.UpsertUserGuildParams{UserID: u.ID, GuildID: "g", Name: "Friends"}); err != nil {
			t.Fatal(err)
		}
	}
}

func TestDiscordRoomNames(t *testing.T) {
	bot := &fakeBot{inVoice: map[string]bool{"alice": true}}
	e := newTestEnv(t, envOptions{devLogin: true, bot: bot})
	alice, bob, carol := e.browser(t), e.browser(t), e.browser(t)
	e.linkDiscord(t, alice.devLogin("alice"), "alice", true)
	e.linkDiscord(t, bob.devLogin("bob"), "bob", false)
	e.linkDiscord(t, carol.devLogin("carol"), "carol", true)

	// The home page shows the room of the voice channel you are in.
	var home api.Home
	alice.json("GET", "/api/home", nil, &home, http.StatusOK)
	if home.Voice == nil || home.Voice.Kind != "discord" || home.Voice.Discord == nil || home.Voice.Discord.ChannelName != "General" {
		t.Fatalf("alice's voice room = %+v", home.Voice)
	}
	room := alice.room(home.Voice.Slug)
	if room.Kind != "discord" || room.Discord == nil || room.Discord.GuildName != "Friends" || room.Discord.URL != "https://discord.com/channels/g/c" {
		t.Fatalf("room = %+v", room)
	}
	var none api.Home
	bob.json("GET", "/api/home", nil, &none, http.StatusOK)
	if none.Voice != nil {
		t.Fatalf("bob is in no voice channel, got %+v", none.Voice)
	}

	// Not in the channel: refused, and told where to go; names only for
	// members of the server.
	if r := bob.do("GET", "/api/rooms/"+room.Slug, ""); r.status != http.StatusForbidden || decode[api.ErrorResponse](t, r.body).Code != string(access.JoinVoiceChannel) {
		t.Fatalf("bob enters: %d %s", r.status, r.body)
	}
	var ch api.DiscordChannel
	bob.json("GET", "/api/rooms/"+room.Slug+"/discord", nil, &ch, http.StatusOK)
	if ch.GuildName != "" || ch.ChannelName != "" || ch.URL == "" {
		t.Fatalf("a non-member sees %+v", ch)
	}
	ch = api.DiscordChannel{}
	carol.json("GET", "/api/rooms/"+room.Slug+"/discord", nil, &ch, http.StatusOK)
	if ch.GuildName != "Friends" || ch.ChannelName != "General" {
		t.Fatalf("a member sees %+v", ch)
	}
}
