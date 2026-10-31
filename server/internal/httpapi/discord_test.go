package httpapi

import (
	"context"
	"net/http"
	"net/url"
	"strings"
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
	video   map[string]bool
}

func (b *fakeBot) Member(_ context.Context, _, _, userID string) rooms.VoiceMember {
	in := b.inVoice[userID]
	return rooms.VoiceMember{InChannel: in, InChannelToStream: in, CanVideo: in && b.video[userID]}
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

func TestMoveStreamToLinkRoom(t *testing.T) {
	bot := &fakeBot{inVoice: map[string]bool{"alice": true, "bob": true, "carol": true, "dave": true}, video: map[string]bool{"alice": true}}
	e := newTestEnv(t, envOptions{devLogin: true, bot: bot})
	e.gw.DetectFeatures(t.Context())
	alice, bob, carol, dave := e.browser(t), e.browser(t), e.browser(t), e.browser(t)
	users := map[string]api.User{}
	for name, b := range map[string]*browser{"alice": alice, "bob": bob, "carol": carol, "dave": dave} {
		users[name] = b.devLogin(name)
		e.linkDiscord(t, users[name], name, true)
	}

	// Alice goes live in her voice channel's room; the others watch.
	var started api.StartStreamResponse
	alice.json("POST", "/api/streams", api.StartStreamRequest{Source: "web"}, &started, http.StatusCreated)
	discordSlug, id := started.RoomSlug, started.Stream.ID
	if r := alice.do("POST", started.WhipURL, h264Offer, "Content-Type", "application/sdp", "Authorization", "Bearer "+started.Token); r.status != http.StatusCreated {
		t.Fatalf("WHIP = %d %s", r.status, r.body)
	}
	e.reconcile(t)
	sessions := map[string]string{} // user -> MediaMTX session ID
	for name, b := range map[string]*browser{"bob": bob, "carol": carol, "dave": dave} {
		r := b.whep(id)
		if r.status != http.StatusCreated {
			t.Fatalf("%s WHEP = %d %s", name, r.status, r.body)
		}
		loc, _ := url.Parse(r.header.Get("Location"))
		for _, s := range e.mtx.Sessions("s/" + id) {
			if strings.HasSuffix(loc.Path, s.Secret) {
				sessions[name] = s.ID
			}
		}
	}
	e.reconcile(t)

	var viewers []api.StreamViewer
	alice.json("GET", "/api/streams/"+id+"/viewers", nil, &viewers, http.StatusOK)
	if len(viewers) != 3 || viewers[0].Allowed {
		t.Fatalf("viewers = %+v", viewers)
	}
	bob.json("GET", "/api/streams/"+id+"/viewers", nil, nil, http.StatusForbidden)

	// She moves it to her link room: Bob until the stream ends, Dave for
	// good, Carol not.
	var moved api.MoveStreamResponse
	alice.json("POST", "/api/streams/"+id+"/move", api.MoveStreamRequest{Viewers: []api.MoveViewer{
		{UserID: users["bob"].ID}, {UserID: users["dave"].ID, Always: true},
	}}, &moved, http.StatusOK)
	link := alice.room(moved.RoomSlug)
	if link.Kind != "link" || len(link.Streams) != 1 || link.Streams[0].ID != id {
		t.Fatalf("link room = %+v", link)
	}
	if d := alice.room(discordSlug); len(d.Streams) != 0 {
		t.Fatalf("the Discord room still has %+v", d.Streams)
	}
	bob.room(moved.RoomSlug)
	dave.room(moved.RoomSlug)
	carol.json("GET", "/api/rooms/"+moved.RoomSlug, nil, nil, http.StatusForbidden)
	for name, sid := range sessions {
		s, ok := e.mtx.Session(sid)
		if !ok || s.VideoPaused != (name == "carol") {
			t.Errorf("%s's session after the move: %+v (open %v)", name, s, ok)
		}
	}
	alice.json("POST", "/api/streams/"+id+"/move", api.MoveStreamRequest{}, nil, http.StatusConflict)

	// Bob's pass ends with the stream; Dave stays allowed.
	alice.json("DELETE", "/api/streams/"+id, nil, nil, http.StatusNoContent)
	bob.json("GET", "/api/rooms/"+moved.RoomSlug, nil, nil, http.StatusForbidden)
	dave.room(moved.RoomSlug)
}
