package httpapi

import (
	"net/http"
	"testing"

	"github.com/RAdu2005/cupstream/server/internal/api"
)

func (b *browser) befriend(other *browser, otherUser, me api.User) {
	b.t.Helper()
	b.json("POST", "/api/friends/requests", api.SendFriendRequest{UserID: otherUser.ID}, nil, http.StatusOK)
	other.json("POST", "/api/friends/requests/"+me.ID+"/accept", nil, nil, http.StatusOK)
}

func TestHome(t *testing.T) {
	e := newTestEnv(t, envOptions{devLogin: true})
	alice, bob, carol, dave, erin := e.browser(t), e.browser(t), e.browser(t), e.browser(t), e.browser(t)
	me := alice.devLogin("alice")
	bob.devLogin("bob")
	carol.devLogin("carol")
	daveUser := dave.devLogin("dave")
	erinUser := erin.devLogin("erin")

	// Bob allows Alice to stream; Carol invites her; Dave's room is closed to her.
	bob.json("PUT", "/api/rooms/"+bob.roomSlug()+"/allowed/alice", api.SetAllowedRequest{CanStream: true}, nil, 200)
	_, token := carol.invite(carol.roomSlug(), api.CreateInviteRequest{Expiry: "never"})
	alice.redeem(token, 200)

	// Carol is live in her room; Dave and Erin are Alice's friends and live.
	if r := carol.obs(carol.streamKey(), "", h264Offer); r.status != http.StatusCreated {
		t.Fatalf("carol OBS = %d %s", r.status, r.body)
	}
	alice.befriend(dave, daveUser, me)
	alice.befriend(erin, erinUser, me)
	dave.obs(dave.streamKey(), "", h264Offer)
	erin.json("PATCH", "/api/rooms/"+erin.roomSlug(), api.UpdateRoomRequest{Visibility: "signed_in"}, nil, 200)
	erin.obs(erin.streamKey(), "", h264Offer)
	e.reconcile(t)

	var home api.Home
	alice.json("GET", "/api/home", nil, &home, 200)
	got := map[string]api.HomeRoom{}
	var order []string
	for _, r := range home.Rooms {
		got[r.Owner.Handle] = r
		order = append(order, r.Owner.Handle)
	}
	if len(order) != 3 || order[0] != "alice" || order[1] != "carol" || order[2] != "bob" {
		t.Fatalf("rooms %v, want alice, then live carol, then bob", order)
	}
	if r := got["alice"]; r.Access != "own" || !r.CanStream {
		t.Fatalf("own room %+v", r)
	}
	if r := got["bob"]; r.Access != "allowed" || !r.CanStream || len(r.Streams) != 0 {
		t.Fatalf("bob's room %+v", r)
	}
	if r := got["carol"]; r.Access != "pass" || r.CanStream || len(r.Streams) != 1 || r.Streams[0].User.Handle != "carol" {
		t.Fatalf("carol's room %+v", r)
	}
	// Erin's room is open to signed-in users; Dave's is invite only, so his
	// friend does not see him live there.
	if len(home.FriendsLive) != 1 || home.FriendsLive[0].Owner.Handle != "erin" || home.FriendsLive[0].Access != "" {
		t.Fatalf("friends live = %+v", home.FriendsLive)
	}

	// A ban removes the room.
	bob.json("PUT", "/api/rooms/"+bob.roomSlug()+"/bans/alice", nil, nil, http.StatusNoContent)
	alice.json("GET", "/api/home", nil, &home, 200)
	if len(home.Rooms) != 2 {
		t.Fatalf("rooms after a ban = %+v", home.Rooms)
	}
	e.browser(t).json("GET", "/api/home", nil, nil, http.StatusUnauthorized)
}
