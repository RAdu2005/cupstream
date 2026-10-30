package httpapi

import (
	"context"
	"net/http"
	"testing"
	"time"

	"github.com/coder/websocket"

	"github.com/RAdu2005/cupstream/server/internal/api"
)

func TestAdminAccess(t *testing.T) {
	e := newTestEnv(t, envOptions{devLogin: true, admins: []string{"dev:root"}})
	root, bob, anon := e.browser(t), e.browser(t), e.browser(t)
	root.devLogin("root")
	bob.devLogin("bob")

	var me api.Me
	root.json("GET", "/api/me", nil, &me, 200)
	if !me.Admin {
		t.Fatal("root is not an admin")
	}
	me = api.Me{}
	bob.json("GET", "/api/me", nil, &me, 200)
	if me.Admin {
		t.Fatal("bob is an admin")
	}
	bob.json("GET", "/api/admin", nil, nil, http.StatusForbidden)
	bob.json("PUT", "/api/admin/bans/root", nil, nil, http.StatusForbidden)
	anon.json("GET", "/api/admin", nil, nil, http.StatusUnauthorized)
	root.json("GET", "/api/admin", nil, nil, 200)
}

func TestAdminEndsStream(t *testing.T) {
	e := newTestEnv(t, envOptions{devLogin: true, admins: []string{"dev:root"}})
	root, bob := e.browser(t), e.browser(t)
	root.devLogin("root")
	bob.devLogin("bob")
	var started api.StartStreamResponse
	bob.json("POST", "/api/streams", api.StartStreamRequest{Source: "web", LinkFallback: true}, &started, http.StatusCreated)

	var o api.AdminOverview
	root.json("GET", "/api/admin", nil, &o, 200)
	if len(o.Streams) != 1 || o.Streams[0].Stream.ID != started.Stream.ID || o.Streams[0].RoomSlug != started.RoomSlug || o.Streams[0].Stream.User.Handle != "bob" {
		t.Fatalf("overview %+v", o)
	}
	root.json("POST", "/api/admin/streams/"+started.Stream.ID+"/end", nil, nil, http.StatusNoContent)
	if s, _ := e.db.GetStream(t.Context(), started.Stream.ID); s.EndReason.String != "ended_by_admin" {
		t.Fatalf("end reason %q", s.EndReason.String)
	}
	root.json("POST", "/api/admin/streams/"+started.Stream.ID+"/end", nil, nil, http.StatusNotFound)
}

func TestPlatformBan(t *testing.T) {
	e := newTestEnv(t, envOptions{devLogin: true, admins: []string{"dev:root"}})
	root, bob, carol := e.browser(t), e.browser(t), e.browser(t)
	root.devLogin("root")
	bob.devLogin("bob")
	carol.devLogin("carol")
	key := bob.streamKey()
	slug := bob.roomSlug()
	bob.json("PATCH", "/api/rooms/"+slug, api.UpdateRoomRequest{Visibility: "everyone"}, nil, 200)
	if r := bob.obs(key, "", h264Offer); r.status != http.StatusCreated {
		t.Fatalf("OBS = %d %s", r.status, r.body)
	}
	e.reconcile(t)
	streams := bob.room(slug).Streams
	if len(streams) != 1 {
		t.Fatalf("streams %+v", streams)
	}
	stream := streams[0]

	// Carol watches Bob; then she is banned while watching.
	if r := carol.whep(stream.ID); r.status != http.StatusCreated {
		t.Fatalf("whep = %d %s", r.status, r.body)
	}
	cs := carol.connect("carol-tab-1")
	root.json("PUT", "/api/admin/bans/carol", nil, nil, http.StatusNoContent)
	if n := len(e.mtx.Sessions("s/"+stream.ID)) - 1; n != 0 { // and Bob's own
		t.Fatalf("%d sessions left on bob's stream after carol's ban", n)
	}
	ctx, cancel := context.WithTimeout(t.Context(), 3*time.Second)
	defer cancel()
	for {
		if _, _, err := cs.ws.Read(ctx); err != nil {
			if websocket.CloseStatus(err) != 4001 {
				t.Fatalf("carol's socket: %v", err)
			}
			break
		}
	}
	carol.json("GET", "/api/me", nil, nil, http.StatusUnauthorized)
	carol.json("POST", "/api/auth/dev/login", api.DevLoginRequest{Handle: "carol"}, nil, http.StatusForbidden)

	// Bob is banned while live: the stream ends, OBS is refused.
	root.json("PUT", "/api/admin/bans/bob", nil, nil, http.StatusNoContent)
	if s, _ := e.db.GetStream(t.Context(), stream.ID); s.EndReason.String != "user_banned" {
		t.Fatalf("end reason %q", s.EndReason.String)
	}
	if r := e.browser(t).obs(key, "", h264Offer); r.status != http.StatusForbidden || decode[api.ErrorResponse](t, r.body).Code != "banned" {
		t.Fatalf("OBS after the ban: %d %s", r.status, r.body)
	}

	var o api.AdminOverview
	root.json("GET", "/api/admin", nil, &o, 200)
	if len(o.Banned) != 2 || len(o.Streams) != 0 {
		t.Fatalf("overview %+v", o)
	}
	root.json("PUT", "/api/admin/bans/root", nil, nil, http.StatusConflict)
	root.json("PUT", "/api/admin/bans/nobody", nil, nil, http.StatusNotFound)

	root.json("DELETE", "/api/admin/bans/carol", nil, nil, http.StatusNoContent)
	carol.devLogin("carol")
	root.json("GET", "/api/admin", nil, &o, 200)
	if len(o.Banned) != 1 || o.Banned[0].User.Handle != "bob" {
		t.Fatalf("banned after lifting carol's ban: %+v", o.Banned)
	}
}
