package httpapi

import (
	"net/http"
	"slices"
	"strings"
	"testing"

	"github.com/RAdu2005/cupstream/server/internal/api"
	"github.com/RAdu2005/cupstream/server/internal/mediamtx/mtxtest"
)

const h264Offer = "v=0\r\nm=video 9 UDP/TLS/RTP/SAVPF 96\r\na=rtpmap:96 H264/90000\r\nm=audio 9 UDP/TLS/RTP/SAVPF 111\r\na=rtpmap:111 opus/48000/2\r\n"

func (b *browser) streamKey() string {
	b.t.Helper()
	var k api.NewStreamKey
	b.json("POST", "/api/account/stream-key", nil, &k, 200)
	if !strings.HasPrefix(k.Key, "cs_") || !strings.HasSuffix(k.WhipURL, "/whip") {
		b.t.Fatalf("stream key = %+v", k)
	}
	return k.Key
}

// obs publishes like OBS: POST /whip with the stream key as bearer token.
func (b *browser) obs(key, query, offer string) response {
	b.t.Helper()
	return b.do("POST", "/whip"+query, offer, "Content-Type", "application/sdp", "Authorization", "Bearer "+key)
}

func (b *browser) whep(streamID string) response {
	b.t.Helper()
	return b.do("POST", "/api/streams/"+streamID+"/whep", h264Offer, "Content-Type", "application/sdp")
}

func (b *browser) room(slug string) api.Room {
	b.t.Helper()
	var r api.Room
	b.json("GET", "/api/rooms/"+slug, nil, &r, 200)
	return r
}

func (e *testEnv) reconcile(t *testing.T) {
	t.Helper()
	if err := e.gw.Reconcile(t.Context()); err != nil {
		t.Fatal(err)
	}
}

// TestTwoOBSInOneLinkRoom is phase 2's acceptance scenario: two OBS
// instances stream into one link room; an allowed browser watches both and
// everyone else is refused.
func TestTwoOBSInOneLinkRoom(t *testing.T) {
	e := newTestEnv(t, envOptions{devLogin: true})
	alice, bob, carol, dave, anon := e.browser(t), e.browser(t), e.browser(t), e.browser(t), e.browser(t)
	alice.devLogin("alice")
	bob.devLogin("bob")
	carol.devLogin("carol")
	dave.devLogin("dave")

	var acc api.Account
	alice.json("GET", "/api/account", nil, &acc, 200)
	slug := acc.RoomSlug
	if r := alice.room(slug); r.Visibility != "invite_only" || !r.Permissions.ModerateStreams {
		t.Fatalf("alice's room = %+v", r)
	}

	// Alice lets Bob co-stream and Carol watch.
	alice.json("PUT", "/api/rooms/"+slug+"/allowed/bob", api.SetAllowedRequest{CanStream: true}, nil, 200)
	alice.json("PUT", "/api/rooms/"+slug+"/allowed/carol", api.SetAllowedRequest{}, nil, 200)
	bob.json("PUT", "/api/rooms/"+slug+"/allowed/dave", api.SetAllowedRequest{}, nil, http.StatusForbidden)
	var allowed []api.AllowedEntry
	alice.json("GET", "/api/rooms/"+slug+"/allowed", nil, &allowed, 200)
	if len(allowed) != 2 || allowed[0].User.Handle != "bob" || !allowed[0].CanStream {
		t.Fatalf("allowed = %+v", allowed)
	}

	// Alice's OBS follows her start mode (Discord auto, no bot yet: link room).
	r := alice.obs(alice.streamKey(), "", h264Offer)
	if r.status != http.StatusCreated || !strings.Contains(r.header.Get("Location"), "/api/media/") || r.header.Get("Content-Length") == "" {
		t.Fatalf("alice OBS = %d %s %v", r.status, r.body, r.header)
	}
	// Bob's OBS names Alice's room.
	bobKey := bob.streamKey()
	if r := bob.obs(bobKey, "?room="+slug, h264Offer); r.status != http.StatusCreated {
		t.Fatalf("bob OBS = %d %s", r.status, r.body)
	}
	e.reconcile(t)

	room := carol.room(slug)
	if len(room.Streams) != 2 || room.Permissions.Stream || room.Permissions.ModerateStreams {
		t.Fatalf("carol sees %+v", room)
	}
	for _, s := range room.Streams {
		if s.State != "live" || s.Source != "obs" {
			t.Fatalf("stream %+v", s)
		}
		if r := carol.whep(s.ID); r.status != http.StatusCreated {
			t.Fatalf("carol WHEP = %d %s", r.status, r.body)
		}
		if r := dave.whep(s.ID); r.status != http.StatusForbidden || !strings.Contains(r.body, "not_allowed") {
			t.Fatalf("dave WHEP = %d %s", r.status, r.body)
		}
		if r := anon.whep(s.ID); r.status != http.StatusUnauthorized {
			t.Fatalf("anonymous WHEP = %d %s", r.status, r.body)
		}
	}
	dave.json("GET", "/api/rooms/"+slug, nil, nil, http.StatusForbidden)
	anon.json("GET", "/api/rooms/"+slug, nil, nil, http.StatusUnauthorized)
	e.reconcile(t)
	if v := carol.room(slug).Streams[0].Viewers; v != 1 {
		t.Fatalf("viewers = %d", v)
	}

	// Removing Carol disconnects her.
	before := len(e.mtx.Kicked())
	alice.json("DELETE", "/api/rooms/"+slug+"/allowed/carol", nil, nil, http.StatusNoContent)
	if kicked := len(e.mtx.Kicked()) - before; kicked != 2 {
		t.Fatalf("kicked %d sessions, want carol's 2", kicked)
	}
	carol.json("GET", "/api/rooms/"+slug, nil, nil, http.StatusForbidden)

	// Opening the room to everyone lets anonymous visitors watch.
	alice.json("PATCH", "/api/rooms/"+slug, api.UpdateRoomRequest{Visibility: "everyone"}, nil, 200)
	room = anon.room(slug)
	if r := anon.whep(room.Streams[0].ID); r.status != http.StatusCreated {
		t.Fatalf("anonymous WHEP in open room = %d %s", r.status, r.body)
	}
	if room.Permissions.Chat {
		t.Fatal("anonymous visitors cannot chat")
	}

	// Bob loses streaming: his stream ends.
	alice.json("PUT", "/api/rooms/"+slug+"/allowed/bob", api.SetAllowedRequest{CanStream: false}, nil, 200)
	if n := len(anon.room(slug).Streams); n != 1 {
		t.Fatalf("%d streams after revoking bob", n)
	}
	if r := bob.obs(bobKey, "?room="+slug, h264Offer); r.status != http.StatusForbidden {
		t.Fatalf("bob OBS after revocation = %d %s", r.status, r.body)
	}
}

func TestOneLiveStreamPerUser(t *testing.T) {
	e := newTestEnv(t, envOptions{devLogin: true})
	alice := e.browser(t)
	alice.devLogin("alice")
	key := alice.streamKey()
	if r := alice.obs(key, "", h264Offer); r.status != http.StatusCreated {
		t.Fatalf("OBS = %d %s", r.status, r.body)
	}
	e.reconcile(t)

	// OBS reconnecting its own stream keeps the same stream.
	var acc api.Account
	alice.json("GET", "/api/account", nil, &acc, 200)
	first := alice.room(acc.RoomSlug).Streams[0].ID
	if r := alice.obs(key, "", h264Offer); r.status != http.StatusCreated || !strings.Contains(r.header.Get("Location"), first) {
		t.Fatalf("OBS reconnect = %d %v", r.status, r.header.Get("Location"))
	}

	// Going live from the browser asks first; Discord (auto) also needs the
	// link-room fallback accepted.
	var errResp api.ErrorResponse
	alice.json("POST", "/api/streams", api.StartStreamRequest{Source: "web"}, &errResp, http.StatusConflict)
	if errResp.Code != "discord_unavailable" {
		t.Fatalf("code = %q", errResp.Code)
	}
	alice.json("POST", "/api/streams", api.StartStreamRequest{Source: "web", LinkFallback: true}, &errResp, http.StatusConflict)
	if errResp.Code != "already_live" {
		t.Fatalf("code = %q", errResp.Code)
	}
	var started api.StartStreamResponse
	alice.json("POST", "/api/streams", api.StartStreamRequest{Source: "web", LinkFallback: true, Replace: true}, &started, http.StatusCreated)
	if started.RoomSlug != acc.RoomSlug || started.Token == "" || !strings.HasSuffix(started.WhipURL, "/api/streams/"+started.Stream.ID+"/whip") {
		t.Fatalf("started = %+v", started)
	}
	if s, _ := e.db.GetStream(t.Context(), first); s.EndReason.String != "replaced" {
		t.Fatalf("OBS stream end reason %q", s.EndReason.String)
	}

	// OBS cannot replace a stream: it is refused while the browser is live.
	if r := alice.obs(key, "", h264Offer); r.status != http.StatusConflict || !strings.Contains(r.body, "already_live") {
		t.Fatalf("OBS while live in browser = %d %s", r.status, r.body)
	}

	// The browser publishes with its token, then stops with a WHIP DELETE.
	r := alice.do("POST", started.WhipURL, h264Offer, "Content-Type", "application/sdp", "Authorization", "Bearer "+started.Token)
	if r.status != http.StatusCreated {
		t.Fatalf("web WHIP = %d %s", r.status, r.body)
	}
	if d := alice.do("DELETE", r.header.Get("Location"), ""); d.status != http.StatusOK {
		t.Fatalf("WHIP DELETE = %d %s", d.status, d.body)
	}
	if s, _ := e.db.GetStream(t.Context(), started.Stream.ID); s.EndReason.String != "stopped" {
		t.Fatalf("web stream end reason %q", s.EndReason.String)
	}
}

func TestPublishRefusals(t *testing.T) {
	e := newTestEnv(t, envOptions{devLogin: true})
	alice, bob := e.browser(t), e.browser(t)
	alice.devLogin("alice")
	bob.devLogin("bob")
	key := alice.streamKey()

	if r := alice.obs("cs_wrong", "", h264Offer); r.status != http.StatusUnauthorized {
		t.Fatalf("wrong key = %d", r.status)
	}
	hevc := "v=0\r\nm=video 9 UDP/TLS/RTP/SAVPF 98\r\na=rtpmap:98 H265/90000\r\n"
	if r := alice.obs(key, "", hevc); r.status != http.StatusUnsupportedMediaType || !strings.Contains(r.body, "HEVC") {
		t.Fatalf("HEVC = %d %s", r.status, r.body)
	}
	// A refused offer leaves no stream behind.
	var acc api.Account
	alice.json("GET", "/api/account", nil, &acc, 200)
	if n := len(alice.room(acc.RoomSlug).Streams); n != 0 {
		t.Fatalf("%d streams after a refused offer", n)
	}

	// Regenerating the key revokes the old one.
	alice.streamKey()
	if r := alice.obs(key, "", h264Offer); r.status != http.StatusUnauthorized {
		t.Fatalf("revoked key = %d", r.status)
	}

	// OBS fallback "refuse": no Discord room means no stream.
	bob.json("PATCH", "/api/account/settings", api.Settings{StartMode: "discord_auto", ObsFallback: "refuse"}, nil, 200)
	if r := bob.obs(bob.streamKey(), "", h264Offer); r.status != http.StatusConflict || !strings.Contains(r.body, "discord_unavailable") {
		t.Fatalf("refuse fallback = %d %s", r.status, r.body)
	}
	// Streaming into someone else's room needs their permission.
	if r := bob.obs(bob.streamKey(), "?room="+acc.RoomSlug, h264Offer); r.status != http.StatusForbidden {
		t.Fatalf("foreign room = %d %s", r.status, r.body)
	}
}

func TestModeratorEndsStream(t *testing.T) {
	e := newTestEnv(t, envOptions{devLogin: true})
	alice, bob, carol := e.browser(t), e.browser(t), e.browser(t)
	alice.devLogin("alice")
	bob.devLogin("bob")
	carol.devLogin("carol")
	var acc api.Account
	alice.json("GET", "/api/account", nil, &acc, 200)
	alice.json("PUT", "/api/rooms/"+acc.RoomSlug+"/allowed/bob", api.SetAllowedRequest{CanStream: true}, nil, 200)
	alice.json("PUT", "/api/rooms/"+acc.RoomSlug+"/allowed/carol", api.SetAllowedRequest{CanStream: true}, nil, 200)

	var started api.StartStreamResponse
	bob.json("POST", "/api/streams", api.StartStreamRequest{Source: "web", Room: acc.RoomSlug}, &started, http.StatusCreated)
	carol.json("DELETE", "/api/streams/"+started.Stream.ID, nil, nil, http.StatusForbidden)
	alice.json("DELETE", "/api/streams/"+started.Stream.ID, nil, nil, http.StatusNoContent)
	if s, _ := e.db.GetStream(t.Context(), started.Stream.ID); s.EndReason.String != "ended_by_moderator" {
		t.Fatalf("end reason %q", s.EndReason.String)
	}
}

func TestSessionPatchFiltering(t *testing.T) {
	e := newTestEnv(t, envOptions{devLogin: true})
	alice := e.browser(t)
	alice.devLogin("alice")
	alice.json("PATCH", "/api/account/settings", api.Settings{StartMode: "link", ObsFallback: "link"}, nil, 200)
	r := alice.obs(alice.streamKey(), "", h264Offer)
	loc := r.header.Get("Location")

	// A client cannot pause or resume itself through the gateway.
	if p := alice.do("PATCH", loc, `{"paused":false}`, "Content-Type", "application/json"); p.status != http.StatusUnsupportedMediaType {
		t.Fatalf("JSON PATCH = %d", p.status)
	}
	if p := alice.do("PATCH", loc, "a=candidate:1 1 udp 1 127.0.0.1 5000 typ host\r\n", "Content-Type", "application/trickle-ice-sdpfrag"); p.status != http.StatusNoContent {
		t.Fatalf("trickle PATCH = %d %s", p.status, p.body)
	}
	for _, bad := range []string{"/api/media/notastream/whip/x", "/api/media/" + strings.Repeat("a", 16) + "/other/00000000-0000-0000-0000-000000000000"} {
		if p := alice.do("DELETE", bad, ""); p.status != http.StatusNotFound {
			t.Fatalf("DELETE %s = %d", bad, p.status)
		}
	}
	if !slices.ContainsFunc(e.mtx.Sessions("s/"+strings.Split(loc, "/")[5]), func(s mtxSession) bool { return len(s.Patches) == 1 }) {
		t.Fatal("trickle PATCH did not reach MediaMTX")
	}
}

type mtxSession = mtxtest.Session
