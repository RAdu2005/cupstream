package friends

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/RAdu2005/cupstream/server/internal/store"
)

func newService(t *testing.T) (*Service, *store.DB) {
	t.Helper()
	db, err := store.Open(context.Background(), ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	return New(db), db
}

func user(t *testing.T, db *store.DB, handle string) string {
	t.Helper()
	u, err := db.CreateUser(context.Background(), store.CreateUserParams{ID: "id-" + handle, Handle: handle, DisplayName: handle})
	if err != nil {
		t.Fatal(err)
	}
	return u.ID
}

// manyUsers creates n users with IDs prefix-0 … prefix-(n-1).
func manyUsers(t *testing.T, db *store.DB, prefix string, n int) {
	t.Helper()
	exec(t, db, `WITH RECURSIVE seq (i) AS (SELECT 0 UNION ALL SELECT i + 1 FROM seq WHERE i + 1 < ?2)
		INSERT INTO users (id, handle, display_name, created_at) SELECT ?1 || '-' || i, ?1 || i, 'x', 0 FROM seq`, prefix, n)
}

func exec(t *testing.T, db *store.DB, query string, args ...any) {
	t.Helper()
	if _, err := db.SQL.Exec(query, args...); err != nil {
		t.Fatal(err)
	}
}

func count(t *testing.T, db *store.DB, query string, args ...any) int {
	t.Helper()
	var n int
	if err := db.SQL.QueryRow(query, args...).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

func relationOf(t *testing.T, s *Service, a, b string) Relation {
	t.Helper()
	rel, _, err := relation(context.Background(), s.db.Queries, a, b)
	if err != nil {
		t.Fatal(err)
	}
	return rel
}

func TestRequests(t *testing.T) {
	ctx := context.Background()
	s, db := newService(t)
	ana, bob, cy := user(t, db, "ana"), user(t, db, "bob"), user(t, db, "cy")

	if _, _, _, err := s.Request(ctx, ana, ana); !errors.Is(err, ErrSelf) {
		t.Fatalf("befriending yourself: %v", err)
	}
	if _, _, _, err := s.Request(ctx, ana, "nobody"); !errors.Is(err, ErrNoUser) {
		t.Fatalf("unknown user: %v", err)
	}

	// Ana asks Bob; asking again changes nothing.
	if _, rel, changed, err := s.Request(ctx, ana, bob); err != nil || rel != Outgoing || !changed {
		t.Fatalf("request = %v %v %v", rel, changed, err)
	}
	if _, rel, changed, err := s.Request(ctx, ana, bob); err != nil || rel != Outgoing || changed {
		t.Fatalf("second request = %v %v %v", rel, changed, err)
	}
	if got := relationOf(t, s, bob, ana); got != Incoming {
		t.Fatalf("bob sees %v", got)
	}
	// Only the person asked can accept.
	if _, err := s.Accept(ctx, ana, bob); !errors.Is(err, ErrNoRequest) {
		t.Fatalf("requester accepting: %v", err)
	}
	if _, err := s.Accept(ctx, bob, ana); err != nil {
		t.Fatal(err)
	}
	if got := relationOf(t, s, ana, bob); got != Friends {
		t.Fatalf("after accepting: %v", got)
	}
	if _, err := s.RemoveRequest(ctx, ana, bob); !errors.Is(err, ErrNoRequest) {
		t.Fatalf("cancelling an accepted request: %v", err)
	}

	// Asking someone who already asked you accepts their request.
	s.Request(ctx, cy, ana)
	if _, rel, changed, err := s.Request(ctx, ana, cy); err != nil || rel != Friends || !changed {
		t.Fatalf("mutual request = %v %v %v", rel, changed, err)
	}

	list, err := s.List(ctx, ana)
	if err != nil || len(list.Friends) != 2 || len(list.Incoming)+len(list.Outgoing) != 0 {
		t.Fatalf("ana's list: %+v %v", list, err)
	}
	if ids, _ := s.FriendIDs(ctx, ana); len(ids) != 2 {
		t.Fatalf("friend IDs: %v", ids)
	}

	// Unfriending, then declining and cancelling.
	if err := s.Unfriend(ctx, bob, ana); err != nil {
		t.Fatal(err)
	}
	if err := s.Unfriend(ctx, bob, ana); !errors.Is(err, ErrNotFriends) {
		t.Fatalf("unfriending twice: %v", err)
	}
	s.Request(ctx, bob, ana)
	if rel, err := s.RemoveRequest(ctx, ana, bob); err != nil || rel != Incoming {
		t.Fatalf("declining = %v %v", rel, err)
	}
	s.Request(ctx, bob, ana)
	if rel, err := s.RemoveRequest(ctx, bob, ana); err != nil || rel != Outgoing {
		t.Fatalf("cancelling = %v %v", rel, err)
	}
	if err := s.Unfriend(ctx, cy, ana); err != nil {
		t.Fatal(err)
	}
	if list, _ := s.List(ctx, ana); len(list.Friends)+len(list.Incoming)+len(list.Outgoing) != 0 {
		t.Fatalf("ana's list at the end: %+v", list)
	}
}

func TestBlocks(t *testing.T) {
	ctx := context.Background()
	s, db := newService(t)
	ana, bob := user(t, db, "ana"), user(t, db, "bob")

	s.Request(ctx, ana, bob)
	s.Accept(ctx, bob, ana)
	if _, ended, err := s.Block(ctx, ana, bob); err != nil || !ended {
		t.Fatalf("block = %v %v", ended, err)
	}
	if got := relationOf(t, s, ana, bob); got != Blocked {
		t.Fatalf("after blocking: %v", got)
	}
	// Bob can no longer find or ask Ana; Ana must unblock first.
	if _, _, _, err := s.Request(ctx, bob, ana); !errors.Is(err, ErrNoUser) {
		t.Fatalf("blocked person asking: %v", err)
	}
	if _, _, err := s.Lookup(ctx, bob, "ana"); !errors.Is(err, ErrNoUser) {
		t.Fatalf("blocked person looking up: %v", err)
	}
	if _, _, _, err := s.Request(ctx, ana, bob); !errors.Is(err, ErrYouBlocked) {
		t.Fatalf("asking someone you blocked: %v", err)
	}
	if _, rel, err := s.Lookup(ctx, ana, "@BOB "); err != nil || rel != Blocked {
		t.Fatalf("looking up someone you blocked: %v %v", rel, err)
	}
	// Bob can block Ana back.
	if _, ended, err := s.Block(ctx, bob, ana); err != nil || ended {
		t.Fatalf("blocking back = %v %v", ended, err)
	}
	if b, _ := s.Blocked(ctx, ana); len(b) != 1 || b[0].ID != bob {
		t.Fatalf("ana's blocked list: %+v", b)
	}
	if ok, err := s.Unblock(ctx, ana, bob); !ok || err != nil {
		t.Fatalf("unblock = %v %v", ok, err)
	}
	if ok, _ := s.Unblock(ctx, ana, bob); ok {
		t.Fatal("unblocked twice")
	}
	if _, _, err := s.Block(ctx, ana, "nobody"); !errors.Is(err, ErrNoUser) {
		t.Fatalf("blocking an unknown user: %v", err)
	}
}

func TestLimits(t *testing.T) {
	ctx := context.Background()
	s, db := newService(t)
	now := time.Unix(1_800_000_000, 0)
	s.now = func() time.Time { return now }
	ana := user(t, db, "ana")

	// Requests are rate limited per sender.
	var others []string
	for i := range requestBurst + 1 {
		others = append(others, user(t, db, fmt.Sprintf("u%d", i)))
	}
	for _, o := range others[:requestBurst] {
		if _, _, _, err := s.Request(ctx, ana, o); err != nil {
			t.Fatal(err)
		}
	}
	if _, _, _, err := s.Request(ctx, ana, others[requestBurst]); !errors.Is(err, ErrRateLimited) {
		t.Fatalf("over the burst: %v", err)
	}
	now = now.Add(requestRefill)
	if _, _, _, err := s.Request(ctx, ana, others[requestBurst]); err != nil {
		t.Fatalf("after the refill: %v", err)
	}

	// Unanswered requests, sent and received, are limited.
	full := user(t, db, "full")
	manyUsers(t, db, "in", MaxIncoming)
	exec(t, db, `INSERT INTO friendships (user_a, user_b, status, requested_by, created_at)
		SELECT min(id, ?1), max(id, ?1), 'pending', id, 0 FROM users WHERE id LIKE 'in-%'`, full)
	var limit *LimitError
	if _, _, _, err := s.Request(ctx, ana, full); !errors.As(err, &limit) {
		t.Fatalf("full inbox: %v", err)
	}
	s.sends = newLimiter(1000, time.Second)
	for i := range MaxOutgoing - requestBurst - 1 {
		if _, _, _, err := s.Request(ctx, ana, fmt.Sprintf("in-%d", i)); err != nil {
			t.Fatal(err)
		}
	}
	if _, _, _, err := s.Request(ctx, ana, user(t, db, "one_more")); !errors.As(err, &limit) {
		t.Fatalf("too many outgoing: %v", err)
	}
}

// Accepting is refused when either side has the most friends allowed.
func TestFriendLimit(t *testing.T) {
	ctx := context.Background()
	s, db := newService(t)
	ana, bob := user(t, db, "ana"), user(t, db, "bob")
	s.Request(ctx, bob, ana)
	manyUsers(t, db, "f", MaxFriends)
	exec(t, db, `INSERT INTO friendships (user_a, user_b, status, requested_by, created_at)
		SELECT min(id, ?1), max(id, ?1), 'accepted', id, 0 FROM users WHERE id LIKE 'f-%'`, ana)
	var limit *LimitError
	if _, err := s.Accept(ctx, ana, bob); !errors.As(err, &limit) || limit != errMyFriends {
		t.Fatalf("accept over the limit: %v", err)
	}
	if _, _, _, err := s.Request(ctx, user(t, db, "cy"), ana); !errors.As(err, &limit) || limit != errTheirFriends {
		t.Fatalf("asking someone at the limit: %v", err)
	}
}

func TestMerge(t *testing.T) {
	ctx := context.Background()
	s, db := newService(t)
	keep, gone := user(t, db, "keep"), user(t, db, "gone")
	friend, asked, asker, both, mutual, blocker, blockee := user(t, db, "friend"), user(t, db, "asked"), user(t, db, "asker"),
		user(t, db, "both"), user(t, db, "mutual"), user(t, db, "blocker"), user(t, db, "blockee")

	set := func(x, y, status, by string, at int64) {
		a, b := pair(x, y)
		f := store.UpsertFriendshipParams{UserA: a, UserB: b, Status: status, RequestedBy: by, CreatedAt: at}
		if status == statusAccepted {
			f.AcceptedAt.Int64, f.AcceptedAt.Valid = at, true
		}
		if err := db.UpsertFriendship(ctx, f); err != nil {
			t.Fatal(err)
		}
	}
	set(gone, keep, statusPending, gone, 1) // the two accounts themselves
	set(gone, friend, statusAccepted, gone, 2)
	set(gone, asked, statusPending, gone, 3)
	set(asker, gone, statusPending, asker, 4)
	set(keep, both, statusPending, keep, 5) // keep asked, gone was already friends
	set(gone, both, statusAccepted, both, 6)
	set(keep, mutual, statusPending, keep, 7) // each account was asked by or asked mutual
	set(mutual, gone, statusPending, mutual, 8)
	set(keep, blockee, statusAccepted, keep, 9) // gone had blocked blockee
	db.BlockUser(ctx, store.BlockUserParams{UserID: gone, BlockedID: blockee})
	db.BlockUser(ctx, store.BlockUserParams{UserID: blocker, BlockedID: gone})
	set(gone, blocker, statusPending, gone, 10)
	db.DismissSuggestion(ctx, store.DismissSuggestionParams{UserID: gone, OtherID: asked})

	err := db.Tx(ctx, func(q *store.Queries) error {
		if err := Merge(ctx, q, gone, keep); err != nil {
			return err
		}
		return q.DeleteUser(ctx, gone)
	})
	if err != nil {
		t.Fatal(err)
	}

	for other, want := range map[string]Relation{
		friend: Friends, asked: Outgoing, asker: Incoming, both: Friends, mutual: Friends, blockee: Blocked, blocker: None,
	} {
		if got := relationOf(t, s, keep, other); got != want {
			t.Errorf("keep and %s: %v, want %v", other, got, want)
		}
	}
	if got := relationOf(t, s, blocker, keep); got != Blocked {
		t.Errorf("blocker's block did not carry over: %v", got)
	}
	if _, _, err := s.Lookup(ctx, blocker, "keep"); err != nil {
		t.Errorf("blocker looking up keep: %v", err) // blocker blocked keep, not the reverse
	}
	if _, _, err := s.Lookup(ctx, keep, "blocker"); !errors.Is(err, ErrNoUser) {
		t.Errorf("keep looking up blocker: %v", err)
	}
	if n := count(t, db, `SELECT COUNT(*) FROM friend_dismissals WHERE user_id = ? AND other_id = ?`, keep, asked); n != 1 {
		t.Errorf("dismissal not carried over")
	}
	a, b := pair(keep, both)
	if f, _ := db.GetFriendship(ctx, store.GetFriendshipParams{UserA: a, UserB: b}); f.AcceptedAt.Int64 != 6 || f.RequestedBy != both {
		t.Errorf("keep and both: %+v", f)
	}
}

// Deleting an account deletes its friendships and blocks.
func TestDeleteUser(t *testing.T) {
	ctx := context.Background()
	s, db := newService(t)
	ana, bob, cy := user(t, db, "ana"), user(t, db, "bob"), user(t, db, "cy")
	s.Request(ctx, ana, bob)
	s.Block(ctx, cy, ana)
	if err := db.DeleteUser(ctx, ana); err != nil {
		t.Fatal(err)
	}
	if l, _ := s.List(ctx, bob); len(l.Incoming) != 0 {
		t.Fatalf("bob's requests: %+v", l)
	}
	if b, _ := s.Blocked(ctx, cy); len(b) != 0 {
		t.Fatalf("cy's blocks: %+v", b)
	}
}

func TestSuggestions(t *testing.T) {
	ctx := context.Background()
	s, db := newService(t)
	people := map[string]string{}
	for _, h := range []string{"ana", "bob", "cy", "dan", "eve", "fay", "gus", "hal", "ivy", "jon", "kim"} {
		people[h] = user(t, db, h)
		if h != "dan" { // dan has no Discord account linked
			if err := db.CreateIdentity(ctx, store.CreateIdentityParams{Provider: "discord", Subject: h, UserID: people[h], Username: h}); err != nil {
				t.Fatal(err)
			}
		}
	}
	member := func(h, guild string, on bool) {
		if err := db.UpsertUserGuild(ctx, store.UpsertUserGuildParams{UserID: people[h], GuildID: guild, Name: "Server " + guild, SuggestionsEnabled: on}); err != nil {
			t.Fatal(err)
		}
	}
	for _, h := range []string{"ana", "bob", "cy", "dan", "gus", "hal", "ivy", "jon", "kim"} {
		member(h, "1", true)
	}
	member("ana", "2", true)
	member("bob", "2", true)
	member("ana", "big", false) // ana turned this server off (or it is large)
	member("eve", "big", true)
	member("ana", "3", true)
	member("fay", "3", false) // fay turned it off on her side

	ana := people["ana"]
	s.Request(ctx, ana, people["gus"])
	s.Accept(ctx, people["gus"], ana)
	s.Request(ctx, people["hal"], ana)
	s.Block(ctx, people["ivy"], ana)
	s.Block(ctx, ana, people["jon"])
	s.Request(ctx, people["kim"], ana)
	s.RemoveRequest(ctx, ana, people["kim"]) // declined

	got := func() string {
		t.Helper()
		list, err := s.Suggestions(ctx, ana)
		if err != nil {
			t.Fatal(err)
		}
		var out []string
		for _, sg := range list {
			var gs []string
			for _, g := range sg.Guilds {
				gs = append(gs, g.ID)
			}
			out = append(out, fmt.Sprintf("%s%v", sg.User.Handle, gs))
		}
		return fmt.Sprint(out)
	}
	if g := got(); g != "[bob[1 2] cy[1]]" {
		t.Fatalf("suggestions = %s", g)
	}

	if err := s.Dismiss(ctx, ana, people["cy"]); err != nil {
		t.Fatal(err)
	}
	if err := s.Dismiss(ctx, ana, "nobody"); !errors.Is(err, ErrNoUser) {
		t.Fatalf("dismissing an unknown user: %v", err)
	}
	if g := got(); g != "[bob[1 2]]" {
		t.Fatalf("after dismissing cy: %s", g)
	}
	// Turning server 1 off leaves server 2; unlinking Discord (which deletes
	// the server list) leaves nothing.
	db.SetGuildSuggestions(ctx, store.SetGuildSuggestionsParams{SuggestionsEnabled: false, UserID: ana, GuildID: "1"})
	if g := got(); g != "[bob[2]]" {
		t.Fatalf("after turning server 1 off: %s", g)
	}
	db.DeleteUserGuilds(ctx, people["bob"])
	if g := got(); g != "[]" {
		t.Fatalf("after bob unlinked Discord: %s", g)
	}
}
