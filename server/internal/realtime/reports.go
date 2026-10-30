package realtime

import (
	"github.com/RAdu2005/cupstream/server/internal/api"
	"github.com/RAdu2005/cupstream/server/internal/gateway"
)

// viewerReport records what a viewer's browser measured on the streams it
// plays in a room it is subscribed to. Reports are frequent and need no
// answer, so only malformed ones get an error.
func (h *Hub) viewerReport(c *conn, m api.ClientMessage) {
	if m.Report == nil || len(m.Report.Streams) > gateway.MaxRoomStreams {
		c.send(api.RealtimeError{Type: "error", Room: m.Room, Ref: m.Ref, Code: "invalid", Error: "a report lists the streams you play"})
		return
	}
	h.mu.Lock()
	rs, ok := c.subs[m.Room]
	var streams []api.StreamReport
	if ok {
		// Only streams of this room; the gateway also matches the tab's sessions.
		for _, s := range m.Report.Streams {
			if _, inRoom := rs.streams[s.StreamID]; inRoom {
				streams = append(streams, s)
			}
		}
	}
	h.mu.Unlock()
	if !ok || len(streams) == 0 {
		return
	}
	r := *m.Report
	r.Streams = streams
	h.gw.Report(c.userID, c.tab, r)
}
