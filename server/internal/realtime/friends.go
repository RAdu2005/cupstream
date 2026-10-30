package realtime

import (
	"context"
	"log/slog"
	"time"

	"github.com/RAdu2005/cupstream/server/internal/access"
	"github.com/RAdu2005/cupstream/server/internal/api"
	"github.com/RAdu2005/cupstream/server/internal/gateway"
	"github.com/RAdu2005/cupstream/server/internal/store"
	"github.com/RAdu2005/cupstream/server/internal/view"
)

// Friends are told when a friend goes live (a notification) and whenever a
// friend's stream starts, changes state, moves or ends (friendsChanged, so
// their list of friends' streams is fetched again), but only for rooms they
// may enter.

// announceWithin: a stream that becomes live later than this after it was
// created (or one found after a restart) is not announced again.
const announceWithin = 2 * time.Minute

// friendStream is what friends were last told about a user's stream.
type friendStream struct {
	id, roomID string
	state      gateway.State
}

// connectedUsers returns those of ids with a connection open.
func (h *Hub) connectedUsers(ids []string) []string {
	h.mu.Lock()
	defer h.mu.Unlock()
	on := map[string]bool{}
	for c := range h.conns {
		on[c.userID] = true
	}
	var out []string
	for _, id := range ids {
		if on[id] {
			out = append(out, id)
		}
	}
	return out
}

// onlineFriends lists userID's friends who have a connection open.
func (h *Hub) onlineFriends(ctx context.Context, userID string) []string {
	ids, err := h.db.ListFriendIDs(ctx, userID)
	if err != nil {
		slog.ErrorContext(ctx, "listing friends failed", "user", userID, "err", err)
		return nil
	}
	return h.connectedUsers(ids)
}

// mayWatch reports whether userID may watch in the room with this ID.
func (h *Hub) mayWatch(ctx context.Context, rooms map[string]store.Room, roomID, userID string) bool {
	if roomID == "" {
		return false
	}
	room, ok := rooms[roomID]
	if !ok {
		var err error
		if room, err = h.db.GetRoom(ctx, roomID); err != nil {
			return false
		}
		rooms[roomID] = room
	}
	reason, err := h.rooms.Check(ctx, room, userID, access.Watch)
	return err == nil && reason == access.OK
}

// syncFriendStreams compares every user's stream with what their friends
// were last told, and tells them about the differences. Only Run calls it.
func (h *Hub) syncFriendStreams(ctx context.Context) {
	if h.friendStreams == nil {
		h.friendStreams, h.announced = map[string]friendStream{}, map[string]bool{}
	}
	next := map[string]friendStream{}
	created := map[string]int64{}
	for _, ls := range h.gw.ActiveStreams() {
		next[ls.Stream.UserID] = friendStream{ls.Stream.ID, ls.Stream.RoomID, ls.State}
		created[ls.Stream.ID] = ls.Stream.StartedAt
	}
	var changed []string
	for userID, fs := range next {
		if h.friendStreams[userID] != fs {
			changed = append(changed, userID)
		}
	}
	for userID := range h.friendStreams {
		if _, ok := next[userID]; !ok {
			changed = append(changed, userID)
		}
	}
	prev := h.friendStreams
	h.friendStreams = next
	for id := range h.announced {
		if _, ok := created[id]; !ok {
			delete(h.announced, id)
		}
	}

	rooms := map[string]store.Room{}
	users := view.Users{DB: h.db}
	for _, userID := range changed {
		before, now := prev[userID], next[userID]
		announce := now.state == gateway.Live && !h.announced[now.id] &&
			h.now().Sub(time.Unix(created[now.id], 0)) < announceWithin
		if announce {
			h.announced[now.id] = true
		}
		friends := h.onlineFriends(ctx, userID)
		if len(friends) == 0 {
			continue
		}
		var told, live []string
		for _, f := range friends {
			watchNow := h.mayWatch(ctx, rooms, now.roomID, f)
			if watchNow || h.mayWatch(ctx, rooms, before.roomID, f) {
				told = append(told, f)
			}
			if announce && watchNow {
				live = append(live, f)
			}
		}
		h.NotifyUsers(api.FriendsChanged{Type: "friendsChanged"}, told...)
		if len(live) > 0 {
			u, err := users.Get(ctx, userID)
			if err != nil {
				slog.ErrorContext(ctx, "loading a user failed", "user", userID, "err", err)
				continue
			}
			h.NotifyUsers(api.Notification{Type: "notification", Kind: "friendLive", User: u, RoomSlug: rooms[now.roomID].Slug}, live...)
		}
	}
}

// friendRoomChanged tells the friends of everyone streaming in room to fetch
// their friends' streams again: access to the room changed, so some may have
// gained or lost sight of them.
func (h *Hub) friendRoomChanged(ctx context.Context, room store.Room) {
	for _, ls := range h.gw.RoomStreams(room.ID) {
		h.NotifyUsers(api.FriendsChanged{Type: "friendsChanged"}, h.onlineFriends(ctx, ls.Stream.UserID)...)
	}
}
