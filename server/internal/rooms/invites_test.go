package rooms

import (
	"context"
	"database/sql"
	"errors"
	"testing"
	"time"

	"github.com/RAdu2005/cupstream/server/internal/access"
	"github.com/RAdu2005/cupstream/server/internal/auth"
	"github.com/RAdu2005/cupstream/server/internal/oauth"
	"github.com/RAdu2005/cupstream/server/internal/store"
)

type fixture struct {
	db    *store.DB
	s     *Service
	clock time.Time
	room  store.Room
}

func newFixture(t *testing.T) *fixture {
	t.Helper()
	db, err := store.Open(context.Background(), ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	f := &fixture{db: db, clock: time.Unix(1_800_000_000, 0)}
	f.s = New(db)
	f.s.now = func() time.Time { return f.clock }
	owner := f.user(t, "alice")
	f.room, err = db.GetLinkRoomByOwner(context.Background(), sql.NullString{String: owner.ID, Valid: true})
	if err != nil {
		t.Fatal(err)
	}
	return f
}

func (f *fixture) user(t *testing.T, name string) store.User {
	t.Helper()
	u, _, err := auth.NewAccounts(f.db, nil).SignIn(context.Background(), oauth.Identity{Provider: "dev", Subject: name, Username: name})
	if err != nil {
		t.Fatal(err)
	}
	return u
}

func (f *fixture) invite(t *testing.T, req NewInvite) (store.Invite, string) {
	t.Helper()
	inv, token, err := f.s.CreateInvite(context.Background(), f.room, f.room.OwnerID.String, req)
	if err != nil {
		t.Fatal(err)
	}
	return inv, token
}

func (f *fixture) can(t *testing.T, userID string, action access.Action) bool {
	t.Helper()
	r, err := f.s.Check(context.Background(), f.room, userID, action)
	if err != nil {
		t.Fatal(err)
	}
	return r == access.OK
}

func (f *fixture) uses(t *testing.T, id string) int64 {
	t.Helper()
	var n int64
	if err := f.db.SQL.QueryRow("SELECT uses FROM invites WHERE id = ?", id).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

func TestInviteExpires(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	bob := f.user(t, "bob")
	_, token := f.invite(t, NewInvite{Expiry: ExpiryHour})
	if _, err := f.s.Redeem(ctx, token, bob.ID); err != nil {
		t.Fatal(err)
	}
	if !f.can(t, bob.ID, access.Watch) || f.can(t, bob.ID, access.Stream) {
		t.Fatal("bob's pass is wrong")
	}

	// The pass lasts as long as the link.
	f.clock = f.clock.Add(time.Hour)
	if f.can(t, bob.ID, access.Watch) {
		t.Fatal("bob can watch after the link expired")
	}
	if _, err := f.s.Redeem(ctx, token, bob.ID); !errors.Is(err, ErrInviteInvalid) {
		t.Fatalf("redeeming an expired link: %v", err)
	}
	if invites, _ := f.s.ActiveInvites(ctx, f.room); len(invites) != 0 {
		t.Fatalf("expired link still listed: %+v", invites)
	}
}

func TestInviteNeverTakesAccessAway(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	bob := f.user(t, "bob")
	never, neverToken := f.invite(t, NewInvite{Expiry: ExpiryNever, CanStream: true})
	day, dayToken := f.invite(t, NewInvite{Expiry: ExpiryDay})
	if _, err := f.s.Redeem(ctx, neverToken, bob.ID); err != nil {
		t.Fatal(err)
	}
	// A shorter, watch-only link changes nothing and uses nothing.
	if _, err := f.s.Redeem(ctx, dayToken, bob.ID); err != nil {
		t.Fatal(err)
	}
	if f.uses(t, day.ID) != 0 || !f.can(t, bob.ID, access.Stream) {
		t.Fatal("a weaker link replaced bob's pass")
	}
	if f.uses(t, never.ID) != 1 {
		t.Fatal("the first link was not counted")
	}

	// Someone on the allowed list uses nothing either.
	carol := f.user(t, "carol")
	if err := f.s.SetAllowed(ctx, f.room, carol.ID, false); err != nil {
		t.Fatal(err)
	}
	if _, err := f.s.Redeem(ctx, dayToken, carol.ID); err != nil || f.uses(t, day.ID) != 0 {
		t.Fatalf("allowed user: err %v, uses %d", err, f.uses(t, day.ID))
	}
	// But a streaming link gives an allowed viewer a streaming pass.
	if _, err := f.s.Redeem(ctx, neverToken, carol.ID); err != nil || !f.can(t, carol.ID, access.Stream) {
		t.Fatalf("allowed viewer with a streaming link: err %v", err)
	}
}

func TestEndStreamInvites(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	bob := f.user(t, "bob")
	_, token := f.invite(t, NewInvite{Expiry: ExpiryStreamEnd})
	_, keep := f.invite(t, NewInvite{Expiry: ExpiryNever})
	carol := f.user(t, "carol")
	for _, r := range []struct{ token, user string }{{token, bob.ID}, {keep, carol.ID}} {
		if _, err := f.s.Redeem(ctx, r.token, r.user); err != nil {
			t.Fatal(err)
		}
	}
	ended, err := f.s.EndStreamInvites(ctx, f.room.ID)
	if err != nil || !ended {
		t.Fatalf("EndStreamInvites = %v, %v", ended, err)
	}
	if f.can(t, bob.ID, access.Watch) || !f.can(t, carol.ID, access.Watch) {
		t.Fatal("only the stream-end link should have ended")
	}
	if ended, _ := f.s.EndStreamInvites(ctx, f.room.ID); ended {
		t.Fatal("ended twice")
	}
}

func TestRedeemTwiceAtOnce(t *testing.T) {
	f := newFixture(t)
	bob := f.user(t, "bob")
	inv, token := f.invite(t, NewInvite{Expiry: ExpiryNever, MaxUses: 1})
	errs := make(chan error, 2)
	for range 2 {
		go func() {
			_, err := f.s.Redeem(context.Background(), token, bob.ID)
			errs <- err
		}()
	}
	for range 2 {
		if err := <-errs; err != nil {
			t.Fatalf("redeeming twice at once: %v", err)
		}
	}
	if n := f.uses(t, inv.ID); n != 1 {
		t.Fatalf("uses = %d, want 1", n)
	}
}
