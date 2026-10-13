package auth

import (
	"context"
	"errors"
	"testing"

	"github.com/RAdu2005/cupstream/server/internal/oauth"
	"github.com/RAdu2005/cupstream/server/internal/store"
)

func newAccounts(t *testing.T) (*Accounts, *store.DB) {
	t.Helper()
	db, err := store.Open(context.Background(), ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	return NewAccounts(db, nil), db
}

func google(sub, name string) oauth.Identity {
	return oauth.Identity{Provider: oauth.Google, Subject: sub, Username: name, DisplayName: name}
}

func discord(sub, username string) oauth.Identity {
	return oauth.Identity{Provider: oauth.Discord, Subject: sub, Username: username, DisplayName: username}
}

func TestSignInCreatesAccountAndRoom(t *testing.T) {
	ctx := context.Background()
	acc, db := newAccounts(t)

	u, created, err := acc.SignIn(ctx, google("g1", "Ana Popescu"))
	if err != nil || !created {
		t.Fatalf("SignIn = %v, created=%v", err, created)
	}
	if u.Handle != "ana_popescu" || u.DisplayName != "Ana Popescu" {
		t.Fatalf("handle %q, name %q", u.Handle, u.DisplayName)
	}
	room, err := db.GetLinkRoomByOwner(ctx, nullString(u.ID))
	if err != nil || room.Visibility.String != "invite_only" {
		t.Fatalf("link room: %+v, %v", room, err)
	}
	settings, err := db.GetUserSettings(ctx, u.ID)
	if err != nil || settings.StartMode != "discord_auto" || settings.ObsFallback != "link" {
		t.Fatalf("settings: %+v, %v", settings, err)
	}

	again, created, err := acc.SignIn(ctx, google("g1", "Ana Popescu"))
	if err != nil || created || again.ID != u.ID {
		t.Fatalf("second sign-in: %v, created=%v, same=%v", err, created, again.ID == u.ID)
	}

	// Same name, different person: the handle gets a suffix.
	other, _, err := acc.SignIn(ctx, discord("d1", "ana.popescu"))
	if err != nil || other.Handle == u.Handle || other.Handle[:12] != "ana_popescu_" {
		t.Fatalf("second user handle %q, %v", other.Handle, err)
	}
}

func TestHandleBase(t *testing.T) {
	for in, want := range map[string]string{
		"Radu":                                 "radu",
		"cool.gamer.99":                        "cool_gamer_99",
		"  Ünïcødé  Name ":                     "n_c_d_name",
		"x":                                    "user",
		"!!!":                                  "user",
		"a_very_long_name_that_goes_on_and_on": "a_very_long_name_that_goes",
	} {
		if got := handleBase(in); got != want {
			t.Errorf("handleBase(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestLinkUnlink(t *testing.T) {
	ctx := context.Background()
	acc, db := newAccounts(t)
	u, _, _ := acc.SignIn(ctx, google("g1", "ana"))

	if err := acc.Unlink(ctx, u.ID, oauth.Google); !errors.Is(err, ErrLastIdentity) {
		t.Fatalf("unlinking the only identity: %v", err)
	}
	if err := acc.Link(ctx, u.ID, discord("d1", "ana")); err != nil {
		t.Fatal(err)
	}
	if err := acc.Link(ctx, u.ID, discord("d1", "ana_renamed")); err != nil {
		t.Fatalf("relinking the same identity: %v", err)
	}
	if err := acc.Link(ctx, u.ID, discord("d2", "alt")); !errors.Is(err, ErrProviderLinked) {
		t.Fatalf("second Discord account: %v", err)
	}
	ids, _ := db.ListIdentities(ctx, u.ID)
	if len(ids) != 2 || ids[0].Username != "ana_renamed" {
		t.Fatalf("identities: %+v", ids)
	}
	if err := acc.Unlink(ctx, u.ID, oauth.Discord); err != nil {
		t.Fatal(err)
	}
	if ids, _ := db.ListIdentities(ctx, u.ID); len(ids) != 1 {
		t.Fatalf("%d identities after unlink", len(ids))
	}
}

func TestLinkOwnedByOtherAndMerge(t *testing.T) {
	ctx := context.Background()
	acc, db := newAccounts(t)
	ana, _, _ := acc.SignIn(ctx, google("g1", "ana"))
	other, _, _ := acc.SignIn(ctx, discord("d1", "ana_discord"))
	third, _, _ := acc.SignIn(ctx, google("g3", "mara"))

	// "other" was allowed into mara's room; that carries over.
	maraRoom, _ := db.GetLinkRoomByOwner(ctx, nullString(third.ID))
	if err := db.UpsertAllowed(ctx, store.UpsertAllowedParams{RoomID: maraRoom.ID, UserID: other.ID, CanStream: true}); err != nil {
		t.Fatal(err)
	}

	var owned *OwnedByOtherError
	if err := acc.Link(ctx, ana.ID, discord("d1", "ana_discord")); !errors.As(err, &owned) || owned.OtherUserID != other.ID {
		t.Fatalf("Link = %v, want OwnedByOtherError", err)
	}
	if err := acc.Merge(ctx, ana.ID, other.ID); err != nil {
		t.Fatal(err)
	}

	got, err := db.GetUserByIdentity(ctx, store.GetUserByIdentityParams{Provider: oauth.Discord, Subject: "d1"})
	if err != nil || got.ID != ana.ID {
		t.Fatalf("discord identity now belongs to %q (%v), want ana", got.ID, err)
	}
	if _, err := db.GetUser(ctx, other.ID); err == nil {
		t.Fatal("merged account still exists")
	}
	if a, err := db.GetAllowed(ctx, store.GetAllowedParams{RoomID: maraRoom.ID, UserID: ana.ID}); err != nil || !a.CanStream {
		t.Fatalf("allowed-list entry not carried over: %+v %v", a, err)
	}
}

func TestMergeDropsDuplicateProviders(t *testing.T) {
	ctx := context.Background()
	acc, db := newAccounts(t)
	ana, _, _ := acc.SignIn(ctx, google("g1", "ana"))
	other, _, _ := acc.SignIn(ctx, discord("d1", "x"))
	if err := acc.db.CreateIdentity(ctx, store.CreateIdentityParams{Provider: oauth.Google, Subject: "g2", UserID: other.ID, Username: "x"}); err != nil {
		t.Fatal(err)
	}
	if err := acc.Merge(ctx, ana.ID, other.ID); err != nil {
		t.Fatal(err)
	}
	ids, _ := db.ListIdentities(ctx, ana.ID)
	if len(ids) != 2 {
		t.Fatalf("identities after merge: %+v", ids)
	}
	for _, id := range ids {
		if id.Provider == oauth.Google && id.Subject != "g1" {
			t.Fatalf("ana's Google identity was replaced: %+v", id)
		}
	}
}

func TestUpdateProfile(t *testing.T) {
	ctx := context.Background()
	acc, _ := newAccounts(t)
	ana, _, _ := acc.SignIn(ctx, google("g1", "ana"))
	acc.SignIn(ctx, google("g2", "mara"))

	if u, err := acc.UpdateProfile(ctx, ana.ID, "Ana_2", " Ana P. "); err != nil || u.Handle != "ana_2" || u.DisplayName != "Ana P." {
		t.Fatalf("UpdateProfile = %+v, %v", u, err)
	}
	if _, err := acc.UpdateProfile(ctx, ana.ID, "MARA", "Ana"); !errors.Is(err, ErrHandleTaken) {
		t.Fatalf("taken handle: %v", err)
	}
	if _, err := acc.UpdateProfile(ctx, ana.ID, "ok_handle", "   "); !errors.Is(err, ErrInvalidName) {
		t.Fatalf("empty name: %v", err)
	}
	if _, err := acc.UpdateProfile(ctx, ana.ID, "no spaces", "Ana"); !errors.Is(err, ErrInvalidHandle) {
		t.Fatalf("invalid handle: %v", err)
	}
}
