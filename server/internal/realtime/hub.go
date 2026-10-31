// Package realtime is the WebSocket gateway: one connection per browser tab,
// which subscribes to rooms and receives their stream events, presence, chat
// and, for streamers, their viewers' statistics.
//
// Every subscription passes the same access check as watching (package
// access), and is checked again when a room's access rules change
// (Reevaluate) and every minute, for temporary access that expires. A
// subscriber who loses access is told so and unsubscribed.
package realtime

import (
	"cmp"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"log/slog"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/RAdu2005/cupstream/server/internal/access"
	"github.com/RAdu2005/cupstream/server/internal/api"
	"github.com/RAdu2005/cupstream/server/internal/config"
	"github.com/RAdu2005/cupstream/server/internal/gateway"
	"github.com/RAdu2005/cupstream/server/internal/ratelimit"
	"github.com/RAdu2005/cupstream/server/internal/rooms"
	"github.com/RAdu2005/cupstream/server/internal/store"
	"github.com/RAdu2005/cupstream/server/internal/view"
)

const (
	// Stream statistics (bitrates, viewers' reception) are sent this often.
	statsInterval = 2 * time.Second
	// Changes from the gateway are collected this long before they are sent.
	coalesceDelay = 50 * time.Millisecond
	// Subscriptions are checked again this often, for passes that expire.
	recheckInterval = time.Minute
	// Expired chat messages and idle rate limits are deleted this often.
	cleanupInterval = 5 * time.Minute
	// How long a tab may be without a connection before its media sessions
	// are closed; reconnecting within it keeps them.
	defaultTabGrace = 15 * time.Second
	// How long shutting down waits for connections to close.
	shutdownTimeout = 3 * time.Second
	// Connections one signed-in user may have open at once (one per tab),
	// and one client address, signed in or not; friends behind one home
	// connection share an address.
	defaultMaxConnsPerUser = 20
	defaultMaxConnsPerAddr = 50
)

type Hub struct {
	db    *store.DB
	rooms *rooms.Service
	gw    *gateway.Gateway
	cfg   *config.Config // PublicURL is read per request: the allowed origin
	now   func() time.Time

	// TabGrace is how long a browser tab may be without a connection before
	// the media sessions it opened are closed. Set it before serving.
	TabGrace time.Duration
	// MaxConnsPerUser and MaxConnsPerAddr cap the connections of one user
	// and one client address. Set them before serving.
	MaxConnsPerUser, MaxConnsPerAddr int

	chatLimit *ratelimit.Limiter

	mu     sync.Mutex
	closed bool
	conns  map[*conn]struct{}
	subs   map[string]*roomSubs // by room ID
	tabs   map[tabKey]*tabState
	apps   map[string][]*conn // running desktop apps by user ID
	wg     sync.WaitGroup     // connection handlers
	// Open connections (and ones being opened) by user and by address.
	perUser, perAddr map[string]int

	// Owned by Run: what friends were told about each user's stream, and
	// the streams already announced (friends.go).
	friendStreams map[string]friendStream
	announced     map[string]bool
}

// roomSubs is a room with subscribers, and what they were last sent.
type roomSubs struct {
	room     store.Room
	conns    map[*conn]struct{}
	streams  map[string]api.Stream // by stream ID
	presence api.Presence
}

func New(db *store.DB, rs *rooms.Service, gw *gateway.Gateway, cfg *config.Config) *Hub {
	return &Hub{
		db: db, rooms: rs, gw: gw, cfg: cfg, now: time.Now,
		TabGrace:        defaultTabGrace,
		MaxConnsPerUser: defaultMaxConnsPerUser,
		MaxConnsPerAddr: defaultMaxConnsPerAddr,
		chatLimit:       ratelimit.New(chatBurst, chatRefill),
		conns:           map[*conn]struct{}{},
		subs:            map[string]*roomSubs{},
		tabs:            map[tabKey]*tabState{},
		perUser:         map[string]int{},
		perAddr:         map[string]int{},
		apps:            map[string][]*conn{},
	}
}

// Run sends stream events and statistics, checks subscriptions and deletes
// expired chat until ctx is done, then closes every connection.
func (h *Hub) Run(ctx context.Context) {
	stats := time.NewTicker(statsInterval)
	defer stats.Stop()
	recheck := time.NewTicker(recheckInterval)
	defer recheck.Stop()
	cleanup := time.NewTicker(cleanupInterval)
	defer cleanup.Stop()
	h.cleanup(ctx)

	for {
		select {
		case <-ctx.Done():
			h.shutdown()
			return
		case <-h.gw.Changes():
			select {
			case <-ctx.Done():
			case <-time.After(coalesceDelay):
			}
			h.syncStreams(ctx, false)
			h.syncFriendStreams(ctx)
		case <-stats.C:
			h.syncStreams(ctx, true)
		case <-recheck.C:
			h.RecheckAll(ctx)
		case <-cleanup.C:
			h.cleanup(ctx)
		}
	}
}

func (h *Hub) cleanup(ctx context.Context) {
	h.deleteExpiredChat(ctx)
	h.chatLimit.Prune(h.now())
}

// shutdown closes every connection without closing their tabs' media
// sessions: the browsers reconnect to the next server process.
func (h *Hub) shutdown() {
	h.mu.Lock()
	h.closed = true
	for _, ts := range h.tabs {
		if ts.timer != nil {
			ts.timer.Stop()
		}
	}
	for c := range h.conns {
		c.kill(statusGoingAway, "the server is restarting")
	}
	h.mu.Unlock()

	done := make(chan struct{})
	go func() {
		h.wg.Wait()
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(shutdownTimeout):
	}
}

func marshal(v any) []byte {
	b, err := json.Marshal(v)
	if err != nil {
		panic(err) // only API types are sent
	}
	return b
}

func (h *Hub) broadcastLocked(rs *roomSubs, v any) {
	b := marshal(v)
	for c := range rs.conns {
		c.enqueue(b)
	}
}

// subscribe adds c to the room with this slug if c may watch there, and
// sends it the room's current state.
func (h *Hub) subscribe(ctx context.Context, c *conn, slug, ref string) {
	room, err := h.db.GetRoomBySlug(ctx, slug)
	if errors.Is(err, sql.ErrNoRows) {
		c.send(api.Refused{Type: "refused", Room: slug, Code: "not_found", Error: "this room does not exist"})
		return
	}
	if err != nil {
		h.internalError(c, slug, ref, err)
		return
	}
	reason, err := h.rooms.Check(ctx, room, c.userID, access.Watch)
	if err != nil {
		h.internalError(c, slug, ref, err)
		return
	}
	if reason != access.OK {
		c.send(api.Refused{Type: "refused", Room: slug, Code: string(reason), Error: "you cannot enter this room"})
		return
	}

	h.mu.Lock()
	defer h.mu.Unlock()
	if c.closed {
		return
	}
	if _, ok := c.subs[slug]; !ok && len(c.subs) >= maxRooms {
		c.send(api.RealtimeError{Type: "error", Room: slug, Ref: ref, Code: "invalid", Error: "too many rooms on one connection"})
		return
	}
	users := view.Users{DB: h.db}
	rs := h.subs[room.ID]
	if rs == nil {
		rs = &roomSubs{room: room, conns: map[*conn]struct{}{}, presence: api.Presence{Members: []api.User{}}}
		h.subs[room.ID] = rs
	}
	// Bring the other subscribers up to date first, so that the snapshot
	// below is what later events are relative to.
	h.syncRoomLocked(ctx, rs, &users)
	rs.conns[c] = struct{}{}
	c.subs[slug] = rs

	chat, err := h.chatHistory(ctx, room.ID)
	if err != nil {
		slog.ErrorContext(ctx, "loading chat failed", "room", room.ID, "err", err)
		chat = []api.ChatMessage{}
	}
	h.updatePresenceLocked(rs, c) // c gets it in the snapshot
	c.send(api.Subscribed{Type: "subscribed", Room: slug, Streams: sortedStreams(rs.streams), Presence: rs.presence, Chat: chat})
}

func (h *Hub) unsubscribe(c *conn, slug string) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if rs, ok := c.subs[slug]; ok {
		h.unsubscribeLocked(c, rs)
	}
}

func (h *Hub) unsubscribeLocked(c *conn, rs *roomSubs) {
	delete(c.subs, rs.room.Slug)
	delete(rs.conns, c)
	if len(rs.conns) == 0 {
		delete(h.subs, rs.room.ID)
		return
	}
	h.updatePresenceLocked(rs, nil)
}

// updatePresenceLocked sends the room's presence to its subscribers, except
// skip, if it changed.
func (h *Hub) updatePresenceLocked(rs *roomSubs, skip *conn) {
	p := api.Presence{Members: []api.User{}}
	seen := map[string]bool{}
	for c := range rs.conns {
		switch {
		case c.userID == "":
			p.Anonymous++
		case !seen[c.userID]:
			seen[c.userID] = true
			p.Members = append(p.Members, c.user)
		}
	}
	slices.SortFunc(p.Members, func(a, b api.User) int {
		return cmp.Or(cmp.Compare(strings.ToLower(a.DisplayName), strings.ToLower(b.DisplayName)), cmp.Compare(a.Handle, b.Handle))
	})
	if p.Anonymous == rs.presence.Anonymous && slices.Equal(p.Members, rs.presence.Members) {
		return
	}
	rs.presence = p
	b := marshal(api.PresenceEvent{Type: "presence", Room: rs.room.Slug, Presence: p})
	for c := range rs.conns {
		if c != skip {
			c.enqueue(b)
		}
	}
}

func sortedStreams(m map[string]api.Stream) []api.Stream {
	out := make([]api.Stream, 0, len(m))
	for _, s := range m {
		out = append(out, s)
	}
	slices.SortFunc(out, func(a, b api.Stream) int {
		return cmp.Or(cmp.Compare(a.StartedAt, b.StartedAt), cmp.Compare(a.ID, b.ID))
	})
	return out
}

// syncStreams sends every subscribed room's stream changes and, with
// withStats, each streamer their viewers' statistics.
func (h *Hub) syncStreams(ctx context.Context, withStats bool) {
	h.mu.Lock()
	defer h.mu.Unlock()
	users := view.Users{DB: h.db}
	for _, rs := range h.subs {
		h.syncRoomLocked(ctx, rs, &users)
		if withStats {
			h.sendViewerStatsLocked(rs)
		}
	}
}

// syncRoomLocked compares the room's streams with what its subscribers were
// last sent, and sends the differences.
func (h *Hub) syncRoomLocked(ctx context.Context, rs *roomSubs, users *view.Users) {
	next := map[string]api.Stream{}
	for _, ls := range h.gw.RoomStreams(rs.room.ID) {
		st, err := view.Stream(ctx, users, ls)
		if err != nil {
			slog.ErrorContext(ctx, "describing a stream failed", "stream", ls.Stream.ID, "err", err)
			if prev, ok := rs.streams[ls.Stream.ID]; ok {
				next[prev.ID] = prev
			}
			continue
		}
		next[st.ID] = st
		if prev, ok := rs.streams[st.ID]; !ok || prev != st {
			h.broadcastLocked(rs, api.StreamEvent{Type: "stream", Room: rs.room.Slug, Stream: st})
		}
	}
	for id := range rs.streams {
		if _, ok := next[id]; !ok {
			h.broadcastLocked(rs, api.StreamEnded{Type: "streamEnded", Room: rs.room.Slug, StreamID: id})
		}
	}
	rs.streams = next
}

// sendViewerStatsLocked sends streamers who are in their room a summary of
// what their viewers receive and, to app and web senders, their bitrate
// target.
func (h *Hub) sendViewerStatsLocked(rs *roomSubs) {
	for _, st := range rs.streams {
		var streamer []*conn
		for c := range rs.conns {
			if c.userID == st.User.ID {
				streamer = append(streamer, c)
			}
		}
		if len(streamer) == 0 {
			continue
		}
		out := api.ViewerStats{Type: "viewerStats", Room: rs.room.Slug, StreamID: st.ID}
		for _, v := range h.gw.ViewerStats(st.ID) {
			if !v.Watching {
				continue
			}
			out.Viewers++
			out.Bitrate += v.Bitrate
			out.MaxLossRatio = max(out.MaxLossRatio, v.LossRatio)
			out.MaxJitterMs = max(out.MaxJitterMs, float64(v.Jitter.Microseconds())/1000)
			out.MaxRttMs = max(out.MaxRttMs, float64(v.RTT.Microseconds())/1000)
		}
		b := marshal(out)
		for _, c := range streamer {
			c.enqueue(b)
		}
		if t, ok := h.gw.BitrateTarget(st.ID); ok {
			b := marshal(api.BitrateTarget{
				Type: "bitrateTarget", Room: rs.room.Slug, StreamID: st.ID,
				MaxBitrate: t.Bitrate, Viewers: t.Viewers, Congested: t.Congested,
			})
			for _, c := range streamer {
				c.enqueue(b)
			}
		}
	}
}

// Reevaluate applies a change to room's access rules or settings:
// subscribers who may no longer watch are unsubscribed and told why, and the
// others are told to fetch the room again. The friends of its streamers are
// told to fetch their friends' streams again.
func (h *Hub) Reevaluate(ctx context.Context, room store.Room) {
	h.reevaluate(ctx, room, true)
	h.friendRoomChanged(ctx, room)
}

func (h *Hub) reevaluate(ctx context.Context, room store.Room, changed bool) {
	if h.rooms.Stale(room) {
		return // Discord's view is out of date; the bot checks the room again
	}
	h.mu.Lock()
	rs := h.subs[room.ID]
	users := map[string]access.Reason{}
	if rs != nil {
		rs.room = room
		for c := range rs.conns {
			users[c.userID] = access.OK
		}
	}
	h.mu.Unlock()
	if rs == nil {
		return
	}

	for userID := range users {
		reason, err := h.rooms.Check(ctx, room, userID, access.Watch)
		if err != nil {
			// Keep things as they are rather than drop people on errors.
			slog.ErrorContext(ctx, "access check failed", "room", room.ID, "err", err)
			continue
		}
		users[userID] = reason
	}

	h.mu.Lock()
	defer h.mu.Unlock()
	if h.subs[room.ID] != rs {
		return
	}
	for c := range rs.conns {
		reason, checked := users[c.userID]
		switch {
		case checked && reason != access.OK:
			h.unsubscribeLocked(c, rs)
			c.send(api.Refused{Type: "refused", Room: room.Slug, Code: string(reason), Error: "you can no longer enter this room"})
		case changed:
			c.send(api.RoomChanged{Type: "roomChanged", Room: room.Slug})
		}
	}
}

// RoomIdle ends the room's invite links that last until the stream ends,
// once its last stream ended, and removes the people who joined through
// them. The gateway calls it (OnRoomIdle).
func (h *Hub) RoomIdle(ctx context.Context, roomID string) {
	ended, err := h.rooms.EndStreamInvites(ctx, roomID)
	if err != nil {
		slog.ErrorContext(ctx, "ending invite links failed", "room", roomID, "err", err)
		return
	}
	if !ended {
		return
	}
	room, err := h.db.GetRoom(ctx, roomID)
	if err != nil {
		slog.ErrorContext(ctx, "loading a room failed", "room", roomID, "err", err)
		return
	}
	h.Reevaluate(ctx, room)
}

// RemoveUser unsubscribes userID's connections from a room because its
// owner kicked or banned them, and tells them with code ("kicked" or
// "banned").
func (h *Hub) RemoveUser(room store.Room, userID, code string) {
	h.mu.Lock()
	defer h.mu.Unlock()
	rs := h.subs[room.ID]
	if rs == nil || userID == "" {
		return
	}
	for c := range rs.conns {
		if c.userID == userID {
			h.unsubscribeLocked(c, rs)
			c.send(api.Refused{Type: "refused", Room: room.Slug, Code: code, Error: "the room's owner removed you"})
		}
	}
}

// RecheckAll checks every subscription again, and the media sessions in
// those rooms, for access that ends by itself (temporary passes) or rooms
// that were deleted. Run does it every minute.
func (h *Hub) RecheckAll(ctx context.Context) {
	h.mu.Lock()
	ids := make([]string, 0, len(h.subs))
	for id := range h.subs {
		ids = append(ids, id)
	}
	h.mu.Unlock()

	for _, id := range ids {
		room, err := h.db.GetRoom(ctx, id)
		if errors.Is(err, sql.ErrNoRows) {
			h.dropRoom(id)
			continue
		}
		if err != nil {
			slog.ErrorContext(ctx, "loading a room failed", "room", id, "err", err)
			continue
		}
		h.reevaluate(ctx, room, false)
		// Passes that expired also end their media.
		h.gw.Reevaluate(ctx, room)
	}
}

// dropRoom unsubscribes everyone from a room that no longer exists.
func (h *Hub) dropRoom(roomID string) {
	h.mu.Lock()
	defer h.mu.Unlock()
	rs := h.subs[roomID]
	if rs == nil {
		return
	}
	for c := range rs.conns {
		c.send(api.Refused{Type: "refused", Room: rs.room.Slug, Code: "not_found", Error: "this room does not exist"})
		delete(c.subs, rs.room.Slug)
	}
	delete(h.subs, roomID)
}

// UserChanged shows a user's new handle, name or avatar in the presence of
// the rooms their connections are in. Their streams follow with the next
// statistics update, which describes streams afresh.
func (h *Hub) UserChanged(u api.User) {
	h.mu.Lock()
	defer h.mu.Unlock()
	rooms := map[*roomSubs]bool{}
	for c := range h.conns {
		if c.userID == u.ID {
			c.user = u
			for _, rs := range c.subs {
				rooms[rs] = true
			}
		}
	}
	for rs := range rooms {
		h.updatePresenceLocked(rs, nil)
	}
}

// StreamMoved tells the subscribers of from that a stream moved to another
// room (slug to).
func (h *Hub) StreamMoved(from store.Room, streamID, to string) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if rs := h.subs[from.ID]; rs != nil {
		h.broadcastLocked(rs, api.StreamMoved{Type: "streamMoved", Room: from.Slug, StreamID: streamID, ToRoom: to})
	}
}

// SendUser sends v to every connection of userID, subscribed to a room or
// not: messages about them rather than a room.
func (h *Hub) SendUser(userID string, v any) {
	if userID == "" {
		return
	}
	b := marshal(v)
	h.mu.Lock()
	defer h.mu.Unlock()
	for c := range h.conns {
		if c.userID == userID {
			c.enqueue(b)
		}
	}
}

// CloseSession closes the connections signed in with a session that ended
// (signing out, a revoked session).
func (h *Hub) CloseSession(sessionID string) {
	h.closeWhere(func(c *conn) bool { return c.sessionID == sessionID })
}

// CloseUser closes the connections of a user whose account is gone.
func (h *Hub) CloseUser(userID string) {
	h.closeWhere(func(c *conn) bool { return c.userID == userID })
}

func (h *Hub) closeWhere(match func(*conn) bool) {
	h.mu.Lock()
	defer h.mu.Unlock()
	for c := range h.conns {
		if c.userID != "" && match(c) {
			c.kill(statusSignedOut, "signed out")
		}
	}
}

func (h *Hub) internalError(c *conn, room, ref string, err error) {
	slog.Error("realtime request failed", "err", err)
	c.send(api.RealtimeError{Type: "error", Room: room, Ref: ref, Code: "internal", Error: "internal error"})
}
