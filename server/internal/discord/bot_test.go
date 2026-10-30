package discord

import (
	"context"
	"slices"
	"sort"
	"testing"
	"time"

	"github.com/bwmarrin/discordgo"

	"github.com/RAdu2005/cupstream/server/internal/access"
	"github.com/RAdu2005/cupstream/server/internal/auth"
	"github.com/RAdu2005/cupstream/server/internal/oauth"
	"github.com/RAdu2005/cupstream/server/internal/rooms"
	"github.com/RAdu2005/cupstream/server/internal/store"
)

// The test server: @everyone may view, "streamers" may also use Video and
// "mods" may move members and manage messages. Channel c2 denies Video to
// @everyone.
const (
	guildID   = "g"
	streamers = "streamers"
	mods      = "mods"
)

type timer struct {
	at time.Time
	f  func()
}

type fixture struct {
	t     *testing.T
	db    *store.DB
	rooms *rooms.Service
	bot   *Bot
	api   *fakeAPI
	clock time.Time
	start time.Time

	timers []timer
	// When each channel's room was checked again, since start.
	checked map[string][]time.Duration
}

type fakeAPI struct {
	voice   map[string]*discordgo.VoiceState // by user ID
	lookups int
}

func (a *fakeAPI) UserVoiceState(_ context.Context, _, userID string) (*discordgo.VoiceState, error) {
	a.lookups++
	return a.voice[userID], nil
}

func (a *fakeAPI) GuildMember(_ context.Context, _, userID string) (*discordgo.Member, error) {
	return &discordgo.Member{User: &discordgo.User{ID: userID}}, nil
}

func newFixture(t *testing.T) *fixture {
	t.Helper()
	db, err := store.Open(context.Background(), ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	start := time.Unix(1_800_000_000, 0)
	f := &fixture{t: t, db: db, rooms: rooms.New(db), clock: start, start: start, api: &fakeAPI{voice: map[string]*discordgo.VoiceState{}}, checked: map[string][]time.Duration{}}
	f.bot = New(db, f.rooms, "token")
	f.bot.now = func() time.Time { return f.clock }
	f.bot.after = func(d time.Duration, fn func()) { f.timers = append(f.timers, timer{f.clock.Add(d), fn}) }
	f.bot.api = f.api
	f.bot.Reevaluate = func(_ context.Context, room store.Room) {
		f.checked[room.ChannelID.String] = append(f.checked[room.ChannelID.String], f.clock.Sub(f.start))
	}
	f.rooms.SetVoice(f.bot)
	return f
}

// link signs in a CupStream user with this Discord ID.
func (f *fixture) link(discordID string) store.User {
	f.t.Helper()
	u, _, err := auth.NewAccounts(f.db, nil).SignIn(context.Background(), oauth.Identity{Provider: oauth.Discord, Subject: discordID, Username: discordID})
	if err != nil {
		f.t.Fatal(err)
	}
	return u
}

// room creates the Discord room of a channel.
func (f *fixture) room(channelID string) store.Room {
	f.t.Helper()
	r, err := f.rooms.DiscordRoom(context.Background(), guildID, channelID)
	if err != nil {
		f.t.Fatal(err)
	}
	return r
}

// advance moves the clock, firing the timers that fall due on the way.
func (f *fixture) advance(d time.Duration) {
	end := f.clock.Add(d)
	for {
		sort.SliceStable(f.timers, func(i, j int) bool { return f.timers[i].at.Before(f.timers[j].at) })
		if len(f.timers) == 0 || f.timers[0].at.After(end) {
			break
		}
		tm := f.timers[0]
		f.timers = f.timers[1:]
		f.clock = tm.at
		tm.f()
	}
	f.clock = end
}

func (f *fixture) member(userID, channelID string) rooms.VoiceMember {
	return f.bot.Member(context.Background(), guildID, channelID, userID)
}

func voice(userID, channelID string, roles ...string) *discordgo.VoiceState {
	return &discordgo.VoiceState{
		GuildID: guildID, UserID: userID, ChannelID: channelID,
		Member: &discordgo.Member{User: &discordgo.User{ID: userID}, Roles: roles},
	}
}

func voiceUpdate(userID, channelID string, roles ...string) *discordgo.VoiceStateUpdate {
	return &discordgo.VoiceStateUpdate{VoiceState: voice(userID, channelID, roles...)}
}

func ready() *discordgo.Ready {
	return &discordgo.Ready{User: &discordgo.User{Username: "CupStream"}, Guilds: []*discordgo.Guild{{ID: guildID, Unavailable: true}}}
}

func testGuild(states ...*discordgo.VoiceState) *discordgo.Guild {
	for _, vs := range states {
		vs.GuildID = "" // as in GUILD_CREATE
	}
	return &discordgo.Guild{
		ID: guildID, Name: "Test server", OwnerID: "owner",
		Roles: []*discordgo.Role{
			{ID: guildID, Permissions: permViewChannel},
			{ID: streamers, Permissions: permVideo},
			{ID: mods, Permissions: permMoveMembers | permManageMessages},
		},
		Channels: []*discordgo.Channel{
			{ID: "c1", GuildID: guildID, Name: "General", Type: discordgo.ChannelTypeGuildVoice},
			{ID: "c2", GuildID: guildID, Name: "Quiet", Type: discordgo.ChannelTypeGuildVoice, PermissionOverwrites: []*discordgo.PermissionOverwrite{
				{ID: guildID, Type: discordgo.PermissionOverwriteTypeRole, Deny: permVideo},
			}},
		},
		VoiceStates: states,
	}
}

func snapshot(states ...*discordgo.VoiceState) *discordgo.GuildCreate {
	return &discordgo.GuildCreate{Guild: testGuild(states...)}
}

var (
	gone      = rooms.VoiceMember{}
	viewer    = rooms.VoiceMember{InChannel: true, InChannelToStream: true}
	streamer  = rooms.VoiceMember{InChannel: true, InChannelToStream: true, CanVideo: true}
	leaving   = rooms.VoiceMember{InChannelToStream: true, CanVideo: true}
	moderator = rooms.VoiceMember{InChannel: true, InChannelToStream: true, CanMoveMembers: true, CanManageMessages: true}
)

func stale(m rooms.VoiceMember) rooms.VoiceMember { m.Stale = true; return m }

type step struct {
	name    string
	advance time.Duration
	event   any // applied after advancing; nil for none
	user    string
	channel string
	want    rooms.VoiceMember
}

func run(t *testing.T, f *fixture, steps []step) {
	t.Helper()
	for _, s := range steps {
		f.advance(s.advance)
		if s.event != nil {
			f.bot.handle(s.event)
		}
		if got := f.member(s.user, s.channel); got != s.want {
			t.Fatalf("%s (at %s): %s in %s = %+v, want %+v", s.name, f.clock.Sub(f.start), s.user, s.channel, got, s.want)
		}
	}
}

func TestVoiceTracking(t *testing.T) {
	f := newFixture(t)
	f.link("alice")
	f.link("mod")
	f.bot.handle(ready())

	run(t, f, []step{
		{name: "before the snapshot nothing is known", user: "alice", channel: "c1", want: stale(gone)},
		{name: "snapshot", event: snapshot(voice("alice", "c1", streamers), voice("bob", "c1"), voice("mod", "c2", mods)), user: "alice", channel: "c1", want: streamer},
		{name: "not in another channel", user: "alice", channel: "c2", want: gone},
		{name: "moderator", user: "mod", channel: "c2", want: moderator},
		{name: "the channel denies Video", event: voiceUpdate("alice", "c2", streamers), user: "alice", channel: "c2", want: viewer},
		{name: "moved: still within the viewers' delay in c1", user: "alice", channel: "c1", want: rooms.VoiceMember{InChannel: true, InChannelToStream: true, CanVideo: true}},
		{name: "viewers' delay over", advance: viewerDelay, user: "alice", channel: "c1", want: leaving},
		{name: "streamers' grace period over", advance: streamerGrace - viewerDelay, user: "alice", channel: "c1", want: rooms.VoiceMember{CanVideo: true}},
		{name: "back in c1", event: voiceUpdate("alice", "c1", streamers), user: "alice", channel: "c1", want: streamer},
		{name: "leaves voice", event: voiceUpdate("alice", "", streamers), user: "alice", channel: "c1", want: rooms.VoiceMember{InChannel: true, InChannelToStream: true, CanVideo: true}},
		{name: "a second later", advance: time.Second, user: "alice", channel: "c1", want: rooms.VoiceMember{InChannel: true, InChannelToStream: true, CanVideo: true}},
		{name: "rejoins within the grace period", advance: 5 * time.Second, event: voiceUpdate("alice", "c1", streamers), user: "alice", channel: "c1", want: streamer},
	})

	if _, ok := f.bot.guilds[guildID].members["bob"]; ok {
		t.Error("the bot keeps the voice state of someone who does not use CupStream")
	}
	f.bot.handle(voiceUpdate("bob", "c2"))
	if _, ok := f.bot.guilds[guildID].members["bob"]; ok {
		t.Error("the bot keeps a voice update of someone who does not use CupStream")
	}
}

func TestVideoPermissionGrace(t *testing.T) {
	f := newFixture(t)
	f.link("alice")
	f.bot.handle(ready())
	noVideo := &discordgo.GuildRoleUpdate{GuildRole: &discordgo.GuildRole{GuildID: guildID, Role: &discordgo.Role{ID: streamers}}}

	run(t, f, []step{
		{name: "snapshot", event: snapshot(voice("alice", "c1", streamers)), user: "alice", channel: "c1", want: streamer},
		{name: "role loses Video", advance: time.Second, event: noVideo, user: "alice", channel: "c1", want: streamer},
		{name: "still within the grace period", advance: streamerGrace - time.Second, user: "alice", channel: "c1", want: streamer},
		{name: "grace period over", advance: time.Second, user: "alice", channel: "c1", want: viewer},
		{name: "role removed by a voice update", event: voiceUpdate("alice", "c1"), user: "alice", channel: "c1", want: viewer},
	})
}

func TestStaleVoiceState(t *testing.T) {
	f := newFixture(t)
	f.link("alice")
	f.link("carol")
	f.bot.handle(ready())

	run(t, f, []step{
		{name: "snapshot", event: snapshot(voice("alice", "c1", streamers), voice("carol", "c1")), user: "alice", channel: "c1", want: streamer},
		{name: "leaves", event: voiceUpdate("alice", ""), user: "alice", channel: "c1", want: rooms.VoiceMember{InChannel: true, InChannelToStream: true}},
		{name: "the bot loses its connection", advance: time.Second, event: &discordgo.Disconnect{}, user: "alice", channel: "c1", want: stale(rooms.VoiceMember{InChannel: true, InChannelToStream: true})},
		{name: "grace periods stand still", advance: time.Minute, user: "alice", channel: "c1", want: stale(rooms.VoiceMember{InChannel: true, InChannelToStream: true})},
		{name: "others keep what the bot saw", user: "carol", channel: "c1", want: stale(viewer)},
		{name: "resumed: time runs again", event: &discordgo.Resumed{}, user: "alice", channel: "c1", want: gone},
		{name: "carol is still there", user: "carol", channel: "c1", want: viewer},
	})

	// A new session: carol left while the bot was away, and gets her grace
	// period from the new snapshot on.
	f.bot.handle(&discordgo.Disconnect{})
	f.advance(time.Minute)
	f.bot.handle(ready())
	run(t, f, []step{
		{name: "new session, before the snapshot", user: "carol", channel: "c1", want: stale(viewer)},
		{name: "snapshot without carol", event: snapshot(), user: "carol", channel: "c1", want: viewer},
		{name: "her delay runs from the snapshot", advance: viewerDelay, user: "carol", channel: "c1", want: rooms.VoiceMember{InChannelToStream: true}},
	})
}

func TestLookupAfterLinking(t *testing.T) {
	f := newFixture(t)
	f.bot.handle(ready())
	f.bot.handle(snapshot(voice("dave", "c1", streamers)))
	// Dave was in voice before linking Discord, so the snapshot skipped him.
	f.link("dave")
	f.api.voice["dave"] = voice("dave", "c1", streamers)

	run(t, f, []step{
		{name: "looked up", user: "dave", channel: "c1", want: streamer},
		{name: "known now", user: "dave", channel: "c1", want: streamer},
	})
	if f.api.lookups != 1 {
		t.Errorf("lookups = %d, want 1", f.api.lookups)
	}
	f.link("erin")
	run(t, f, []step{{name: "not in voice", user: "erin", channel: "c1", want: gone}})
	run(t, f, []step{{name: "not in voice, again", user: "erin", channel: "c1", want: gone}})
	if f.api.lookups != 2 {
		t.Errorf("lookups = %d, want 2", f.api.lookups)
	}
}

func TestBotRemoved(t *testing.T) {
	f := newFixture(t)
	f.link("alice")
	f.bot.handle(ready())
	run(t, f, []step{
		{name: "snapshot", event: snapshot(voice("alice", "c1", streamers)), user: "alice", channel: "c1", want: streamer},
		{name: "outage", event: &discordgo.GuildDelete{Guild: &discordgo.Guild{ID: guildID, Unavailable: true}}, user: "alice", channel: "c1", want: stale(streamer)},
		{name: "back", event: snapshot(voice("alice", "c1", streamers)), user: "alice", channel: "c1", want: streamer},
		{name: "removed", event: &discordgo.GuildDelete{Guild: &discordgo.Guild{ID: guildID}}, user: "alice", channel: "c1", want: gone},
	})
}

func TestRechecks(t *testing.T) {
	f := newFixture(t)
	f.link("alice")
	f.room("c1")
	f.room("c2")
	f.bot.handle(ready())
	f.bot.handle(snapshot(voice("alice", "c1", streamers)))
	f.advance(time.Minute)
	f.checked = map[string][]time.Duration{}
	f.start = f.clock

	f.bot.handle(voiceUpdate("alice", "c2", streamers))
	f.advance(time.Minute)
	ms := func(d ...time.Duration) []time.Duration { return d }
	if want := ms(viewerDelay+graceMargin, streamerGrace+graceMargin); !slices.Equal(f.checked["c1"], want) {
		t.Errorf("left room checked at %v, want %v", f.checked["c1"], want)
	}
	if want := ms(0); !slices.Equal(f.checked["c2"], want) {
		t.Errorf("joined room checked at %v, want %v", f.checked["c2"], want)
	}
}

func TestDiscordRoomAccess(t *testing.T) {
	f := newFixture(t)
	alice := f.link("alice")
	google, _, err := auth.NewAccounts(f.db, nil).SignIn(context.Background(), oauth.Identity{Provider: oauth.Google, Subject: "g", Username: "g"})
	if err != nil {
		t.Fatal(err)
	}
	room := f.room("c1")
	ctx := context.Background()
	check := func(userID string, a access.Action) access.Reason {
		t.Helper()
		r, err := f.rooms.Check(ctx, room, userID, a)
		if err != nil {
			t.Fatal(err)
		}
		return r
	}

	if r := check(alice.ID, access.Watch); r != access.DiscordReconnecting {
		t.Errorf("before connecting: %q", r)
	}
	if !f.rooms.Stale(room) {
		t.Error("room not stale before the bot connected")
	}
	f.bot.handle(ready())
	f.bot.handle(snapshot(voice("alice", "c1")))
	if r := check(alice.ID, access.Watch); r != access.OK {
		t.Errorf("watch: %q", r)
	}
	if r := check(alice.ID, access.Stream); r != access.NoVideoPerm {
		t.Errorf("stream without Video: %q", r)
	}
	if r := check(google.ID, access.Watch); r != access.DiscordRequired {
		t.Errorf("without Discord: %q", r)
	}
	if err := f.db.UpdateDiscordGuild(ctx, store.UpdateDiscordGuildParams{GuildID: guildID, StreamPolicy: string(access.AnyoneInChannel), Announce: true}); err != nil {
		t.Fatal(err)
	}
	f.bot.handle(snapshot(voice("alice", "c1"))) // reloads the settings
	if r := check(alice.ID, access.Stream); r != access.OK {
		t.Errorf("stream with anyone_in_channel: %q", r)
	}
}
