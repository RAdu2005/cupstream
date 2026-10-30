package gateway

import (
	"context"
	"net/http"
	"strings"
	"testing"

	"github.com/RAdu2005/cupstream/server/internal/access"
	"github.com/RAdu2005/cupstream/server/internal/auth"
	"github.com/RAdu2005/cupstream/server/internal/oauth"
	"github.com/RAdu2005/cupstream/server/internal/rooms"
	"github.com/RAdu2005/cupstream/server/internal/store"
)

// fakeVoice is the Discord bot's view of one voice channel.
type fakeVoice struct {
	members map[string]rooms.VoiceMember // by Discord user ID
	stale   bool
}

func (v *fakeVoice) Member(_ context.Context, _, _, userID string) rooms.VoiceMember {
	m := v.members[userID]
	m.Stale = v.stale
	return m
}
func (v *fakeVoice) Policy(string) access.StreamPolicy { return access.VideoPermission }
func (v *fakeVoice) Stale(string) bool                 { return v.stale }
func (v *fakeVoice) Channel(_ context.Context, userID string, _ []string) (string, string, bool) {
	if m, ok := v.members[userID]; ok && m.InChannel {
		return "g", "c", true
	}
	return "", "", false
}

func (f *fixture) discordUser(t *testing.T, name string) store.User {
	t.Helper()
	u, _, err := auth.NewAccounts(f.db, nil).SignIn(context.Background(), oauth.Identity{Provider: oauth.Discord, Subject: name, Username: name})
	if err != nil {
		t.Fatal(err)
	}
	return u
}

func TestDiscordRoomLeavingVoice(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	f.gw.DetectFeatures(ctx)
	inVoice := rooms.VoiceMember{InChannel: true, InChannelToStream: true}
	v := &fakeVoice{members: map[string]rooms.VoiceMember{
		"streamer": {InChannel: true, InChannelToStream: true, CanVideo: true},
		"viewer":   inVoice,
	}}
	f.gw.rooms.SetVoice(v)
	room, err := f.gw.rooms.DiscordRoom(ctx, "g", "c")
	if err != nil {
		t.Fatal(err)
	}
	streamer, viewer := f.discordUser(t, "streamer"), f.discordUser(t, "viewer")

	st, err := f.gw.Start(ctx, StartRequest{User: streamer, Source: SourceWeb, RoomSlug: room.Slug})
	if err != nil {
		t.Fatal(err)
	}
	f.post(t, st, "Bearer "+st.Token, http.StatusCreated)
	f.reconcile(t)
	f.play(t, st, f.user.ID, "", http.StatusForbidden) // not signed in with Discord
	vs := f.play(t, st, viewer.ID, "", http.StatusCreated)
	f.reconcile(t)

	// The viewer leaves voice: their media pauses, and resumes when they are back.
	v.members["viewer"] = rooms.VoiceMember{}
	f.gw.Reevaluate(ctx, room)
	if s := f.mtxSession(t, vs.mtxID); !s.VideoPaused || !s.AudioPaused {
		t.Fatalf("viewer who left voice is not paused: %+v", s)
	}
	v.members["viewer"] = inVoice
	f.gw.Reevaluate(ctx, room)
	if s := f.mtxSession(t, vs.mtxID); s.VideoPaused || s.AudioPaused {
		t.Fatalf("viewer back in voice is still paused: %+v", s)
	}

	// While the bot's view is stale, nobody is paused or ended.
	v.members["viewer"] = rooms.VoiceMember{}
	v.members["streamer"] = rooms.VoiceMember{}
	v.stale = true
	f.gw.Reevaluate(ctx, room)
	if s := f.mtxSession(t, vs.mtxID); s.VideoPaused {
		t.Fatal("viewer paused while the voice state is stale")
	}
	if f.state(t, st.Stream.ID) != Live {
		t.Fatal("stream ended while the voice state is stale")
	}
	if c := f.control(t, st, vs, "pause", ""); c != http.StatusNoContent {
		t.Fatalf("pause = %d", c)
	}
	if c := f.control(t, st, vs, "resume", ""); c != http.StatusForbidden {
		t.Fatalf("resume while stale = %d, want 403", c)
	}

	// Held viewers are not disconnected while it is stale either.
	v.stale = false
	v.members["streamer"] = rooms.VoiceMember{InChannelToStream: true, CanVideo: true}
	f.gw.Reevaluate(ctx, room)
	if f.state(t, st.Stream.ID) != Live {
		t.Fatal("stream ended within the streamer's grace period")
	}
	v.stale = true
	f.clock = f.clock.Add(holdTimeout + 1)
	f.gw.expire(ctx)
	f.mtxSession(t, vs.mtxID)
	v.stale = false
	f.gw.expire(ctx)
	if _, ok := f.mtx.Session(vs.mtxID); ok {
		t.Fatal("held viewer not disconnected after the hold timeout")
	}

	// The streamer's grace period is over.
	v.members["streamer"] = rooms.VoiceMember{}
	f.gw.Reevaluate(ctx, room)
	if got := f.endReason(t, st.Stream.ID); got != EndLeftVoice {
		t.Fatalf("end reason = %q, want %q", got, EndLeftVoice)
	}
}

func TestDiscordStartMode(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	v := &fakeVoice{members: map[string]rooms.VoiceMember{
		"streamer": {InChannel: true, InChannelToStream: true, CanVideo: true},
		"viewer":   {InChannel: true, InChannelToStream: true},
	}}
	f.gw.rooms.SetVoice(v)
	streamer, viewer := f.discordUser(t, "streamer"), f.discordUser(t, "viewer")
	start := func(u store.User, src Source, fallback bool) (Started, error) {
		return f.gw.Start(ctx, StartRequest{User: u, Source: src, LinkFallback: fallback})
	}
	code := func(err error) string {
		if de, ok := err.(*DestinationError); ok {
			return de.Code
		}
		return ""
	}

	// In voice with the Video permission: the voice channel's room.
	st, err := start(streamer, SourceWeb, false)
	if err != nil || st.Room.Kind != "discord" {
		t.Fatalf("start = %+v, %v; want the Discord room", st.Room, err)
	}
	f.gw.EndStream(ctx, st.Stream.ID, EndStopped)

	// Without it: told why, or the link room when they accept it.
	if _, err := start(viewer, SourceWeb, false); code(err) != CodeDiscordUnavailable || !strings.Contains(err.Error(), "Video permission") {
		t.Fatalf("start without Video = %v", err)
	}
	if st, err := start(viewer, SourceWeb, true); err != nil || st.Room.Kind != "link" {
		t.Fatalf("start with fallback = %+v, %v; want the link room", st.Room, err)
	}

	// OBS in a Discord room, and the streamer leaves voice: OBS's reconnect
	// is refused for two minutes, unless they are back in voice.
	st, err = start(streamer, SourceOBS, false)
	if err != nil || st.Room.Kind != "discord" {
		t.Fatalf("OBS start = %+v, %v", st.Room, err)
	}
	room, _ := f.gw.rooms.DiscordRoom(ctx, "g", "c")
	v.members["streamer"] = rooms.VoiceMember{}
	f.gw.Reevaluate(ctx, room)
	if got := f.endReason(t, st.Stream.ID); got != EndLeftVoice {
		t.Fatalf("end reason = %q", got)
	}
	if _, err := start(streamer, SourceOBS, false); code(err) != CodeLeftVoice {
		t.Fatalf("OBS reconnect after leaving voice = %v, want %s", err, CodeLeftVoice)
	}
	v.members["streamer"] = rooms.VoiceMember{InChannel: true, InChannelToStream: true, CanVideo: true}
	st, err = start(streamer, SourceOBS, false)
	if err != nil || st.Room.Kind != "discord" {
		t.Fatalf("OBS reconnect back in voice = %+v, %v", st.Room, err)
	}
	f.gw.EndStream(ctx, st.Stream.ID, EndStopped)
	v.members["streamer"] = rooms.VoiceMember{}
	f.clock = f.clock.Add(obsBlock)
	if st, err := start(streamer, SourceOBS, false); err != nil || st.Room.Kind != "link" {
		t.Fatalf("OBS after the block = %+v, %v; want the link room", st.Room, err)
	}
}
