package realtime

import (
	"context"
	"database/sql"
	"errors"
	"log/slog"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/google/uuid"

	"github.com/RAdu2005/cupstream/server/internal/access"
	"github.com/RAdu2005/cupstream/server/internal/api"
	"github.com/RAdu2005/cupstream/server/internal/store"
	"github.com/RAdu2005/cupstream/server/internal/view"
)

const (
	// Chat messages are deleted this long after they were sent.
	chatTTL = 24 * time.Hour
	// Messages a new subscriber receives.
	chatHistory = 100
	// The longest message, in characters.
	maxChatBody = 500
	// Each user may send chatBurst messages at once, then one per chatRefill.
	chatBurst  = 5
	chatRefill = time.Second
)

// chatRoom returns the room c is subscribed to under this slug, refusing
// visitors who are not signed in.
func (h *Hub) chatRoom(c *conn, m api.ClientMessage) (store.Room, bool) {
	if c.userID == "" {
		c.send(api.RealtimeError{Type: "error", Room: m.Room, Ref: m.Ref, Code: string(access.SignInRequired), Error: "sign in to chat"})
		return store.Room{}, false
	}
	h.mu.Lock()
	rs, ok := c.subs[m.Room]
	h.mu.Unlock()
	if !ok {
		c.send(api.RealtimeError{Type: "error", Room: m.Room, Ref: m.Ref, Code: "invalid", Error: "subscribe to the room first"})
		return store.Room{}, false
	}
	return rs.room, true
}

// cleanChatBody trims a message and removes control characters other than
// line breaks and tabs.
func cleanChatBody(s string) (string, error) {
	if !utf8.ValidString(s) {
		return "", errors.New("the message is not valid text")
	}
	s = strings.Map(func(r rune) rune {
		if unicode.IsControl(r) && r != '\n' && r != '\t' {
			return -1
		}
		return r
	}, strings.ReplaceAll(s, "\r\n", "\n"))
	s = strings.TrimSpace(s)
	switch n := utf8.RuneCountInString(s); {
	case n == 0:
		return "", errors.New("the message is empty")
	case n > maxChatBody:
		return "", errors.New("the message is too long")
	}
	return s, nil
}

func (h *Hub) sendChat(ctx context.Context, c *conn, m api.ClientMessage) {
	room, ok := h.chatRoom(c, m)
	if !ok {
		return
	}
	body, err := cleanChatBody(m.Body)
	if err != nil {
		c.send(api.RealtimeError{Type: "error", Room: m.Room, Ref: m.Ref, Code: "invalid", Error: err.Error()})
		return
	}
	now := h.now()
	if !h.chatLimit.Allow(c.userID, now) {
		c.send(api.RealtimeError{Type: "error", Room: m.Room, Ref: m.Ref, Code: "rate_limited", Error: "you are sending messages too quickly"})
		return
	}
	// The room as stored now: its visibility may have changed.
	room, err = h.db.GetRoom(ctx, room.ID)
	if err != nil {
		h.internalError(c, m.Room, m.Ref, err)
		return
	}
	reason, err := h.rooms.Check(ctx, room, c.userID, access.Chat)
	if err != nil {
		h.internalError(c, m.Room, m.Ref, err)
		return
	}
	if reason != access.OK {
		c.send(api.RealtimeError{Type: "error", Room: m.Room, Ref: m.Ref, Code: string(reason), Error: "you cannot chat in this room"})
		return
	}

	msg := api.ChatMessage{ID: uuid.Must(uuid.NewV7()).String(), User: c.user, Body: body, CreatedAt: now.Unix()}
	if err := h.db.CreateChatMessage(ctx, store.CreateChatMessageParams{
		ID: msg.ID, RoomID: room.ID, UserID: c.userID, Body: body, CreatedAt: msg.CreatedAt,
	}); err != nil {
		h.internalError(c, m.Room, m.Ref, err)
		return
	}
	h.mu.Lock()
	if rs := h.subs[room.ID]; rs != nil {
		h.broadcastLocked(rs, api.ChatEvent{Type: "chat", Room: room.Slug, Message: msg})
	}
	h.mu.Unlock()
}

// deleteChat deletes a message for a moderator: the owner of a link room.
// In Discord rooms, access.ModerateChat follows Discord's Manage Messages
// permission once rooms.Subject reads the bot's state (phase 6).
func (h *Hub) deleteChat(ctx context.Context, c *conn, m api.ClientMessage) {
	room, ok := h.chatRoom(c, m)
	if !ok {
		return
	}
	reason, err := h.rooms.Check(ctx, room, c.userID, access.ModerateChat)
	if err != nil {
		h.internalError(c, m.Room, m.Ref, err)
		return
	}
	if reason != access.OK {
		c.send(api.RealtimeError{Type: "error", Room: m.Room, Ref: m.Ref, Code: string(reason), Error: "you cannot delete messages in this room"})
		return
	}
	n, err := h.db.DeleteChatMessage(ctx, store.DeleteChatMessageParams{ID: m.ID, RoomID: room.ID})
	if err != nil {
		h.internalError(c, m.Room, m.Ref, err)
		return
	}
	if n == 0 {
		c.send(api.RealtimeError{Type: "error", Room: m.Room, Ref: m.Ref, Code: "not_found", Error: "this message was already deleted"})
		return
	}
	h.mu.Lock()
	if rs := h.subs[room.ID]; rs != nil {
		h.broadcastLocked(rs, api.ChatDeleted{Type: "chatDeleted", Room: room.Slug, ID: m.ID})
	}
	h.mu.Unlock()
}

// chatHistory returns the room's latest messages from the last 24 hours,
// oldest first.
func (h *Hub) chatHistory(ctx context.Context, roomID string) ([]api.ChatMessage, error) {
	rows, err := h.db.ListChatMessages(ctx, store.ListChatMessagesParams{
		RoomID: roomID, Since: h.now().Add(-chatTTL).Unix(), Max: chatHistory,
	})
	if err != nil {
		return nil, err
	}
	out := make([]api.ChatMessage, len(rows))
	for i, row := range rows {
		out[len(rows)-1-i] = view.ChatMessage(row.ChatMessage, row.User)
	}
	return out, nil
}

// deleteExpiredChat deletes messages older than 24 hours. Until it runs,
// they are no longer sent to anyone (chatHistory), and browsers hide them
// themselves.
func (h *Hub) deleteExpiredChat(ctx context.Context) {
	n, err := h.db.DeleteChatMessagesBefore(ctx, h.now().Add(-chatTTL).Unix())
	switch {
	case err != nil && !errors.Is(err, sql.ErrConnDone) && ctx.Err() == nil:
		slog.WarnContext(ctx, "deleting expired chat failed", "err", err)
	case n > 0:
		slog.DebugContext(ctx, "deleted expired chat", "count", n)
	}
}
