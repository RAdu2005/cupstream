package main

import (
	"encoding/json"
	"log"
	"net/http"
	"strings"
	"sync"
	"time"
	"unicode"

	"github.com/google/uuid"
	"github.com/gorilla/websocket"
)

const (
	chatHistorySize = 100
	chatTextMax     = 500
	chatMinInterval = 500 * time.Millisecond
	wsWriteWait     = 10 * time.Second
	wsPongWait      = 60 * time.Second
	wsPingPeriod    = 30 * time.Second
	wsReadLimit     = chatTextMax + 64
)

// ChatMessage is the on-wire representation of a single chat line.
type ChatMessage struct {
	ID        string    `json:"id"`
	Username  string    `json:"username"`
	Text      string    `json:"text"`
	Timestamp time.Time `json:"timestamp"`
}

// wsFrame is the envelope used for every WebSocket message in either direction.
type wsFrame struct {
	Type     string        `json:"type"`
	Messages []ChatMessage `json:"messages"`
	Message  *ChatMessage  `json:"message,omitempty"`
	Text     string        `json:"text,omitempty"`
	Error    string        `json:"error,omitempty"`
}

// ChatHub owns the connected clients, the history ring buffer, and the
// per-username rate limit table. It is safe for concurrent use.
type ChatHub struct {
	mu       sync.RWMutex
	clients  map[*chatClient]struct{}
	history  []ChatMessage
	lastSent map[string]time.Time
	upgrader websocket.Upgrader
}

type chatClient struct {
	hub      *ChatHub
	conn     *websocket.Conn
	username string
	send     chan wsFrame
}

func newChatHub() *ChatHub {
	return &ChatHub{
		clients:  make(map[*chatClient]struct{}),
		lastSent: make(map[string]time.Time),
		upgrader: websocket.Upgrader{
			CheckOrigin: func(r *http.Request) bool { return true },
		},
	}
}

func (h *ChatHub) historyHandler(w http.ResponseWriter, r *http.Request) {
	h.mu.RLock()
	msgs := make([]ChatMessage, len(h.history))
	copy(msgs, h.history)
	h.mu.RUnlock()
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(msgs)
}

func (h *ChatHub) wsHandler(w http.ResponseWriter, r *http.Request) {
	ck, err := r.Cookie("session")
	if err != nil || !validToken(ck.Value) {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}
	username := usernameFromToken(ck.Value)
	if username == "" {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}
	conn, err := h.upgrader.Upgrade(w, r, nil)
	if err != nil {
		log.Printf("ws upgrade: %v", err)
		return
	}
	c := &chatClient{hub: h, conn: conn, username: username, send: make(chan wsFrame, 16)}
	h.register(c)
	go c.writePump()
	go c.readPump()
}

func (h *ChatHub) register(c *chatClient) {
	h.mu.Lock()
	h.clients[c] = struct{}{}
	h.mu.Unlock()

	h.mu.RLock()
	msgs := make([]ChatMessage, len(h.history))
	copy(msgs, h.history)
	h.mu.RUnlock()

	c.send <- wsFrame{Type: "history", Messages: msgs}
}

func (h *ChatHub) unregister(c *chatClient) {
	h.mu.Lock()
	if _, ok := h.clients[c]; ok {
		delete(h.clients, c)
		close(c.send)
	}
	h.mu.Unlock()
	_ = c.conn.Close()
}

func (h *ChatHub) broadcast(msg ChatMessage) {
	h.mu.Lock()
	h.history = append(h.history, msg)
	if len(h.history) > chatHistorySize {
		h.history = h.history[len(h.history)-chatHistorySize:]
	}
	clients := make([]*chatClient, 0, len(h.clients))
	for c := range h.clients {
		clients = append(clients, c)
	}
	h.mu.Unlock()

	frame := wsFrame{Type: "message", Message: &msg}
	for _, c := range clients {
		select {
		case c.send <- frame:
		default:
		}
	}
}

func (h *ChatHub) canSend(username string) bool {
	h.mu.Lock()
	defer h.mu.Unlock()
	if last, ok := h.lastSent[username]; ok && time.Since(last) < chatMinInterval {
		return false
	}
	h.lastSent[username] = time.Now()
	return true
}

// cleanupLoop reaps stale rate-limit entries every 5 minutes.
func (h *ChatHub) cleanupLoop() {
	t := time.NewTicker(5 * time.Minute)
	for range t.C {
		h.mu.Lock()
		cutoff := time.Now().Add(-1 * time.Hour)
		for u, t := range h.lastSent {
			if t.Before(cutoff) {
				delete(h.lastSent, u)
			}
		}
		h.mu.Unlock()
	}
}

func (c *chatClient) readPump() {
	defer c.hub.unregister(c)
	c.conn.SetReadLimit(wsReadLimit)
	_ = c.conn.SetReadDeadline(time.Now().Add(wsPongWait))
	c.conn.SetPongHandler(func(string) error {
		return c.conn.SetReadDeadline(time.Now().Add(wsPongWait))
	})

	for {
		_, raw, err := c.conn.ReadMessage()
		if err != nil {
			return
		}
		var f wsFrame
		if err := json.Unmarshal(raw, &f); err != nil {
			c.sendError("invalid frame")
			continue
		}
		if f.Type != "send" {
			c.sendError("unknown type")
			continue
		}
		text := sanitizeChatText(f.Text)
		if text == "" {
			continue
		}
		if !c.hub.canSend(c.username) {
			c.sendError("slow down")
			continue
		}
		c.hub.broadcast(ChatMessage{
			ID:        uuid.NewString(),
			Username:  c.username,
			Text:      text,
			Timestamp: time.Now(),
		})
	}
}

func (c *chatClient) writePump() {
	ticker := time.NewTicker(wsPingPeriod)
	defer func() {
		ticker.Stop()
		_ = c.conn.Close()
	}()
	for {
		select {
		case frame, ok := <-c.send:
			_ = c.conn.SetWriteDeadline(time.Now().Add(wsWriteWait))
			if !ok {
				_ = c.conn.WriteMessage(websocket.CloseMessage, []byte{})
				return
			}
			if err := c.conn.WriteJSON(frame); err != nil {
				return
			}
		case <-ticker.C:
			_ = c.conn.SetWriteDeadline(time.Now().Add(wsWriteWait))
			if err := c.conn.WriteMessage(websocket.PingMessage, nil); err != nil {
				return
			}
		}
	}
}

func (c *chatClient) sendError(s string) {
	select {
	case c.send <- wsFrame{Type: "error", Error: s}:
	default:
	}
}

func sanitizeChatText(s string) string {
	s = strings.TrimSpace(s)
	if len(s) > chatTextMax {
		s = s[:chatTextMax]
	}
	var b strings.Builder
	for _, r := range s {
		if r == '\n' || !unicode.IsControl(r) {
			b.WriteRune(r)
		}
	}
	return strings.TrimSpace(b.String())
}
