package httpapi

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"net/http"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/coder/websocket"
	"github.com/google/uuid"

	"github.com/RAdu2005/cupstream/server/internal/api"
	"github.com/RAdu2005/cupstream/server/internal/store"
)

// socket is a browser tab's realtime WebSocket.
type socket struct {
	t  *testing.T
	ws *websocket.Conn
}

func (b *browser) dial(tab string, origin string) (*websocket.Conn, *http.Response, error) {
	ctx, cancel := context.WithTimeout(b.t.Context(), 5*time.Second)
	defer cancel()
	url := "ws" + strings.TrimPrefix(b.e.srv.URL, "http") + "/api/realtime?tab=" + tab
	header := http.Header{}
	if origin != "" {
		header.Set("Origin", origin)
	}
	return websocket.Dial(ctx, url, &websocket.DialOptions{HTTPClient: b.hc, HTTPHeader: header})
}

// connect opens a WebSocket like the website does.
func (b *browser) connect(tab string) *socket {
	b.t.Helper()
	ws, _, err := b.dial(tab, b.e.srv.URL)
	if err != nil {
		b.t.Fatal(err)
	}
	b.t.Cleanup(func() { ws.CloseNow() })
	return &socket{t: b.t, ws: ws}
}

func (s *socket) send(m api.ClientMessage) {
	s.t.Helper()
	b, _ := json.Marshal(m)
	if err := s.ws.Write(s.t.Context(), websocket.MessageText, b); err != nil {
		s.t.Fatal(err)
	}
}

// next returns the next message of type typ for which match (if given)
// holds, skipping the others.
func next[T any](s *socket, typ string, match ...func(T) bool) T {
	s.t.Helper()
	ctx, cancel := context.WithTimeout(s.t.Context(), 3*time.Second)
	defer cancel()
	for {
		_, data, err := s.ws.Read(ctx)
		if err != nil {
			s.t.Fatalf("waiting for %q: %v", typ, err)
		}
		var head struct{ Type string }
		json.Unmarshal(data, &head)
		if head.Type != typ {
			continue
		}
		v := decode[T](s.t, string(data))
		if len(match) == 0 || match[0](v) {
			return v
		}
	}
}

func (s *socket) subscribe(slug string) api.Subscribed {
	s.t.Helper()
	s.send(api.ClientMessage{Type: "subscribe", Room: slug})
	return next[api.Subscribed](s, "subscribed")
}

func (s *socket) refused(slug string) api.Refused {
	s.t.Helper()
	s.send(api.ClientMessage{Type: "subscribe", Room: slug})
	return next[api.Refused](s, "refused")
}

func handles(p api.Presence) string {
	var out []string
	for _, u := range p.Members {
		out = append(out, u.Handle)
	}
	return strings.Join(out, ",")
}

func presenceIs(want string, anonymous int) func(api.PresenceEvent) bool {
	return func(e api.PresenceEvent) bool {
		return handles(e.Presence) == want && e.Presence.Anonymous == anonymous
	}
}

func (b *browser) roomSlug() string {
	b.t.Helper()
	var acc api.Account
	b.json("GET", "/api/account", nil, &acc, 200)
	return acc.RoomSlug
}

func TestRealtimeOrigin(t *testing.T) {
	e := newTestEnv(t, envOptions{devLogin: true})
	b := e.browser(t)
	for name, tc := range map[string]struct {
		origin string
		ok     bool
	}{
		"same origin":   {e.srv.URL, true},
		"no origin":     {"", true}, // not a browser: it cannot carry anyone's cookies
		"other site":    {"https://evil.example", false},
		"other scheme":  {"https" + strings.TrimPrefix(e.srv.URL, "http"), false},
		"other port":    {"http://127.0.0.1:1", false},
		"invalid value": {"null", false},
	} {
		t.Run(name, func(t *testing.T) {
			ws, resp, err := b.dial("tab-"+strings.ReplaceAll(name, " ", "-"), tc.origin)
			if tc.ok != (err == nil) {
				t.Fatalf("err = %v", err)
			}
			if err == nil {
				ws.CloseNow()
			} else if resp == nil || resp.StatusCode != http.StatusForbidden {
				t.Fatalf("response = %+v", resp)
			}
		})
	}
	if _, resp, err := b.dial("bad tab!", e.srv.URL); err == nil || resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("invalid tab: %v", err)
	}
}

// TestRealtimeSubscriptions checks access to subscriptions, presence, and
// that subscribers who lose access lose the subscription.
// Each user and each client address may have only so many sockets open.
func TestRealtimeConnectionLimits(t *testing.T) {
	e := newTestEnv(t, envOptions{devLogin: true})
	e.hub.MaxConnsPerUser, e.hub.MaxConnsPerAddr = 2, 3
	alice, anon := e.browser(t), e.browser(t)
	alice.devLogin("alice")
	refused := func(b *browser, tab string) bool {
		t.Helper()
		ws, resp, err := b.dial(tab, e.srv.URL)
		if err == nil {
			t.Cleanup(func() { ws.CloseNow() })
			return false
		}
		if resp == nil || resp.StatusCode != http.StatusTooManyRequests {
			t.Fatalf("dial %s: %v, response %+v", tab, err, resp)
		}
		return true
	}

	first := alice.connect("alice-tab-1")
	alice.connect("alice-tab-2")
	if !refused(alice, "alice-tab-3") {
		t.Fatal("a third socket for alice was accepted")
	}
	// Signed out, only the address counts: it has one more.
	if refused(anon, "anon-tab-1") {
		t.Fatal("the address's third socket was refused")
	}
	if !refused(anon, "anon-tab-2") {
		t.Fatal("a fourth socket from the address was accepted")
	}

	// Closing a socket frees its place.
	first.ws.Close(websocket.StatusNormalClosure, "")
	deadline := time.Now().Add(3 * time.Second)
	for refused(alice, "alice-tab-3") {
		if time.Now().After(deadline) {
			t.Fatal("still refused after a socket closed")
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func TestRealtimeSubscriptions(t *testing.T) {
	e := newTestEnv(t, envOptions{devLogin: true})
	alice, bob, anon := e.browser(t), e.browser(t), e.browser(t)
	alice.devLogin("alice")
	bob.devLogin("bob")
	slug := alice.roomSlug()
	as, bs, ns := alice.connect("alice-tab"), bob.connect("bob-tab-1"), anon.connect("anon-tab")

	// The owner enters; others need to be allowed.
	if sub := as.subscribe(slug); handles(sub.Presence) != "alice" || len(sub.Streams) != 0 || len(sub.Chat) != 0 {
		t.Fatalf("alice subscribed: %+v", sub)
	}
	if r := bs.refused(slug); r.Code != "not_allowed" {
		t.Fatalf("bob: %+v", r)
	}
	if r := ns.refused(slug); r.Code != "sign_in_required" {
		t.Fatalf("anonymous: %+v", r)
	}
	if r := bs.refused("nope"); r.Code != "not_found" {
		t.Fatalf("unknown room: %+v", r)
	}

	// Two browsers see each other.
	alice.json("PUT", "/api/rooms/"+slug+"/allowed/bob", api.SetAllowedRequest{}, nil, 200)
	next(as, "roomChanged", func(e api.RoomChanged) bool { return e.Room == slug })
	if sub := bs.subscribe(slug); handles(sub.Presence) != "alice,bob" {
		t.Fatalf("bob subscribed: %+v", sub.Presence)
	}
	next(as, "presence", presenceIs("alice,bob", 0))

	// A second tab of the same person counts once.
	bob.connect("bob-tab-2").subscribe(slug)

	// Opening the room to everyone lets anonymous visitors in, counted.
	alice.json("PATCH", "/api/rooms/"+slug, api.UpdateRoomRequest{Visibility: "everyone"}, nil, 200)
	ns.subscribe(slug)
	next(as, "presence", presenceIs("alice,bob", 1))

	// Requiring sign-in removes them again.
	alice.json("PATCH", "/api/rooms/"+slug, api.UpdateRoomRequest{Visibility: "signed_in"}, nil, 200)
	if r := next[api.Refused](ns, "refused"); r.Room != slug || r.Code != "sign_in_required" {
		t.Fatalf("anonymous after signed_in: %+v", r)
	}
	next(as, "presence", presenceIs("alice,bob", 0))

	// Removing Bob from an invite-only room removes his subscriptions.
	alice.json("PATCH", "/api/rooms/"+slug, api.UpdateRoomRequest{Visibility: "invite_only"}, nil, 200)
	alice.json("DELETE", "/api/rooms/"+slug+"/allowed/bob", nil, nil, http.StatusNoContent)
	if r := next[api.Refused](bs, "refused"); r.Code != "not_allowed" {
		t.Fatalf("bob after removal: %+v", r)
	}
	next(as, "presence", presenceIs("alice", 0))

	// A pass that expires ends the subscription at the next periodic check.
	pass := e.browser(t)
	pass.devLogin("carol")
	carol, _ := e.db.GetUserByHandle(t.Context(), "carol")
	room, _ := e.db.GetRoomBySlug(t.Context(), slug)
	if _, err := e.db.SQL.Exec(`INSERT INTO room_passes (room_id, user_id, expires_at, created_at) VALUES (?, ?, ?, ?)`,
		room.ID, carol.ID, time.Now().Add(time.Hour).Unix(), time.Now().Unix()); err != nil {
		t.Fatal(err)
	}
	cs := pass.connect("carol-tab")
	cs.subscribe(slug)
	e.db.SQL.Exec(`UPDATE room_passes SET expires_at = ? WHERE user_id = ?`, time.Now().Add(-time.Second).Unix(), carol.ID)
	e.hub.RecheckAll(t.Context())
	if r := next[api.Refused](cs, "refused"); r.Code != "not_allowed" {
		t.Fatalf("carol after her pass expired: %+v", r)
	}
}

// A rename shows in presence and chat without reconnecting.
func TestRealtimeRename(t *testing.T) {
	e := newTestEnv(t, envOptions{devLogin: true})
	alice, bob := e.browser(t), e.browser(t)
	alice.devLogin("alice")
	bob.devLogin("bob")
	slug := alice.roomSlug()
	alice.json("PATCH", "/api/rooms/"+slug, api.UpdateRoomRequest{Visibility: "signed_in"}, nil, 200)
	as, bs := alice.connect("alice-tab"), bob.connect("bob-tab-1")
	as.subscribe(slug)
	bs.subscribe(slug)
	next(as, "presence", presenceIs("alice,bob", 0))

	bob.json("PATCH", "/api/account", api.UpdateProfileRequest{Handle: "robert", DisplayName: "Robert"}, nil, 200)
	ev := next(as, "presence", presenceIs("alice,robert", 0))
	if ev.Presence.Members[1].DisplayName != "Robert" {
		t.Fatalf("presence after the rename: %+v", ev.Presence)
	}
	next(bs, "presence", presenceIs("alice,robert", 0))

	bs.send(api.ClientMessage{Type: "chat", Room: slug, Body: "hi"})
	if m := next[api.ChatEvent](as, "chat").Message; m.User.Handle != "robert" || m.User.DisplayName != "Robert" {
		t.Fatalf("chat after the rename from %+v", m.User)
	}
}

func TestRealtimeChat(t *testing.T) {
	e := newTestEnv(t, envOptions{devLogin: true})
	alice, bob, anon := e.browser(t), e.browser(t), e.browser(t)
	alice.devLogin("alice")
	bob.devLogin("bob")
	slug := alice.roomSlug()
	alice.json("PUT", "/api/rooms/"+slug+"/allowed/bob", api.SetAllowedRequest{}, nil, 200)
	alice.json("PATCH", "/api/rooms/"+slug, api.UpdateRoomRequest{Visibility: "everyone"}, nil, 200)

	// A message older than 24 hours is never sent, even before it is deleted.
	room, _ := e.db.GetRoomBySlug(t.Context(), slug)
	owner, _ := e.db.GetUserByHandle(t.Context(), "alice")
	if err := e.db.CreateChatMessage(t.Context(), store.CreateChatMessageParams{
		ID: uuid.NewString(), RoomID: room.ID, UserID: owner.ID, Body: "yesterday", CreatedAt: time.Now().Add(-25 * time.Hour).Unix(),
	}); err != nil {
		t.Fatal(err)
	}

	as, bs, ns := alice.connect("alice-tab"), bob.connect("bob-tab-1"), anon.connect("anon-tab")
	if sub := as.subscribe(slug); len(sub.Chat) != 0 {
		t.Fatalf("expired chat was sent: %+v", sub.Chat)
	}
	bs.subscribe(slug)
	ns.subscribe(slug)

	// Messages reach everyone in the room, anonymous visitors included.
	bs.send(api.ClientMessage{Type: "chat", Room: slug, Body: "  hi\x00 there \r\n "})
	for _, s := range []*socket{as, bs, ns} {
		if m := next[api.ChatEvent](s, "chat").Message; m.Body != "hi there" || m.User.Handle != "bob" || m.ID == "" {
			t.Fatalf("chat = %+v", m)
		}
	}

	// Refusals.
	for _, tc := range []struct {
		s    *socket
		m    api.ClientMessage
		code string
	}{
		{ns, api.ClientMessage{Type: "chat", Room: slug, Body: "hello", Ref: "1"}, "sign_in_required"},
		{bs, api.ClientMessage{Type: "chat", Room: slug, Body: " \n ", Ref: "2"}, "invalid"},
		{bs, api.ClientMessage{Type: "chat", Room: slug, Body: strings.Repeat("a", 501), Ref: "3"}, "invalid"},
		{bs, api.ClientMessage{Type: "chat", Room: "elsewhere", Body: "hello", Ref: "4"}, "invalid"},
	} {
		tc.s.send(tc.m)
		if r := next[api.RealtimeError](tc.s, "error"); r.Code != tc.code || r.Ref != tc.m.Ref {
			t.Fatalf("%+v: %+v", tc.m, r)
		}
	}

	// Each user may send five messages at once (one is spent above).
	for range 4 {
		bs.send(api.ClientMessage{Type: "chat", Room: slug, Body: "spam"})
		next(as, "chat", func(e api.ChatEvent) bool { return e.Message.Body == "spam" })
	}
	bs.send(api.ClientMessage{Type: "chat", Room: slug, Body: "one too many", Ref: "limit"})
	if r := next[api.RealtimeError](bs, "error"); r.Code != "rate_limited" || r.Ref != "limit" {
		t.Fatalf("over the limit: %+v", r)
	}
	// Another tab of the same user shares the limit.
	bs2 := bob.connect("bob-tab-2")
	bs2.subscribe(slug)
	bs2.send(api.ClientMessage{Type: "chat", Room: slug, Body: "from my other tab"})
	if r := next[api.RealtimeError](bs2, "error"); r.Code != "rate_limited" {
		t.Fatalf("other tab: %+v", r)
	}

	// New subscribers get the history, oldest first.
	as.send(api.ClientMessage{Type: "chat", Room: slug, Body: "welcome"})
	welcome := next(bs, "chat", func(e api.ChatEvent) bool { return e.Message.Body == "welcome" }).Message
	history := e.browser(t).connect("late-tab").subscribe(slug).Chat
	if len(history) != 6 || history[0].Body != "hi there" || history[5].ID != welcome.ID {
		t.Fatalf("history = %+v", history)
	}

	// Only the owner may delete messages.
	bs.send(api.ClientMessage{Type: "deleteChat", Room: slug, ID: welcome.ID, Ref: "del"})
	if r := next[api.RealtimeError](bs, "error"); r.Code != "not_allowed" {
		t.Fatalf("bob deleting: %+v", r)
	}
	as.send(api.ClientMessage{Type: "deleteChat", Room: slug, ID: history[0].ID})
	for _, s := range []*socket{as, bs, ns} {
		if d := next[api.ChatDeleted](s, "chatDeleted"); d.ID != history[0].ID {
			t.Fatalf("deleted %+v", d)
		}
	}
	as.send(api.ClientMessage{Type: "deleteChat", Room: slug, ID: history[0].ID})
	if r := next[api.RealtimeError](as, "error"); r.Code != "not_found" {
		t.Fatalf("deleting twice: %+v", r)
	}
	if _, err := e.db.GetChatMessage(t.Context(), history[0].ID); !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("the deleted message: %v", err)
	}
}

// TestRealtimeStreamEvents is phase 3's live room page: streams appear,
// go live and end without polling.
func TestRealtimeStreamEvents(t *testing.T) {
	e := newTestEnv(t, envOptions{devLogin: true})
	alice := e.browser(t)
	alice.devLogin("alice")
	slug := alice.roomSlug()
	as := alice.connect("alice-tab")
	as.subscribe(slug)

	if r := alice.obs(alice.streamKey(), "", h264Offer); r.status != http.StatusCreated {
		t.Fatalf("OBS = %d %s", r.status, r.body)
	}
	st := next[api.StreamEvent](as, "stream").Stream
	if st.User.Handle != "alice" || st.Source != "obs" {
		t.Fatalf("stream = %+v", st)
	}
	e.reconcile(t)
	next(as, "stream", func(e api.StreamEvent) bool { return e.Stream.ID == st.ID && e.Stream.State == "live" })

	// A viewer joining changes the count.
	r := alice.whep(st.ID)
	if r.status != http.StatusCreated {
		t.Fatalf("WHEP = %d %s", r.status, r.body)
	}
	next(as, "stream", func(e api.StreamEvent) bool { return e.Stream.Viewers == 1 })

	// The streamer gets their viewers' statistics.
	if v := next[api.ViewerStats](as, "viewerStats"); v.StreamID != st.ID || v.Viewers != 1 {
		t.Fatalf("viewer stats = %+v", v)
	}

	// A late subscriber gets the stream in the snapshot.
	if sub := alice.connect("other-tab").subscribe(slug); len(sub.Streams) != 1 || sub.Streams[0].State != "live" {
		t.Fatalf("snapshot = %+v", sub.Streams)
	}

	alice.json("DELETE", "/api/streams/"+st.ID, nil, nil, http.StatusNoContent)
	if ended := next[api.StreamEnded](as, "streamEnded"); ended.StreamID != st.ID {
		t.Fatalf("ended = %+v", ended)
	}
}

// TestRealtimeClosedTab: when a tab's WebSocket is gone for longer than the
// grace period, the media sessions it opened are closed.
func TestRealtimeClosedTab(t *testing.T) {
	e := newTestEnv(t, envOptions{devLogin: true})
	alice, bob := e.browser(t), e.browser(t)
	alice.devLogin("alice")
	bob.devLogin("bob")
	slug := alice.roomSlug()
	alice.json("PATCH", "/api/rooms/"+slug, api.UpdateRoomRequest{Visibility: "signed_in"}, nil, 200)
	if r := alice.obs(alice.streamKey(), "", h264Offer); r.status != http.StatusCreated {
		t.Fatalf("OBS = %d %s", r.status, r.body)
	}
	e.reconcile(t)
	streamID := bob.room(slug).Streams[0].ID
	play := func(tab string) string {
		r := bob.do("POST", "/api/streams/"+streamID+"/whep?tab="+tab, h264Offer, "Content-Type", "application/sdp")
		if r.status != http.StatusCreated {
			t.Fatalf("WHEP = %d %s", r.status, r.body)
		}
		for _, s := range e.mtx.Sessions("s/" + streamID) {
			if strings.HasSuffix(r.header.Get("Location"), s.Secret) {
				return s.ID
			}
		}
		t.Fatal("no session")
		return ""
	}
	if r := bob.do("POST", "/api/streams/"+streamID+"/whep?tab=no", h264Offer, "Content-Type", "application/sdp"); r.status != http.StatusBadRequest {
		t.Fatalf("invalid tab = %d %s", r.status, r.body)
	}

	closed, kept, reloaded := play("closed-tab"), play("kept-tab"), play("reloaded-tab")
	other := play("bobs-other-tab") // never connected: left alone
	bob.connect("closed-tab").ws.Close(websocket.StatusGoingAway, "")
	bob.connect("kept-tab")
	reload := bob.connect("reloaded-tab")
	// Alice's tab with the same ID does not keep Bob's sessions.
	alice.connect("closed-tab")

	reload.ws.Close(websocket.StatusGoingAway, "")
	bob.connect("reloaded-tab") // back within the grace period
	time.Sleep(200 * time.Millisecond)

	kicked := e.mtx.Kicked()
	if !slices.Contains(kicked, closed) {
		t.Fatalf("closed tab's session was not closed: %v", kicked)
	}
	for _, id := range []string{kept, reloaded, other} {
		if slices.Contains(kicked, id) {
			t.Fatalf("session %s was closed: %v", id, kicked)
		}
	}
}

func TestRealtimeSignOut(t *testing.T) {
	e := newTestEnv(t, envOptions{devLogin: true})
	alice := e.browser(t)
	alice.devLogin("alice")
	s := alice.connect("signout-tab")
	s.subscribe(alice.roomSlug())
	alice.json("POST", "/api/auth/logout", nil, nil, http.StatusNoContent)
	ctx, cancel := context.WithTimeout(t.Context(), 3*time.Second)
	defer cancel()
	for {
		_, _, err := s.ws.Read(ctx)
		if err != nil {
			if websocket.CloseStatus(err) != 4001 {
				t.Fatalf("closed with %v", err)
			}
			return
		}
	}
}
