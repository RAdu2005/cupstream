package auth

import (
	"context"
	"database/sql"
	"errors"
	"testing"
	"time"

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

// signUp creates the account of id with its username as the handle.
func signUp(t *testing.T, acc *Accounts, id oauth.Identity) store.User {
	t.Helper()
	u, err := acc.SignUp(context.Background(), id, id.Username, id.DisplayName)
	if err != nil {
		t.Fatal(err)
	}
	return u
}

func TestSignUpCreatesAccountAndRoom(t *testing.T) {
	ctx := context.Background()
	acc, db := newAccounts(t)

	if _, err := acc.SignIn(ctx, google("g1", "Ana Popescu")); !errors.Is(err, ErrNoAccount) {
		t.Fatalf("signing in before signing up: %v", err)
	}
	u, err := acc.SignUp(ctx, google("g1", "Ana Popescu"), "Ana_P", " Ana Popescu ")
	if err != nil {
		t.Fatal(err)
	}
	if u.Handle != "ana_p" || u.DisplayName != "Ana Popescu" {
		t.Fatalf("handle %q, name %q", u.Handle, u.DisplayName)
	}
	room, err := db.GetLinkRoomByOwner(ctx, nullString(u.ID))
	if err != nil || room.Slug != "ana_p" || room.Visibility.String != "invite_only" {
		t.Fatalf("link room: %+v, %v", room, err)
	}
	settings, err := db.GetUserSettings(ctx, u.ID)
	if err != nil || settings.StartMode != "discord_auto" || settings.ObsFallback != "link" {
		t.Fatalf("settings: %+v, %v", settings, err)
	}

	if again, err := acc.SignIn(ctx, google("g1", "Ana Popescu")); err != nil || again.ID != u.ID {
		t.Fatalf("second sign-in: %v, same=%v", err, again.ID == u.ID)
	}
	// Signing up twice (two tabs) signs in to the account.
	if again, err := acc.SignUp(ctx, google("g1", "Ana Popescu"), "other_handle", "Ana"); err != nil || again.ID != u.ID || again.Handle != "ana_p" {
		t.Fatalf("second sign-up: %+v, %v", again, err)
	}

	if _, err := acc.SignUp(ctx, discord("d1", "ana"), "ANA_P", "Ana"); !errors.Is(err, ErrHandleTaken) {
		t.Fatalf("taken handle: %v", err)
	}
	if _, err := acc.SignUp(ctx, discord("d1", "ana"), "a", "Ana"); !errors.Is(err, ErrInvalidHandle) {
		t.Fatalf("invalid handle: %v", err)
	}
	if _, err := acc.SignIn(ctx, discord("d1", "ana")); !errors.Is(err, ErrNoAccount) {
		t.Fatalf("a refused sign-up created an account: %v", err)
	}
}

func TestSuggestHandle(t *testing.T) {
	ctx := context.Background()
	acc, _ := newAccounts(t)

	if h, err := acc.SuggestHandle(ctx, google("g1", "Ana Popescu")); err != nil || h != "ana_popescu" {
		t.Fatalf("SuggestHandle = %q, %v", h, err)
	}
	signUp(t, acc, google("g1", "ana_popescu"))
	// Same name, different person: the suggestion gets a suffix.
	if h, err := acc.SuggestHandle(ctx, discord("d1", "ana.popescu")); err != nil || h[:12] != "ana_popescu_" {
		t.Fatalf("SuggestHandle = %q, %v", h, err)
	}
}

func TestHandleRenamingRules(t *testing.T) {
	ctx := context.Background()
	acc, db := newAccounts(t)
	now := time.Unix(1_800_000_000, 0)
	acc.now = func() time.Time { return now }
	ana := signUp(t, acc, google("g1", "ana"))
	mara := signUp(t, acc, google("g2", "mara"))
	slug := func() string {
		t.Helper()
		room, err := db.GetLinkRoomByOwner(ctx, nullString(ana.ID))
		if err != nil {
			t.Fatal(err)
		}
		return room.Slug
	}

	if _, err := acc.UpdateProfile(ctx, ana.ID, "anna", "Ana"); err != nil || slug() != "anna" {
		t.Fatalf("renaming: %v, room %q", err, slug())
	}
	st, _ := acc.HandleStatus(ctx, ana.ID)
	if st.NextChange != now.Add(7*24*time.Hour) || st.Previous != "ana" || st.PreviousUntil != now.Add(24*time.Hour) {
		t.Fatalf("status after renaming: %+v", st)
	}

	// For a day, the old handle is still ana's.
	now = now.Add(time.Hour)
	if _, err := acc.UpdateProfile(ctx, mara.ID, "ana", "Mara"); !errors.Is(err, ErrHandleTaken) {
		t.Fatalf("taking a reserved handle: %v", err)
	}
	if _, err := acc.SignUp(ctx, discord("d1", "ana"), "ana", "Ana"); !errors.Is(err, ErrHandleTaken) {
		t.Fatalf("signing up with a reserved handle: %v", err)
	}
	if ok, _ := acc.HandleAvailable(ctx, ana.ID, "ana"); !ok {
		t.Fatal("ana's own reserved handle is not available to her")
	}
	// Changing again within the week is refused, but not the display name.
	var cooldown *HandleCooldownError
	if _, err := acc.UpdateProfile(ctx, ana.ID, "ann", "Ana"); !errors.As(err, &cooldown) || cooldown.Until != st.NextChange {
		t.Fatalf("second change within a week: %v", err)
	}
	if u, err := acc.UpdateProfile(ctx, ana.ID, "anna", "Ana P."); err != nil || u.DisplayName != "Ana P." {
		t.Fatalf("changing the name only: %v", err)
	}

	// Switching back undoes the change: the week starts over, and the
	// handle given up is free at once.
	if u, err := acc.UpdateProfile(ctx, ana.ID, "ana", "Ana"); err != nil || u.Handle != "ana" || slug() != "ana" {
		t.Fatalf("switching back: %+v, %v, room %q", u, err, slug())
	}
	if st, _ := acc.HandleStatus(ctx, ana.ID); st != (HandleStatus{}) {
		t.Fatalf("status after switching back: %+v", st)
	}
	if _, err := acc.UpdateProfile(ctx, mara.ID, "anna", "Mara"); err != nil {
		t.Fatalf("taking the handle ana switched back from: %v", err)
	}

	// After a day, the old handle is free, and switching back is a change
	// like any other.
	if _, err := acc.UpdateProfile(ctx, ana.ID, "ana_p", "Ana"); err != nil {
		t.Fatal(err)
	}
	now = now.Add(25 * time.Hour)
	if _, err := acc.UpdateProfile(ctx, ana.ID, "ana", "Ana"); !errors.As(err, &cooldown) {
		t.Fatalf("switching back after a day: %v", err)
	}
	if _, err := acc.SignUp(ctx, discord("d1", "ana"), "ana", "Ana"); err != nil {
		t.Fatalf("signing up with a handle reserved a day ago: %v", err)
	}

	now = now.Add(7 * 24 * time.Hour)
	if _, err := acc.UpdateProfile(ctx, ana.ID, "ana_q", "Ana"); err != nil || slug() != "ana_q" {
		t.Fatalf("changing after a week: %v", err)
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
	u := signUp(t, acc, google("g1", "ana"))

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
	ana := signUp(t, acc, google("g1", "ana"))
	other := signUp(t, acc, discord("d1", "ana_discord"))
	third := signUp(t, acc, google("g3", "mara"))

	// "other" was allowed into mara's room; that carries over.
	maraRoom, _ := db.GetLinkRoomByOwner(ctx, nullString(third.ID))
	if err := db.UpsertAllowed(ctx, store.UpsertAllowedParams{RoomID: maraRoom.ID, UserID: other.ID, CanStream: true}); err != nil {
		t.Fatal(err)
	}

	// So does its friendship with mara.
	if err := db.UpsertFriendship(ctx, friendship(other.ID, third.ID)); err != nil {
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
	if ids, err := db.ListFriendIDs(ctx, ana.ID); err != nil || len(ids) != 1 || ids[0] != third.ID {
		t.Fatalf("friendship not carried over: %v %v", ids, err)
	}
}

func friendship(x, y string) store.UpsertFriendshipParams {
	if y < x {
		x, y = y, x
	}
	return store.UpsertFriendshipParams{UserA: x, UserB: y, Status: "accepted", RequestedBy: x}
}

func TestMergeDropsDuplicateProviders(t *testing.T) {
	ctx := context.Background()
	acc, db := newAccounts(t)
	ana := signUp(t, acc, google("g1", "ana"))
	other := signUp(t, acc, discord("d1", "xx"))
	if err := acc.db.CreateIdentity(ctx, store.CreateIdentityParams{Provider: oauth.Google, Subject: "g2", UserID: other.ID, Username: "xx"}); err != nil {
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
	ana := signUp(t, acc, google("g1", "ana"))
	signUp(t, acc, google("g2", "mara"))

	if _, err := acc.UpdateProfile(ctx, ana.ID, "MARA", "Ana"); !errors.Is(err, ErrHandleTaken) {
		t.Fatalf("taken handle: %v", err)
	}
	if _, err := acc.UpdateProfile(ctx, ana.ID, "ok_handle", "   "); !errors.Is(err, ErrInvalidName) {
		t.Fatalf("empty name: %v", err)
	}
	if _, err := acc.UpdateProfile(ctx, ana.ID, "no spaces", "Ana"); !errors.Is(err, ErrInvalidHandle) {
		t.Fatalf("invalid handle: %v", err)
	}
	if u, err := acc.UpdateProfile(ctx, ana.ID, "Ana_2", " Ana P. "); err != nil || u.Handle != "ana_2" || u.DisplayName != "Ana P." {
		t.Fatalf("UpdateProfile = %+v, %v", u, err)
	}
}

// A Discord room's slug is taken like a handle.
func TestHandleTakenByDiscordRoom(t *testing.T) {
	ctx := context.Background()
	acc, db := newAccounts(t)
	ana := signUp(t, acc, google("g1", "ana"))
	if err := db.CreateDiscordRoom(ctx, store.CreateDiscordRoomParams{
		ID: "r1", Slug: "0a1b2c3d4e", GuildID: nullString("g"), ChannelID: nullString("c"), CreatedAt: 1,
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := acc.UpdateProfile(ctx, ana.ID, "0a1b2c3d4e", "Ana"); !errors.Is(err, ErrHandleTaken) {
		t.Fatalf("renaming to a Discord room's slug: %v", err)
	}
	if _, err := acc.SignUp(ctx, discord("d1", "x"), "0a1b2c3d4e", "X"); !errors.Is(err, ErrHandleTaken) {
		t.Fatalf("signing up with a Discord room's slug: %v", err)
	}
}

func TestBannedAccounts(t *testing.T) {
	ctx := context.Background()
	acc, db := newAccounts(t)
	banned, err := acc.SignUp(ctx, google("g1", "Banned"), "banned", "Banned")
	if err != nil {
		t.Fatal(err)
	}
	other, err := acc.SignUp(ctx, discord("d1", "other"), "other", "other")
	if err != nil {
		t.Fatal(err)
	}
	if err := db.SetUserBanned(ctx, store.SetUserBannedParams{BannedAt: nullInt(1), ID: banned.ID}); err != nil {
		t.Fatal(err)
	}

	if _, err := acc.SignIn(ctx, google("g1", "Banned")); !errors.Is(err, ErrBanned) {
		t.Fatalf("signing in as a banned user: %v", err)
	}
	// Linking or merging the banned account would move its identity out of
	// the ban.
	if err := acc.Link(ctx, other.ID, google("g1", "Banned")); !errors.Is(err, ErrBanned) {
		t.Fatalf("linking a banned user's identity: %v", err)
	}
	if err := acc.Merge(ctx, other.ID, banned.ID); !errors.Is(err, ErrBanned) {
		t.Fatalf("merging a banned account: %v", err)
	}
	if u, err := db.GetUserByIdentity(ctx, store.GetUserByIdentityParams{Provider: oauth.Google, Subject: "g1"}); err != nil || u.ID != banned.ID {
		t.Fatalf("the identity moved: %+v, %v", u, err)
	}

	if err := db.SetUserBanned(ctx, store.SetUserBannedParams{ID: banned.ID}); err != nil {
		t.Fatal(err)
	}
	if _, err := acc.SignIn(ctx, google("g1", "Banned")); err != nil {
		t.Fatalf("signing in after the ban was lifted: %v", err)
	}
}

func nullInt(n int64) sql.NullInt64 { return sql.NullInt64{Int64: n, Valid: true} }
