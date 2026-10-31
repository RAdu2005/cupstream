package realtime

import (
	"slices"

	"github.com/RAdu2005/cupstream/server/internal/api"
)

// The desktop app keeps a realtime connection open while it runs, signed in
// with its app session. The user's other connections are told which apps
// are running (AppsEvent), and the website can ask one to go live
// (appGoLive), which opens the app's screen picker.

// appsLocked lists the user's running apps, in the order they connected.
func (h *Hub) appsLocked(userID string) []api.AppInstance {
	out := []api.AppInstance{}
	for _, c := range h.apps[userID] {
		out = append(out, api.AppInstance{ID: c.sessionID, DeviceName: c.deviceName})
	}
	return out
}

// sendAppsLocked sends the user's running apps to all of their connections.
func (h *Hub) sendAppsLocked(userID string) {
	b := marshal(api.AppsEvent{Type: "apps", Apps: h.appsLocked(userID)})
	for c := range h.conns {
		if c.userID == userID {
			c.enqueue(b)
		}
	}
}

// appConnectedLocked registers a connection when it opens: an app is added
// to its user's list, which all their connections get; anyone else signed
// in gets the current list.
func (h *Hub) appConnectedLocked(c *conn) {
	switch {
	case c.app:
		h.apps[c.userID] = append(h.apps[c.userID], c)
		h.sendAppsLocked(c.userID)
	case c.userID != "":
		c.send(api.AppsEvent{Type: "apps", Apps: h.appsLocked(c.userID)})
	}
}

func (h *Hub) appDisconnectedLocked(c *conn) {
	if !c.app {
		return
	}
	list := slices.DeleteFunc(h.apps[c.userID], func(x *conn) bool { return x == c })
	if len(list) == 0 {
		delete(h.apps, c.userID)
	} else {
		h.apps[c.userID] = list
	}
	if !h.closed {
		h.sendAppsLocked(c.userID)
	}
}

// appGoLive asks one of the user's apps to go live: the one with the given
// ID, or the one that connected last.
func (h *Hub) appGoLive(c *conn, m api.ClientMessage) {
	if c.userID == "" || c.app {
		c.send(api.RealtimeError{Type: "error", Ref: m.Ref, Code: "invalid", Error: "only the website can ask the app to go live"})
		return
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	list := h.apps[c.userID]
	var target *conn
	for i := len(list) - 1; i >= 0 && target == nil; i-- {
		if m.ID == "" || list[i].sessionID == m.ID {
			target = list[i]
		}
	}
	if target == nil {
		c.send(api.RealtimeError{Type: "error", Ref: m.Ref, Code: "app_offline", Error: "the CupStream app is not running"})
		return
	}
	target.send(api.AppGoLive{Type: "goLive", Room: m.Room})
}
