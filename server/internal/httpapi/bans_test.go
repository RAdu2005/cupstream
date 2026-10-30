package httpapi

import (
	"net/http"
	"testing"

	"github.com/RAdu2005/cupstream/server/internal/api"
)

func TestKick(t *testing.T) {
	e := newTestEnv(t, envOptions{devLogin: true})
	alice, bob, carol := e.browser(t), e.browser(t), e.browser(t)
	alice.devLogin("alice")
	bob.devLogin("bob")
	carol.devLogin("carol")
	slug := alice.roomSlug()
	alice.json("PATCH", "/api/rooms/"+slug, api.UpdateRoomRequest{Visibility: "signed_in"}, nil, 200)

	if r := alice.obs(alice.streamKey(), "", h264Offer); r.status != http.StatusCreated {
		t.Fatalf("OBS = %d %s", r.status, r.body)
	}
	e.reconcile(t)
	streamID := bob.room(slug).Streams[0].ID
	if r := bob.whep(streamID); r.status != http.StatusCreated {
		t.Fatalf("bob WHEP = %d %s", r.status, r.body)
	}
	ws := bob.connect("bob-tab-1")
	ws.subscribe(slug)

	// Only the owner kicks, and not themselves.
	carol.json("POST", "/api/rooms/"+slug+"/kick/bob", nil, nil, http.StatusForbidden)
	alice.json("POST", "/api/rooms/"+slug+"/kick/alice", nil, nil, http.StatusBadRequest)
	alice.json("POST", "/api/rooms/"+slug+"/kick/nobody", nil, nil, http.StatusNotFound)

	before := len(e.mtx.Kicked())
	alice.json("POST", "/api/rooms/"+slug+"/kick/bob", nil, nil, http.StatusNoContent)
	if r := next[api.Refused](ws, "refused"); r.Code != "kicked" {
		t.Fatalf("refused = %+v", r)
	}
	if n := len(e.mtx.Kicked()) - before; n != 1 {
		t.Fatalf("closed %d sessions, want bob's 1", n)
	}
	// The room is open to signed-in users, so Bob may come back.
	bob.room(slug)
	ws.subscribe(slug)
}

func TestBan(t *testing.T) {
	e := newTestEnv(t, envOptions{devLogin: true})
	alice, bob := e.browser(t), e.browser(t)
	alice.devLogin("alice")
	bob.devLogin("bob")
	slug := alice.roomSlug()

	// Bob co-streams; a ban ends his stream and keeps him out.
	alice.json("PUT", "/api/rooms/"+slug+"/allowed/bob", api.SetAllowedRequest{CanStream: true}, nil, 200)
	bobKey := bob.streamKey()
	if r := bob.obs(bobKey, "?room="+slug, h264Offer); r.status != http.StatusCreated {
		t.Fatalf("bob OBS = %d %s", r.status, r.body)
	}
	e.reconcile(t)
	ws := bob.connect("bob-tab-1")
	ws.subscribe(slug)

	bob.json("PUT", "/api/rooms/"+slug+"/bans/alice", nil, nil, http.StatusForbidden)
	alice.json("PUT", "/api/rooms/"+slug+"/bans/bob", nil, nil, http.StatusNoContent)
	if r := next[api.Refused](ws, "refused"); r.Code != "banned" {
		t.Fatalf("refused = %+v", r)
	}
	if n := len(alice.room(slug).Streams); n != 0 {
		t.Fatalf("%d streams after banning the streamer", n)
	}
	var bans []api.BanEntry
	alice.json("GET", "/api/rooms/"+slug+"/bans", nil, &bans, 200)
	if len(bans) != 1 || bans[0].User.Handle != "bob" {
		t.Fatalf("bans = %+v", bans)
	}
	var allowed []api.AllowedEntry
	alice.json("GET", "/api/rooms/"+slug+"/allowed", nil, &allowed, 200)
	if len(allowed) != 0 {
		t.Fatalf("a banned user stays allowed: %+v", allowed)
	}

	// Even an open room stays closed to him.
	alice.json("PATCH", "/api/rooms/"+slug, api.UpdateRoomRequest{Visibility: "everyone"}, nil, 200)
	if r := bob.do("GET", "/api/rooms/"+slug, ""); r.status != http.StatusForbidden || decode[api.ErrorResponse](t, r.body).Code != "banned" {
		t.Fatalf("banned GET room = %d %s", r.status, r.body)
	}
	if r := ws.refused(slug); r.Code != "banned" {
		t.Fatalf("banned subscribe = %+v", r)
	}
	if r := bob.obs(bobKey, "?room="+slug, h264Offer); r.status != http.StatusForbidden {
		t.Fatalf("banned OBS = %d", r.status)
	}

	// Unbanning lets him into the open room again, without restoring his
	// place on the allowed list.
	alice.json("DELETE", "/api/rooms/"+slug+"/bans/bob", nil, nil, http.StatusNoContent)
	alice.json("DELETE", "/api/rooms/"+slug+"/bans/bob", nil, nil, http.StatusNotFound)
	if r := bob.room(slug); r.Permissions.Stream {
		t.Fatal("unbanned bob can stream again")
	}
}
