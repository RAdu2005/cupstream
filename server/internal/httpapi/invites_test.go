package httpapi

import (
	"net/http"
	"strings"
	"testing"

	"github.com/RAdu2005/cupstream/server/internal/api"
)

func (b *browser) slug() string {
	b.t.Helper()
	var acc api.Account
	b.json("GET", "/api/account", nil, &acc, 200)
	return acc.RoomSlug
}

func (b *browser) invite(slug string, req api.CreateInviteRequest) (api.Invite, string) {
	b.t.Helper()
	var out api.NewInvite
	b.json("POST", "/api/rooms/"+slug+"/invites", req, &out, http.StatusCreated)
	token, ok := strings.CutPrefix(out.URL, b.e.srv.URL+"/i/")
	if !ok || len(token) < 20 {
		b.t.Fatalf("invite URL %q", out.URL)
	}
	return out.Invite, token
}

func (b *browser) redeem(token string, want int) api.RedeemedInvite {
	b.t.Helper()
	var out api.RedeemedInvite
	b.json("POST", "/api/invites/redeem", api.InviteToken{Token: token}, &out, want)
	return out
}

func TestInviteLinks(t *testing.T) {
	e := newTestEnv(t, envOptions{devLogin: true})
	alice, bob, carol, dave, anon := e.browser(t), e.browser(t), e.browser(t), e.browser(t), e.browser(t)
	alice.devLogin("alice")
	bob.devLogin("bob")
	carol.devLogin("carol")
	dave.devLogin("dave")
	slug := alice.slug()

	// Only the owner manages links, and the settings are checked.
	bob.json("POST", "/api/rooms/"+slug+"/invites", api.CreateInviteRequest{Expiry: "1h"}, nil, http.StatusForbidden)
	alice.json("POST", "/api/rooms/"+slug+"/invites", api.CreateInviteRequest{Expiry: "1w"}, nil, http.StatusBadRequest)
	alice.json("POST", "/api/rooms/"+slug+"/invites", api.CreateInviteRequest{Expiry: "1h", MaxUses: -1}, nil, http.StatusBadRequest)

	inv, token := alice.invite(slug, api.CreateInviteRequest{Expiry: "1h", MaxUses: 2})
	if inv.ExpiresAt == 0 || inv.MaxUses != 2 || inv.Uses != 0 || inv.CanStream {
		t.Fatalf("invite = %+v", inv)
	}

	// Anyone with the link sees where it leads; joining needs sign-in.
	var preview api.InvitePreview
	anon.json("POST", "/api/invites/preview", api.InviteToken{Token: token}, &preview, 200)
	if preview.RoomSlug != slug || preview.Owner.Handle != "alice" {
		t.Fatalf("preview = %+v", preview)
	}
	anon.redeem(token, http.StatusUnauthorized)
	if r := anon.do("POST", "/api/invites/preview", `{"token":"nope"}`); r.status != 404 || !strings.Contains(r.body, "invite_invalid") {
		t.Fatalf("unknown token = %d %s", r.status, r.body)
	}

	bob.json("GET", "/api/rooms/"+slug, nil, nil, http.StatusForbidden)
	if got := bob.redeem(token, 200); got.RoomSlug != slug {
		t.Fatalf("redeemed into %q", got.RoomSlug)
	}
	if r := bob.room(slug); r.Permissions.Stream {
		t.Fatal("a watch link lets bob stream")
	}
	// Opening the link again, or as the owner, uses nothing.
	bob.redeem(token, 200)
	alice.redeem(token, 200)
	carol.redeem(token, 200)
	dave.redeem(token, http.StatusNotFound) // used up
	var list []api.Invite
	alice.json("GET", "/api/rooms/"+slug+"/invites", nil, &list, 200)
	if len(list) != 1 || list[0].Uses != 2 {
		t.Fatalf("invites = %+v", list)
	}
	// People on a link are never added to the allowed list.
	var allowed []api.AllowedEntry
	alice.json("GET", "/api/rooms/"+slug+"/allowed", nil, &allowed, 200)
	if len(allowed) != 0 {
		t.Fatalf("allowed = %+v", allowed)
	}

	// A streaming link: Dave may stream and watch.
	_, streamToken := alice.invite(slug, api.CreateInviteRequest{Expiry: "never", CanStream: true})
	dave.redeem(streamToken, 200)
	if r := dave.room(slug); !r.Permissions.Stream {
		t.Fatal("a streaming link does not let dave stream")
	}

	// Revoking a link removes everyone who joined through it.
	bob.json("DELETE", "/api/rooms/"+slug+"/invites/"+inv.ID, nil, nil, http.StatusForbidden)
	alice.json("DELETE", "/api/rooms/"+slug+"/invites/"+inv.ID, nil, nil, http.StatusNoContent)
	alice.json("DELETE", "/api/rooms/"+slug+"/invites/"+inv.ID, nil, nil, http.StatusNotFound)
	bob.json("GET", "/api/rooms/"+slug, nil, nil, http.StatusForbidden)
	carol.json("GET", "/api/rooms/"+slug, nil, nil, http.StatusForbidden)
	dave.room(slug)
	bob.json("POST", "/api/invites/preview", api.InviteToken{Token: token}, nil, http.StatusNotFound)
}

func TestInviteUntilStreamEnds(t *testing.T) {
	e := newTestEnv(t, envOptions{devLogin: true})
	alice, bob := e.browser(t), e.browser(t)
	alice.devLogin("alice")
	bob.devLogin("bob")
	slug := alice.slug()

	inv, token := alice.invite(slug, api.CreateInviteRequest{Expiry: "stream"})
	if !inv.UntilStreamEnd || inv.ExpiresAt != 0 {
		t.Fatalf("invite = %+v", inv)
	}
	bob.redeem(token, 200)

	if r := alice.obs(alice.streamKey(), "", h264Offer); r.status != http.StatusCreated {
		t.Fatalf("OBS = %d %s", r.status, r.body)
	}
	e.reconcile(t)
	room := bob.room(slug)
	if len(room.Streams) != 1 {
		t.Fatalf("streams = %+v", room.Streams)
	}

	// The room's last stream ends: so do the link and Bob's access.
	alice.json("DELETE", "/api/streams/"+room.Streams[0].ID, nil, nil, http.StatusNoContent)
	bob.json("GET", "/api/rooms/"+slug, nil, nil, http.StatusForbidden)
	bob.redeem(token, http.StatusNotFound)
	var list []api.Invite
	alice.json("GET", "/api/rooms/"+slug+"/invites", nil, &list, 200)
	if len(list) != 0 {
		t.Fatalf("invites after the stream ended = %+v", list)
	}
}
