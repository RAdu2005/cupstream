package realtime

import (
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/url"
	"sync"
	"time"

	"github.com/coder/websocket"

	"github.com/RAdu2005/cupstream/server/internal/api"
	"github.com/RAdu2005/cupstream/server/internal/auth"
	"github.com/RAdu2005/cupstream/server/internal/gateway"
	"github.com/RAdu2005/cupstream/server/internal/ratelimit"
	"github.com/RAdu2005/cupstream/server/internal/view"
)

const (
	// Cloudflare closes WebSockets that are idle for about 100 seconds.
	pingInterval = 30 * time.Second
	pingTimeout  = 15 * time.Second
	writeTimeout = 10 * time.Second
	// Messages queued for a connection before it counts as too slow and is
	// closed; the browser reconnects and fetches the current state.
	sendBuffer = 256
	// The largest message a browser may send.
	maxMessage = 4 << 10
	// Rooms one connection may subscribe to.
	maxRooms = 8
	// Messages a browser may send at once, and how often one more is allowed.
	floodBurst  = 20
	floodRefill = 100 * time.Millisecond
)

const (
	statusGoingAway = websocket.StatusGoingAway
	statusTooSlow   = websocket.StatusPolicyViolation
	// The session ended (signed out or revoked); reconnecting gives an
	// anonymous connection.
	statusSignedOut websocket.StatusCode = 4001
)

// conn is one browser tab's WebSocket, or a running desktop app's.
type conn struct {
	hub        *Hub
	ws         *websocket.Conn
	userID     string   // "" when signed out
	user       api.User // guarded by hub.mu once connected: UserChanged updates it
	sessionID  string
	tab        string
	app        bool // signed in with a desktop-app session
	deviceName string

	queue chan []byte
	done  chan struct{} // closed by kill
	once  sync.Once
	code  websocket.StatusCode
	why   string

	// Guarded by hub.mu.
	closed bool
	subs   map[string]*roomSubs // by room slug
}

// kill closes the connection with code and reason. Safe to call more than
// once and from any goroutine.
func (c *conn) kill(code websocket.StatusCode, reason string) {
	c.once.Do(func() {
		c.code, c.why = code, reason
		close(c.done)
	})
}

// enqueue queues a message without blocking; a connection that cannot keep
// up is closed.
func (c *conn) enqueue(b []byte) {
	select {
	case <-c.done:
	case c.queue <- b:
	default:
		c.kill(statusTooSlow, "too slow")
	}
}

func (c *conn) send(v any) { c.enqueue(marshal(v)) }

// allowedOrigin accepts browsers on the website's own origin, and clients
// that are not browsers (no Origin header), which cannot carry a visitor's
// cookies.
func (h *Hub) allowedOrigin(r *http.Request) bool {
	origin := r.Header.Get("Origin")
	if origin == "" {
		return true
	}
	u, err := url.Parse(origin)
	pub := h.cfg.PublicURL
	return err == nil && u.Scheme == pub.Scheme && u.Host == pub.Host
}

// Serve serves GET /api/realtime?tab=<id>: the WebSocket of one browser tab,
// or of the desktop app (signed in with its Bearer token, without a tab).
func (h *Hub) Serve(w http.ResponseWriter, r *http.Request) {
	if !h.allowedOrigin(r) {
		http.Error(w, "cross-origin WebSocket refused", http.StatusForbidden)
		return
	}
	tab := r.URL.Query().Get("tab")
	if tab != "" && !gateway.ValidTab.MatchString(tab) {
		http.Error(w, "tab is not a valid tab ID", http.StatusBadRequest)
		return
	}
	var userID string
	if cur, ok := auth.FromContext(r.Context()); ok {
		userID = cur.User.ID
	}
	addr := ratelimit.AddrKey(r.RemoteAddr)
	if !h.reserve(userID, addr) {
		http.Error(w, "too many open connections", http.StatusTooManyRequests)
		return
	}
	defer h.release(userID, addr)

	// The origin was checked above; the library checks it again by host.
	ws, err := websocket.Accept(w, r, &websocket.AcceptOptions{OriginPatterns: []string{h.cfg.PublicURL.Host}})
	if err != nil {
		return // Accept answered the request
	}
	ws.SetReadLimit(maxMessage)

	c := &conn{
		hub: h, ws: ws, tab: tab,
		queue: make(chan []byte, sendBuffer),
		done:  make(chan struct{}),
		subs:  map[string]*roomSubs{},
	}
	if cur, ok := auth.FromContext(r.Context()); ok {
		c.userID, c.user, c.sessionID = cur.User.ID, view.User(cur.User), cur.SessionID
		c.app, c.deviceName = cur.Kind == auth.KindApp, cur.DeviceName
	}
	if !h.connect(c) {
		ws.Close(statusGoingAway, "the server is restarting")
		return
	}
	defer h.wg.Done()
	defer h.disconnect(c)

	ctx, cancel := context.WithCancel(context.WithoutCancel(r.Context()))
	defer cancel()
	written := make(chan struct{})
	go func() {
		defer close(written)
		c.writeLoop(ctx)
	}()
	go c.pingLoop(ctx)

	flood := ratelimit.New(floodBurst, floodRefill)
	for {
		typ, data, err := ws.Read(ctx)
		if err != nil {
			break
		}
		if typ != websocket.MessageText {
			c.kill(websocket.StatusUnsupportedData, "messages are JSON text")
			break
		}
		if !flood.Allow("", h.now()) {
			c.kill(statusTooSlow, "too many messages")
			break
		}
		var m api.ClientMessage
		if err := json.Unmarshal(data, &m); err != nil {
			c.send(api.RealtimeError{Type: "error", Code: "invalid", Error: "messages are JSON objects"})
			continue
		}
		h.handle(ctx, c, m)
	}
	c.kill(websocket.StatusNormalClosure, "")
	cancel()
	<-written
}

func (h *Hub) handle(ctx context.Context, c *conn, m api.ClientMessage) {
	switch m.Type {
	case "subscribe":
		h.subscribe(ctx, c, m.Room, m.Ref)
	case "unsubscribe":
		h.unsubscribe(c, m.Room)
	case "chat":
		h.sendChat(ctx, c, m)
	case "deleteChat":
		h.deleteChat(ctx, c, m)
	case "appGoLive":
		h.appGoLive(c, m)
	default:
		c.send(api.RealtimeError{Type: "error", Room: m.Room, Ref: m.Ref, Code: "invalid", Error: "unknown message type"})
	}
}

// writeLoop sends queued messages until the connection is killed, then
// closes it with the kill's status.
func (c *conn) writeLoop(ctx context.Context) {
	for {
		select {
		case <-c.done:
			c.ws.Close(c.code, c.why)
			return
		case <-ctx.Done():
			c.ws.CloseNow()
			return
		case b := <-c.queue:
			wctx, cancel := context.WithTimeout(ctx, writeTimeout)
			err := c.ws.Write(wctx, websocket.MessageText, b)
			cancel()
			if err != nil {
				c.ws.CloseNow()
				return
			}
		}
	}
}

// pingLoop pings the browser, which keeps proxies from closing the idle
// connection and detects browsers that vanished.
func (c *conn) pingLoop(ctx context.Context) {
	t := time.NewTicker(pingInterval)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			pctx, cancel := context.WithTimeout(ctx, pingTimeout)
			err := c.ws.Ping(pctx)
			cancel()
			if err != nil {
				if ctx.Err() == nil {
					slog.Debug("realtime ping failed", "err", err)
				}
				c.ws.CloseNow()
				return
			}
		}
	}
}

// reserve counts a connection about to open against its user's and its
// address's limits, unless either is full.
func (h *Hub) reserve(userID, addr string) bool {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.perAddr[addr] >= h.MaxConnsPerAddr || (userID != "" && h.perUser[userID] >= h.MaxConnsPerUser) {
		return false
	}
	h.perAddr[addr]++
	if userID != "" {
		h.perUser[userID]++
	}
	return true
}

func (h *Hub) release(userID, addr string) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.perAddr[addr]--; h.perAddr[addr] <= 0 {
		delete(h.perAddr, addr)
	}
	if userID == "" {
		return
	}
	if h.perUser[userID]--; h.perUser[userID] <= 0 {
		delete(h.perUser, userID)
	}
}

// tabKey is a browser tab of a user ("" when signed out).
type tabKey struct{ userID, tab string }

type tabState struct {
	conns int
	timer *time.Timer
	gen   int // which timer is current
}

func (h *Hub) connect(c *conn) bool {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.closed {
		return false
	}
	h.wg.Add(1)
	h.conns[c] = struct{}{}
	h.appConnectedLocked(c)
	if c.tab != "" {
		k := tabKey{c.userID, c.tab}
		ts := h.tabs[k]
		if ts == nil {
			ts = &tabState{}
			h.tabs[k] = ts
		}
		ts.conns++
		if ts.timer != nil {
			ts.timer.Stop()
			ts.timer = nil
		}
	}
	return true
}

func (h *Hub) disconnect(c *conn) {
	h.mu.Lock()
	defer h.mu.Unlock()
	c.closed = true
	delete(h.conns, c)
	h.appDisconnectedLocked(c)
	for _, rs := range c.subs {
		h.unsubscribeLocked(c, rs)
	}
	if c.tab == "" || h.closed {
		return
	}
	k := tabKey{c.userID, c.tab}
	ts := h.tabs[k]
	if ts == nil {
		return
	}
	if ts.conns--; ts.conns > 0 {
		return
	}
	ts.gen++
	gen := ts.gen
	ts.timer = time.AfterFunc(h.TabGrace, func() { h.tabGone(k, ts, gen) })
}

// tabGone closes the media sessions of a tab that has not reconnected.
func (h *Hub) tabGone(k tabKey, ts *tabState, gen int) {
	h.mu.Lock()
	if h.closed || h.tabs[k] != ts || ts.conns > 0 || ts.gen != gen {
		h.mu.Unlock()
		return
	}
	delete(h.tabs, k)
	h.mu.Unlock()

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if n := h.gw.CloseTabSessions(ctx, k.userID, k.tab); n > 0 {
		slog.Debug("closed the media sessions of a closed tab", "sessions", n)
	}
}
