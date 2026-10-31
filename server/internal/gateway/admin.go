package gateway

import "context"

// CodeBanned refuses streaming to a user an admin banned.
const CodeBanned = "banned"

// End reasons for streams an admin ended, directly or by banning the user.
const (
	EndAdmin  = "ended_by_admin"
	EndBanned = "user_banned"
)

// AdminStream is a live stream as the admin page sees it.
type AdminStream struct {
	LiveStream
	// Watching viewers, and what MediaMTX sends them, in bits per second.
	Watching int
	Outgoing int64
}

// AllStreams returns every active stream, oldest first.
func (g *Gateway) AllStreams() []AdminStream {
	g.mu.Lock()
	defer g.mu.Unlock()
	counts := g.viewerCountsLocked()
	var live []LiveStream
	for id, s := range g.streams {
		live = append(live, g.snapshotLocked(id, s, counts))
	}
	slicesSortByStart(live)
	out := make([]AdminStream, len(live))
	byID := map[string]*AdminStream{}
	for i, ls := range live {
		out[i] = AdminStream{LiveStream: ls}
		byID[ls.Stream.ID] = &out[i]
	}
	for _, sess := range g.sessions {
		if as := byID[sess.streamID]; as != nil && !sess.publish && sess.watching() {
			as.Watching++
			as.Outgoing += sess.stats.pub.Bitrate
		}
	}
	return out
}

// CloseUserViewers closes every viewer session of a user, e.g. one who was
// banned. Their own streams are ended separately (EndUserStreams).
func (g *Gateway) CloseUserViewers(ctx context.Context, userID string) int {
	if userID == "" {
		return 0
	}
	var ids []string
	g.mu.Lock()
	for id, sess := range g.sessions {
		if !sess.publish && sess.userID == userID {
			ids = append(ids, id)
		}
	}
	g.mu.Unlock()
	for _, id := range ids {
		g.kickSession(ctx, id)
	}
	return len(ids)
}
