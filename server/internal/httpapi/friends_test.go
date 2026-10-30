package httpapi

import (
	"net/http"
	"testing"

	"github.com/RAdu2005/cupstream/server/internal/api"
)

func TestFriends(t *testing.T) {
	e := newTestEnv(t, envOptions{devLogin: true})
	alice, bob, carol := e.browser(t), e.browser(t), e.browser(t)
	a := alice.devLogin("alice")
	b := bob.devLogin("bob")
	c := carol.devLogin("carol")
	as, bs := alice.connect("alice-tab"), bob.connect("bob-tab-1")

	// Alice finds Bob by his exact handle, not by part of it.
	var found api.UserRelation
	alice.json("GET", "/api/friends/lookup?handle=Bob", nil, &found, 200)
	if found.User.ID != b.ID || found.Relation != "none" {
		t.Fatalf("lookup = %+v", found)
	}
	alice.json("GET", "/api/friends/lookup?handle=bo", nil, nil, http.StatusNotFound)
	alice.json("GET", "/api/friends/lookup?handle=alice", nil, nil, http.StatusBadRequest)

	// A request reaches Bob at once, and Alice's other tabs.
	var rel api.UserRelation
	alice.json("POST", "/api/friends/requests", api.SendFriendRequest{UserID: b.ID}, &rel, 200)
	if rel.Relation != "outgoing" {
		t.Fatalf("request = %+v", rel)
	}
	if n := next[api.Notification](bs, "notification"); n.Kind != "friendRequest" || n.User.ID != a.ID {
		t.Fatalf("bob's notification = %+v", n)
	}
	next[api.FriendsChanged](bs, "friendsChanged")
	next[api.FriendsChanged](as, "friendsChanged")

	var list api.Friends
	bob.json("GET", "/api/friends", nil, &list, 200)
	if len(list.Incoming) != 1 || list.Incoming[0].User.ID != a.ID || len(list.Friends) != 0 || len(list.Outgoing) != 0 {
		t.Fatalf("bob's list = %+v", list)
	}

	// Only Bob can accept; Alice hears about it.
	alice.json("POST", "/api/friends/requests/"+b.ID+"/accept", nil, nil, http.StatusNotFound)
	bob.json("POST", "/api/friends/requests/"+a.ID+"/accept", nil, &rel, 200)
	if n := next[api.Notification](as, "notification"); n.Kind != "friendAccepted" || n.User.ID != b.ID {
		t.Fatalf("alice's notification = %+v", n)
	}
	alice.json("GET", "/api/friends", nil, &list, 200)
	if len(list.Friends) != 1 || list.Friends[0].User.Handle != "bob" || list.Friends[0].Since == 0 {
		t.Fatalf("alice's list = %+v", list)
	}

	// Carol asks Alice, who declines; then Carol asks again and cancels.
	carol.json("POST", "/api/friends/requests", api.SendFriendRequest{UserID: a.ID}, nil, 200)
	alice.json("DELETE", "/api/friends/requests/"+c.ID, nil, nil, http.StatusNoContent)
	alice.json("DELETE", "/api/friends/requests/"+c.ID, nil, nil, http.StatusNotFound)
	carol.json("POST", "/api/friends/requests", api.SendFriendRequest{UserID: a.ID}, nil, 200)
	carol.json("DELETE", "/api/friends/requests/"+a.ID, nil, nil, http.StatusNoContent)
	carol.json("GET", "/api/friends", nil, &list, 200)
	if len(list.Outgoing) != 0 {
		t.Fatalf("carol's list = %+v", list)
	}

	// Alice blocks Carol, who can then neither find nor ask her.
	alice.json("PUT", "/api/friends/blocked/"+c.ID, nil, &rel, 200)
	if rel.Relation != "blocked" {
		t.Fatalf("block = %+v", rel)
	}
	carol.json("GET", "/api/friends/lookup?handle=alice", nil, nil, http.StatusNotFound)
	carol.json("POST", "/api/friends/requests", api.SendFriendRequest{UserID: a.ID}, nil, http.StatusNotFound)
	if r := alice.do("POST", "/api/friends/requests", `{"userId":"`+c.ID+`"}`); r.status != http.StatusConflict {
		t.Fatalf("asking someone you blocked = %d %s", r.status, r.body)
	}
	var blocked []api.User
	alice.json("GET", "/api/friends/blocked", nil, &blocked, 200)
	if len(blocked) != 1 || blocked[0].ID != c.ID {
		t.Fatalf("blocked = %+v", blocked)
	}
	alice.json("DELETE", "/api/friends/blocked/"+c.ID, nil, nil, http.StatusNoContent)
	alice.json("DELETE", "/api/friends/blocked/"+c.ID, nil, nil, http.StatusNotFound)

	// Unfriending tells both.
	bob.json("DELETE", "/api/friends/"+a.ID, nil, nil, http.StatusNoContent)
	bob.json("DELETE", "/api/friends/"+a.ID, nil, nil, http.StatusNotFound)
	alice.json("GET", "/api/friends", nil, &list, 200)
	if len(list.Friends) != 0 {
		t.Fatalf("alice's list after unfriending = %+v", list)
	}

	// Signed out, nothing works.
	anon := e.browser(t)
	anon.json("GET", "/api/friends", nil, nil, http.StatusUnauthorized)
	anon.json("POST", "/api/friends/requests", api.SendFriendRequest{UserID: a.ID}, nil, http.StatusUnauthorized)
}
