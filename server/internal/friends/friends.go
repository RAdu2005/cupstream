// Package friends manages friend requests, friendships, blocks and
// suggestions.
//
// A friendship is a request that the other person accepts: without
// acceptance, anyone could add someone and see when they go live. Each pair
// of users has at most one row in friendships, pending or accepted. Blocking
// someone ends any friendship or request with them; they can then neither
// find the user by handle nor send them requests.
//
// Suggestions are CupStream users with a linked Discord account who share a
// Discord server with the user, among the servers both left on for
// suggestions (very large ones start off, see auth.LargeGuildMembers).
package friends

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/RAdu2005/cupstream/server/internal/store"
)

const (
	// MaxFriends is how many friends one user may have.
	MaxFriends = 1000
	// MaxOutgoing is how many unanswered requests one user may have sent.
	MaxOutgoing = 100
	// MaxIncoming is how many unanswered requests one user may receive.
	MaxIncoming = 500

	// MaxSuggestions is how many suggestions are listed at once.
	MaxSuggestions = 50

	// Each user may send requestBurst requests at once, then one per
	// requestRefill: every request notifies its recipient, so sending and
	// cancelling in a loop must not flood them.
	requestBurst  = 20
	requestRefill = time.Minute
)

type Relation string

const (
	None     Relation = "none"
	Friends  Relation = "friends"
	Outgoing Relation = "outgoing" // the user asked
	Incoming Relation = "incoming" // the other person asked
	Blocked  Relation = "blocked"  // the user blocked the other person
)

const (
	statusPending  = "pending"
	statusAccepted = "accepted"
)

var (
	ErrSelf        = errors.New("you cannot be your own friend")
	ErrNoUser      = errors.New("no user has this handle")
	ErrNoRequest   = errors.New("there is no such friend request")
	ErrNotFriends  = errors.New("you are not friends")
	ErrYouBlocked  = errors.New("you blocked this person; unblock them first")
	ErrRateLimited = errors.New("you are sending friend requests too quickly; try again in a minute")
)

// LimitError is returned when a request or friendship would exceed a limit.
type LimitError struct{ msg string }

func (e *LimitError) Error() string { return e.msg }

var (
	errMyFriends    = &LimitError{fmt.Sprintf("you can have at most %d friends", MaxFriends)}
	errTheirFriends = &LimitError{"this person has reached the limit of friends"}
	errMyOutgoing   = &LimitError{fmt.Sprintf("you have %d unanswered friend requests; cancel some first", MaxOutgoing)}
	errTheirInbox   = &LimitError{"this person cannot receive more friend requests right now"}
)

type Service struct {
	db    *store.DB
	now   func() time.Time
	sends *limiter
}

func New(db *store.DB) *Service {
	return &Service{db: db, now: time.Now, sends: newLimiter(requestBurst, requestRefill)}
}

// pair orders two user IDs the way friendships stores them.
func pair(a, b string) (string, string) {
	if a < b {
		return a, b
	}
	return b, a
}

func getFriendship(ctx context.Context, q *store.Queries, a, b string) (store.Friendship, bool, error) {
	ua, ub := pair(a, b)
	f, err := q.GetFriendship(ctx, store.GetFriendshipParams{UserA: ua, UserB: ub})
	if errors.Is(err, sql.ErrNoRows) {
		return f, false, nil
	}
	return f, err == nil, err
}

// relation is how userID stands with otherID, and whether otherID blocked
// userID.
func relation(ctx context.Context, q *store.Queries, userID, otherID string) (rel Relation, blockedByThem bool, err error) {
	if blockedByThem, err = q.IsBlocked(ctx, store.IsBlockedParams{UserID: otherID, BlockedID: userID}); err != nil {
		return None, false, err
	}
	blocked, err := q.IsBlocked(ctx, store.IsBlockedParams{UserID: userID, BlockedID: otherID})
	if err != nil || blocked {
		return Blocked, blockedByThem, err
	}
	f, ok, err := getFriendship(ctx, q, userID, otherID)
	switch {
	case err != nil || !ok:
		return None, blockedByThem, err
	case f.Status == statusAccepted:
		return Friends, blockedByThem, nil
	case f.RequestedBy == userID:
		return Outgoing, blockedByThem, nil
	}
	return Incoming, blockedByThem, nil
}

// otherUser loads the person a request is about. Someone who blocked the
// user does not exist as far as the user can tell.
func otherUser(ctx context.Context, q *store.Queries, userID, otherID string) (store.User, Relation, error) {
	if otherID == userID {
		return store.User{}, None, ErrSelf
	}
	u, err := q.GetUser(ctx, otherID)
	if errors.Is(err, sql.ErrNoRows) {
		return u, None, ErrNoUser
	}
	if err != nil {
		return u, None, err
	}
	rel, blockedByThem, err := relation(ctx, q, userID, otherID)
	if err == nil && blockedByThem {
		err = ErrNoUser
	}
	return u, rel, err
}

// Lookup finds someone by their exact handle. Handles are only found
// exactly, so the list of users cannot be browsed.
func (s *Service) Lookup(ctx context.Context, userID, handle string) (store.User, Relation, error) {
	u, err := s.db.GetUserByHandle(ctx, strings.TrimPrefix(strings.TrimSpace(handle), "@"))
	if errors.Is(err, sql.ErrNoRows) {
		return u, None, ErrNoUser
	}
	if err != nil {
		return u, None, err
	}
	_, rel, err := otherUser(ctx, s.db.Queries, userID, u.ID)
	return u, rel, err
}

// checkCanBefriend fails if a new friendship would exceed either user's
// limit.
func checkCanBefriend(ctx context.Context, q *store.Queries, userID, otherID string) error {
	mine, err := q.CountFriendships(ctx, userID)
	if err != nil {
		return err
	}
	if mine.Friends >= MaxFriends {
		return errMyFriends
	}
	theirs, err := q.CountFriendships(ctx, otherID)
	if err != nil {
		return err
	}
	if theirs.Friends >= MaxFriends {
		return errTheirFriends
	}
	return nil
}

// Request sends otherID a friend request from userID, or accepts theirs if
// they already asked. It returns the relation afterwards, Outgoing or
// Friends, and whether anything changed: asking again is not an error.
func (s *Service) Request(ctx context.Context, userID, otherID string) (store.User, Relation, bool, error) {
	var u store.User
	var rel Relation
	changed := false
	err := s.db.Tx(ctx, func(q *store.Queries) error {
		var err error
		if u, rel, err = otherUser(ctx, q, userID, otherID); err != nil {
			return err
		}
		switch rel {
		case Blocked:
			return ErrYouBlocked
		case Friends, Outgoing:
			return nil
		case Incoming:
			rel, changed = Friends, true
			return accept(ctx, q, userID, otherID, s.now())
		}

		mine, err := q.CountFriendships(ctx, userID)
		if err != nil {
			return err
		}
		theirs, err := q.CountFriendships(ctx, otherID)
		switch {
		case err != nil:
			return err
		case mine.Outgoing >= MaxOutgoing:
			return errMyOutgoing
		case theirs.Incoming >= MaxIncoming:
			return errTheirInbox
		}
		if err := checkCanBefriend(ctx, q, userID, otherID); err != nil {
			return err
		}
		if !s.sends.allow(userID, s.now()) {
			return ErrRateLimited
		}
		a, b := pair(userID, otherID)
		rel, changed = Outgoing, true
		return q.CreateFriendRequest(ctx, store.CreateFriendRequestParams{UserA: a, UserB: b, RequestedBy: userID, CreatedAt: s.now().Unix()})
	})
	return u, rel, changed, err
}

func accept(ctx context.Context, q *store.Queries, userID, otherID string, now time.Time) error {
	if err := checkCanBefriend(ctx, q, userID, otherID); err != nil {
		return err
	}
	a, b := pair(userID, otherID)
	return q.AcceptFriendRequest(ctx, store.AcceptFriendRequestParams{AcceptedAt: sql.NullInt64{Int64: now.Unix(), Valid: true}, UserA: a, UserB: b})
}

// Accept accepts otherID's request to userID.
func (s *Service) Accept(ctx context.Context, userID, otherID string) (store.User, error) {
	var u store.User
	err := s.db.Tx(ctx, func(q *store.Queries) error {
		var rel Relation
		var err error
		if u, rel, err = otherUser(ctx, q, userID, otherID); err != nil {
			return err
		}
		if rel != Incoming {
			return ErrNoRequest
		}
		return accept(ctx, q, userID, otherID, s.now())
	})
	return u, err
}

// RemoveRequest declines otherID's request to userID or cancels userID's
// request to otherID, and returns which it was (Incoming or Outgoing).
func (s *Service) RemoveRequest(ctx context.Context, userID, otherID string) (Relation, error) {
	return s.remove(ctx, userID, otherID, statusPending, ErrNoRequest)
}

// Unfriend ends a friendship.
func (s *Service) Unfriend(ctx context.Context, userID, otherID string) error {
	_, err := s.remove(ctx, userID, otherID, statusAccepted, ErrNotFriends)
	return err
}

func (s *Service) remove(ctx context.Context, userID, otherID, status string, missing error) (Relation, error) {
	var rel Relation
	err := s.db.Tx(ctx, func(q *store.Queries) error {
		f, ok, err := getFriendship(ctx, q, userID, otherID)
		switch {
		case err != nil:
			return err
		case !ok || f.Status != status:
			return missing
		case status == statusAccepted:
			rel = Friends
		case f.RequestedBy == userID:
			rel = Outgoing
		default:
			rel = Incoming
		}
		if err := q.DeleteFriendship(ctx, store.DeleteFriendshipParams{UserA: f.UserA, UserB: f.UserB}); err != nil {
			return err
		}
		if rel == Incoming {
			// Declined: do not suggest them again.
			return q.DismissSuggestion(ctx, store.DismissSuggestionParams{UserID: userID, OtherID: otherID, CreatedAt: s.now().Unix()})
		}
		return nil
	})
	return rel, err
}

// Block blocks otherID for userID, ending any friendship or request between
// them. It reports whether there was one.
func (s *Service) Block(ctx context.Context, userID, otherID string) (store.User, bool, error) {
	if otherID == userID {
		return store.User{}, false, ErrSelf
	}
	var u store.User
	var ended bool
	err := s.db.Tx(ctx, func(q *store.Queries) error {
		var err error
		u, err = q.GetUser(ctx, otherID)
		if errors.Is(err, sql.ErrNoRows) {
			return ErrNoUser
		}
		if err != nil {
			return err
		}
		f, ok, err := getFriendship(ctx, q, userID, otherID)
		if err != nil {
			return err
		}
		if ok {
			ended = true
			if err := q.DeleteFriendship(ctx, store.DeleteFriendshipParams{UserA: f.UserA, UserB: f.UserB}); err != nil {
				return err
			}
		}
		return q.BlockUser(ctx, store.BlockUserParams{UserID: userID, BlockedID: otherID, CreatedAt: s.now().Unix()})
	})
	return u, ended, err
}

// Unblock removes a block, and reports whether there was one.
func (s *Service) Unblock(ctx context.Context, userID, otherID string) (bool, error) {
	n, err := s.db.UnblockUser(ctx, store.UnblockUserParams{UserID: userID, BlockedID: otherID})
	return n > 0, err
}

// Entry is a friend (Since: when they became friends) or a pending request
// (Since: when it was sent).
type Entry struct {
	User  store.User
	Since int64
}

type List struct {
	Friends, Incoming, Outgoing []Entry
}

// List returns userID's friends and pending requests.
func (s *Service) List(ctx context.Context, userID string) (List, error) {
	var out List
	rows, err := s.db.ListFriendships(ctx, userID)
	if err != nil {
		return out, err
	}
	for _, row := range rows {
		switch {
		case row.Status == statusAccepted:
			out.Friends = append(out.Friends, Entry{row.User, row.AcceptedAt.Int64})
		case row.RequestedBy == userID:
			out.Outgoing = append(out.Outgoing, Entry{row.User, row.CreatedAt})
		default:
			out.Incoming = append(out.Incoming, Entry{row.User, row.CreatedAt})
		}
	}
	return out, nil
}

// Blocked lists the people userID blocked.
func (s *Service) Blocked(ctx context.Context, userID string) ([]store.User, error) {
	return s.db.ListBlocked(ctx, userID)
}

// Guild is a Discord server shared with a suggested person.
type Guild struct {
	ID, Name, IconURL string
}

type Suggestion struct {
	User   store.User
	Guilds []Guild // the shared servers, by name
}

// Suggestions lists people userID may know from their Discord servers, those
// sharing the most servers first.
func (s *Service) Suggestions(ctx context.Context, userID string) ([]Suggestion, error) {
	rows, err := s.db.ListSuggestions(ctx, store.ListSuggestionsParams{UserID: userID, Max: MaxSuggestions})
	if err != nil || len(rows) == 0 {
		return []Suggestion{}, err
	}
	ids := make([]string, len(rows))
	for i, row := range rows {
		ids[i] = row.User.ID
	}
	shared, err := s.db.ListSharedGuilds(ctx, store.ListSharedGuildsParams{UserID: userID, OtherIds: ids})
	if err != nil {
		return nil, err
	}
	guilds := map[string][]Guild{}
	for _, g := range shared {
		guilds[g.OtherID] = append(guilds[g.OtherID], Guild{ID: g.GuildID, Name: g.Name, IconURL: g.Icon})
	}
	out := make([]Suggestion, len(rows))
	for i, row := range rows {
		out[i] = Suggestion{User: row.User, Guilds: guilds[row.User.ID]}
	}
	return out, nil
}

// Dismiss stops suggesting otherID to userID.
func (s *Service) Dismiss(ctx context.Context, userID, otherID string) error {
	if otherID == userID {
		return ErrSelf
	}
	if _, err := s.db.GetUser(ctx, otherID); errors.Is(err, sql.ErrNoRows) {
		return ErrNoUser
	} else if err != nil {
		return err
	}
	return s.db.DismissSuggestion(ctx, store.DismissSuggestionParams{UserID: userID, OtherID: otherID, CreatedAt: s.now().Unix()})
}

// FriendIDs lists the IDs of userID's friends.
func (s *Service) FriendIDs(ctx context.Context, userID string) ([]string, error) {
	return s.db.ListFriendIDs(ctx, userID)
}

// Merge carries the friendships, requests, blocks and dismissed suggestions
// of from (an account being merged away) over to to, inside the merge's
// transaction. Where both accounts had a relation with the same person, a
// friendship wins over a request, and requests in both directions become a
// friendship. Friendships with someone either side blocked are dropped.
func Merge(ctx context.Context, q *store.Queries, from, to string) error {
	rows, err := q.ListUserFriendships(ctx, from)
	if err != nil {
		return err
	}
	for _, f := range rows {
		other := f.UserA
		if other == from {
			other = f.UserB
		}
		if other == to {
			continue // deleted with from
		}
		moved := f
		moved.UserA, moved.UserB = pair(to, other)
		if moved.RequestedBy == from {
			moved.RequestedBy = to
		}
		existing, ok, err := getFriendship(ctx, q, to, other)
		if err != nil {
			return err
		}
		if ok {
			moved = combine(existing, moved)
		}
		if err := q.UpsertFriendship(ctx, store.UpsertFriendshipParams(moved)); err != nil {
			return err
		}
	}
	if err := q.MoveBlocksBy(ctx, store.MoveBlocksByParams{ToUser: to, FromUser: from}); err != nil {
		return err
	}
	if err := q.MoveBlocksOf(ctx, store.MoveBlocksOfParams{ToUser: to, FromUser: from}); err != nil {
		return err
	}
	if err := q.MoveDismissals(ctx, store.MoveDismissalsParams{ToUser: to, FromUser: from}); err != nil {
		return err
	}
	return q.DeleteBlockedFriendships(ctx, to)
}

// combine merges two relations of the same pair.
func combine(x, y store.Friendship) store.Friendship {
	switch {
	case x.Status == statusAccepted && y.Status == statusAccepted:
		if y.AcceptedAt.Int64 < x.AcceptedAt.Int64 {
			return y
		}
		return x
	case x.Status == statusAccepted:
		return x
	case y.Status == statusAccepted:
		return y
	case x.RequestedBy != y.RequestedBy:
		// Each asked the other.
		x.Status = statusAccepted
		x.AcceptedAt = sql.NullInt64{Int64: max(x.CreatedAt, y.CreatedAt), Valid: true}
		x.CreatedAt = min(x.CreatedAt, y.CreatedAt)
	}
	return x
}

// limiter is a token bucket per user: burst actions at once, then one more
// every refill.
type limiter struct {
	burst  float64
	refill time.Duration

	mu      sync.Mutex
	buckets map[string]*bucket
}

type bucket struct {
	tokens float64
	at     time.Time
}

func newLimiter(burst int, refill time.Duration) *limiter {
	return &limiter{burst: float64(burst), refill: refill, buckets: map[string]*bucket{}}
}

// allow reports whether key may act now, and counts it if so.
func (l *limiter) allow(key string, now time.Time) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	fill := func(b *bucket) {
		if now.After(b.at) {
			b.tokens = min(l.burst, b.tokens+float64(now.Sub(b.at))/float64(l.refill))
			b.at = now
		}
	}
	if len(l.buckets) > 10_000 {
		for k, b := range l.buckets {
			if fill(b); b.tokens >= l.burst {
				delete(l.buckets, k)
			}
		}
	}
	b := l.buckets[key]
	if b == nil {
		b = &bucket{tokens: l.burst, at: now}
		l.buckets[key] = b
	}
	fill(b)
	if b.tokens < 1 {
		return false
	}
	b.tokens--
	return true
}
