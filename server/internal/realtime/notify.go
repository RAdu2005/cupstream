package realtime

// NotifyUsers sends v to every connection of the given users. It is for
// app-wide messages, which concern the user rather than a room: friend
// requests and other notifications.
func (h *Hub) NotifyUsers(v any, userIDs ...string) {
	want := map[string]bool{}
	for _, id := range userIDs {
		if id != "" {
			want[id] = true
		}
	}
	b := marshal(v)
	h.mu.Lock()
	defer h.mu.Unlock()
	for c := range h.conns {
		if want[c.userID] {
			c.enqueue(b)
		}
	}
}
