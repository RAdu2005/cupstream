// Package gateway is the media gateway: every WHIP (publish) and WHEP (play)
// request passes through it to MediaMTX, which listens on localhost only.
//
// The gateway decides access, gives each stream its own MediaMTX path
// (s/<streamId>), records every session so it can end it when access
// changes, and reconciles live state with MediaMTX's API every two seconds.
package gateway

import (
	"cmp"
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/base32"
	"errors"
	"log/slog"
	"net/url"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/RAdu2005/cupstream/server/internal/access"
	"github.com/RAdu2005/cupstream/server/internal/mediamtx"
	"github.com/RAdu2005/cupstream/server/internal/rooms"
	"github.com/RAdu2005/cupstream/server/internal/store"
)

const (
	reconcileInterval = 2 * time.Second
	// A stream whose publisher has not connected after this long is dropped.
	startTimeout = 30 * time.Second
	// A stream whose publisher disconnected is kept this long for reconnects.
	reconnectGrace = 15 * time.Second
	// A session MediaMTX has never listed is forgotten after this long.
	sessionHandshakeTimeout = 30 * time.Second
)

type Source string

const (
	SourceApp Source = "app"
	SourceWeb Source = "web"
	SourceOBS Source = "obs"
)

type State string

const (
	Starting     State = "starting"     // created, publisher not connected yet
	Live         State = "live"         // MediaMTX reports the path online
	Reconnecting State = "reconnecting" // publisher gone, within the grace period
)

// End reasons stored in streams.end_reason.
const (
	EndStopped       = "stopped"
	EndDisconnected  = "disconnected"
	EndFailedToStart = "failed_to_start"
	EndReplaced      = "replaced"
	EndModerator     = "ended_by_moderator"
	EndAccountMerged = "account_merged"
	EndAccessRevoked = "access_revoked"
)

type liveStream struct {
	rec   store.Stream
	state State
	since time.Time // when the current state began
	peak  int
}

type session struct {
	mtxID     string // MediaMTX session ID, from the ID response header
	secret    string // from the Location header; empty for sessions adopted after a restart
	streamID  string
	publish   bool
	userID    string // viewer ("" for anonymous) or publisher
	created   time.Time
	confirmed bool // seen in MediaMTX's session list
}

// LiveStream is a snapshot of a stream for API responses.
type LiveStream struct {
	Stream  store.Stream
	State   State
	Viewers int
}

type Gateway struct {
	db        *store.DB
	mtx       *mediamtx.Client
	rooms     *rooms.Service
	publicURL *url.URL
	now       func() time.Time

	mu       sync.Mutex
	streams  map[string]*liveStream // by stream ID
	sessions map[string]*session    // by MediaMTX session ID
}

func New(db *store.DB, mtx *mediamtx.Client, rs *rooms.Service, publicURL *url.URL) *Gateway {
	return &Gateway{
		db: db, mtx: mtx, rooms: rs, publicURL: publicURL, now: time.Now,
		streams:  map[string]*liveStream{},
		sessions: map[string]*session{},
	}
}

func mtxPath(streamID string) string { return "s/" + streamID }

func streamIDFromPath(p string) (string, bool) { return strings.CutPrefix(p, "s/") }

var idEncoding = base32.StdEncoding.WithPadding(base32.NoPadding)

func newStreamID() string {
	b := make([]byte, 10)
	rand.Read(b)
	return strings.ToLower(idEncoding.EncodeToString(b))
}

// Run restores streams that were live before a restart and reconciles with
// MediaMTX until ctx is done.
func (g *Gateway) Run(ctx context.Context) error {
	if err := g.Restore(ctx); err != nil {
		return err
	}
	t := time.NewTicker(reconcileInterval)
	defer t.Stop()
	for {
		if err := g.Reconcile(ctx); err != nil && ctx.Err() == nil {
			slog.Warn("media reconcile failed", "err", err)
		}
		select {
		case <-ctx.Done():
			return nil
		case <-t.C:
		}
	}
}

// Restore loads the streams that were active before a restart. They count as
// reconnecting until the next Reconcile finds them online in MediaMTX.
func (g *Gateway) Restore(ctx context.Context) error {
	active, err := g.db.ListActiveStreams(ctx)
	if err != nil {
		return err
	}
	g.mu.Lock()
	for _, s := range active {
		g.streams[s.ID] = &liveStream{rec: s, state: Reconnecting, since: g.now(), peak: int(s.PeakViewers)}
	}
	g.mu.Unlock()
	if len(active) > 0 {
		slog.Info("restored active streams", "count", len(active))
	}
	return nil
}

type ending struct {
	id, reason string
	peak       int
}

// Reconcile updates live state from MediaMTX: streams go live, start their
// reconnect grace period or end, and sessions are matched with MediaMTX's.
func (g *Gateway) Reconcile(ctx context.Context) error {
	paths, err := g.mtx.Paths(ctx)
	if err != nil {
		return err
	}
	mtxSessions, err := g.mtx.WebRTCSessions(ctx)
	if err != nil {
		return err
	}
	online := map[string]bool{}
	for _, p := range paths {
		online[p.Name] = p.Online
	}

	now := g.now()
	var ends []ending
	var kicks []string

	g.mu.Lock()
	for id, s := range g.streams {
		up := online[mtxPath(id)]
		switch {
		case up && s.state != Live:
			s.state, s.since = Live, now
		case !up && s.state == Live:
			s.state, s.since = Reconnecting, now
		case !up && s.state == Starting && now.Sub(s.since) > startTimeout:
			ends = append(ends, ending{id, EndFailedToStart, s.peak})
		case !up && s.state == Reconnecting && now.Sub(s.since) > reconnectGrace:
			ends = append(ends, ending{id, EndDisconnected, s.peak})
		}
	}

	present := map[string]bool{}
	for _, ms := range mtxSessions {
		present[ms.ID] = true
		if sess, ok := g.sessions[ms.ID]; ok {
			sess.confirmed = true
			continue
		}
		// Unknown session: one we created before a restart, or a stray.
		streamID, ok := streamIDFromPath(ms.Path)
		if _, live := g.streams[streamID]; !ok || !live {
			kicks = append(kicks, ms.ID)
			continue
		}
		q, _ := url.ParseQuery(ms.Query)
		g.sessions[ms.ID] = &session{
			mtxID: ms.ID, streamID: streamID, publish: ms.State == "publish",
			userID: q.Get("u"), created: ms.Created, confirmed: true,
		}
	}
	for id, sess := range g.sessions {
		if !present[id] && (sess.confirmed || now.Sub(sess.created) > sessionHandshakeTimeout) {
			delete(g.sessions, id)
		}
	}
	for id, n := range g.viewerCountsLocked() {
		if s := g.streams[id]; s != nil && n > s.peak {
			s.peak = n
		}
	}
	g.mu.Unlock()

	for _, id := range kicks {
		slog.Info("closing unknown media session", "id", id)
		if err := g.mtx.Kick(ctx, id); err != nil {
			slog.Warn("kick failed", "id", id, "err", err)
		}
	}
	for _, e := range ends {
		g.finish(ctx, e)
	}
	return nil
}

func (g *Gateway) viewerCountsLocked() map[string]int {
	counts := map[string]int{}
	for _, s := range g.sessions {
		if !s.publish {
			counts[s.streamID]++
		}
	}
	return counts
}

// EndStream ends a stream: its publisher and viewers are disconnected.
func (g *Gateway) EndStream(ctx context.Context, streamID, reason string) {
	g.mu.Lock()
	s, ok := g.streams[streamID]
	peak := 0
	if ok {
		peak = s.peak
	}
	g.mu.Unlock()
	if ok {
		g.finish(ctx, ending{streamID, reason, peak})
	}
}

// EndUserStreams ends every stream of userID.
func (g *Gateway) EndUserStreams(ctx context.Context, userID, reason string) {
	if s := g.activeStreamOf(userID); s != nil {
		g.EndStream(ctx, s.ID, reason)
	}
}

func (g *Gateway) finish(ctx context.Context, e ending) {
	g.mu.Lock()
	delete(g.streams, e.id)
	var ids []string
	for id, sess := range g.sessions {
		if sess.streamID == e.id {
			ids = append(ids, id)
			delete(g.sessions, id)
		}
	}
	g.mu.Unlock()

	if err := g.db.EndStream(ctx, store.EndStreamParams{
		EndedAt:   sql.NullInt64{Int64: g.now().Unix(), Valid: true},
		EndReason: sql.NullString{String: e.reason, Valid: true},
		Peak:      int64(e.peak),
		ID:        e.id,
	}); err != nil {
		slog.Error("ending stream failed", "stream", e.id, "err", err)
	}
	for _, id := range ids {
		if err := g.mtx.Kick(ctx, id); err != nil {
			slog.Warn("kick failed", "id", id, "err", err)
		}
	}
	slog.Info("stream ended", "stream", e.id, "reason", e.reason)
}

// Reevaluate applies a change to room's access rules: viewers who may no
// longer watch are disconnected, and streams whose streamer may no longer
// stream there are ended.
func (g *Gateway) Reevaluate(ctx context.Context, room store.Room) {
	type viewer struct{ mtxID, userID string }
	var viewers []viewer
	streamers := map[string]string{} // stream ID -> user ID
	g.mu.Lock()
	for id, s := range g.streams {
		if s.rec.RoomID == room.ID {
			streamers[id] = s.rec.UserID
		}
	}
	for id, sess := range g.sessions {
		if _, inRoom := streamers[sess.streamID]; inRoom && !sess.publish {
			viewers = append(viewers, viewer{id, sess.userID})
		}
	}
	g.mu.Unlock()

	allowed := func(userID string, action access.Action) bool {
		r, err := g.rooms.Check(ctx, room, userID, action)
		if err != nil {
			slog.Error("access check failed", "err", err)
			return true // keep things as they are rather than kick on errors
		}
		return r == access.OK
	}

	for streamID, userID := range streamers {
		if !allowed(userID, access.Stream) {
			g.EndStream(ctx, streamID, EndAccessRevoked)
		}
	}
	canWatch := map[string]bool{}
	for _, v := range viewers {
		ok, seen := canWatch[v.userID]
		if !seen {
			ok = allowed(v.userID, access.Watch)
			canWatch[v.userID] = ok
		}
		if !ok {
			g.kickSession(ctx, v.mtxID)
		}
	}
}

// forgetSecret drops the session with this secret after its client closed it.
func (g *Gateway) forgetSecret(secret string) {
	g.mu.Lock()
	defer g.mu.Unlock()
	for id, sess := range g.sessions {
		if sess.secret == secret {
			delete(g.sessions, id)
		}
	}
}

func (g *Gateway) kickSession(ctx context.Context, mtxID string) {
	g.mu.Lock()
	delete(g.sessions, mtxID)
	g.mu.Unlock()
	if err := g.mtx.Kick(ctx, mtxID); err != nil {
		slog.Warn("kick failed", "id", mtxID, "err", err)
	}
}

// RoomStreams lists the active streams of a room, oldest first.
func (g *Gateway) RoomStreams(roomID string) []LiveStream {
	g.mu.Lock()
	defer g.mu.Unlock()
	counts := g.viewerCountsLocked()
	var out []LiveStream
	for id, s := range g.streams {
		if s.rec.RoomID == roomID {
			out = append(out, LiveStream{Stream: s.rec, State: s.state, Viewers: counts[id]})
		}
	}
	slicesSortByStart(out)
	return out
}

// Stream returns an active stream.
func (g *Gateway) Stream(streamID string) (LiveStream, bool) {
	g.mu.Lock()
	defer g.mu.Unlock()
	s, ok := g.streams[streamID]
	if !ok {
		return LiveStream{}, false
	}
	return LiveStream{Stream: s.rec, State: s.state, Viewers: g.viewerCountsLocked()[streamID]}, true
}

func (g *Gateway) activeStreamOf(userID string) *store.Stream {
	g.mu.Lock()
	defer g.mu.Unlock()
	for _, s := range g.streams {
		if s.rec.UserID == userID {
			rec := s.rec
			return &rec
		}
	}
	return nil
}

var errStreamGone = errors.New("this stream has ended")

func slicesSortByStart(s []LiveStream) {
	slices.SortFunc(s, func(a, b LiveStream) int { return cmp.Compare(a.Stream.StartedAt, b.Stream.StartedAt) })
}
