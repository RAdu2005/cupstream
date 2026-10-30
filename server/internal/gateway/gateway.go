// Package gateway is the media gateway: every WHIP (publish) and WHEP (play)
// request passes through it to MediaMTX, which listens on localhost only.
//
// The gateway decides access, gives each stream its own MediaMTX path
// (s/<streamId>) and records every session so it can pause or end it when
// access changes. It follows MediaMTX's event stream for live state,
// re-synchronizing with one list call after each (re)connection, and polls
// every two seconds when the event stream is unavailable.
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
	// While following events, timeouts are checked this often (no API calls).
	expireInterval = time.Second
	// How often a build without the event stream is checked for an upgrade.
	featuresInterval = 30 * time.Second
	// How long to poll after the event stream drops before reconnecting.
	eventsRetryDelay = 5 * time.Second
	// A stream whose publisher has not connected after this long is dropped.
	startTimeout = 30 * time.Second
	// A stream whose publisher disconnected is kept this long for reconnects.
	reconnectGrace = 15 * time.Second
	// A session MediaMTX has never listed is forgotten after this long.
	sessionHandshakeTimeout = 30 * time.Second
	// A viewer held because they lost access is disconnected after this long,
	// unless they get access back first.
	holdTimeout = 2 * time.Minute
	// Session events that arrive before the WHIP/WHEP answer are kept this long.
	earlyEventTTL = time.Minute
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
	stats streamStats
}

type session struct {
	mtxID     string // MediaMTX session ID, from the ID response header
	secret    string // from the Location header; empty for sessions adopted after a restart
	streamID  string
	publish   bool
	userID    string // viewer ("" for anonymous) or publisher
	tab       string // the viewer's browser tab, if it said (see CloseTabSessions)
	created   time.Time
	confirmed bool // seen in MediaMTX's session list or event stream

	// Viewers only.
	paused mediamtx.Tracks // tracks the viewer paused
	held   bool            // paused by the server: the viewer lost access
	heldAt time.Time
	stats  sessionStats
}

// watching reports whether a viewer receives media: not held, and not
// paused entirely (a pre-warmed or background session is not a viewer).
func (s *session) watching() bool {
	return !s.publish && !s.held && !(s.paused.Video && s.paused.Audio)
}

// earlyEvent is a session event that arrived before the gateway recorded
// the session: MediaMTX announces a session before answering its request.
type earlyEvent struct {
	at     time.Time
	closed bool
}

// LiveStream is a snapshot of a stream for API responses.
type LiveStream struct {
	Stream  store.Stream
	State   State
	Viewers int
	Stats   StreamStats
}

type Gateway struct {
	db        *store.DB
	mtx       *mediamtx.Client
	rooms     *rooms.Service
	publicURL *url.URL
	now       func() time.Time

	mu       sync.Mutex
	features mediamtx.Features
	streams  map[string]*liveStream // by stream ID
	sessions map[string]*session    // by MediaMTX session ID
	early    map[string]earlyEvent  // by MediaMTX session ID

	// changes is signalled when streams start, end or change state, or their
	// viewers change (see Changes).
	changes chan struct{}

	// pauseMu serializes pause and resume calls, which read and change a
	// session's pause state around a MediaMTX request.
	pauseMu sync.Mutex

	// OnRoomIdle, if set, is called after the last active stream of a room
	// ended. Set it before Run.
	OnRoomIdle func(ctx context.Context, roomID string)
}

func New(db *store.DB, mtx *mediamtx.Client, rs *rooms.Service, publicURL *url.URL) *Gateway {
	return &Gateway{
		db: db, mtx: mtx, rooms: rs, publicURL: publicURL, now: time.Now,
		streams:  map[string]*liveStream{},
		sessions: map[string]*session{},
		early:    map[string]earlyEvent{},
		changes:  make(chan struct{}, 1),
	}
}

// Changes is signalled after streams start, end or change state, and after
// their viewers change. Signals are coalesced: one receive may stand for
// several changes, so the receiver reads the current state (RoomStreams).
// Bitrates and other statistics change without a signal.
func (g *Gateway) Changes() <-chan struct{} { return g.changes }

func (g *Gateway) changed() {
	select {
	case g.changes <- struct{}{}:
	default:
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

// Features returns what the running MediaMTX build supports.
func (g *Gateway) Features() mediamtx.Features {
	g.mu.Lock()
	defer g.mu.Unlock()
	return g.features
}

// DetectFeatures asks MediaMTX for its version and enables the features of
// that build. It keeps the previous features when MediaMTX is unreachable.
func (g *Gateway) DetectFeatures(ctx context.Context) {
	info, err := g.mtx.Info(ctx)
	if err != nil {
		return
	}
	f := mediamtx.FeaturesOf(info.Version)
	g.mu.Lock()
	changed := f != g.features
	g.features = f
	g.mu.Unlock()
	if changed {
		slog.Info("MediaMTX features", "version", info.Version, "pause", f.Pause, "events", f.Events)
	}
}

// Run restores streams that were live before a restart and follows
// MediaMTX until ctx is done.
func (g *Gateway) Run(ctx context.Context) error {
	if err := g.Restore(ctx); err != nil {
		return err
	}
	for ctx.Err() == nil {
		g.DetectFeatures(ctx)
		if !g.Features().Events {
			g.poll(ctx, featuresInterval)
			continue
		}
		err := g.follow(ctx)
		if ctx.Err() != nil {
			break
		}
		slog.Warn("MediaMTX event stream ended; polling until it reconnects", "err", err)
		g.poll(ctx, eventsRetryDelay)
	}
	return nil
}

// poll reconciles every two seconds for d.
func (g *Gateway) poll(ctx context.Context, d time.Duration) {
	stop := time.After(d)
	t := time.NewTicker(reconcileInterval)
	defer t.Stop()
	for {
		if err := g.Reconcile(ctx); err != nil && ctx.Err() == nil {
			slog.Warn("media reconcile failed", "err", err)
		}
		select {
		case <-ctx.Done():
			return
		case <-stop:
			return
		case <-t.C:
		}
	}
}

// follow applies MediaMTX's events until the stream ends. Timeouts are
// checked locally and statistics refreshed while there are streams.
func (g *Gateway) follow(ctx context.Context) error {
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()

	synced := make(chan struct{})
	go func() {
		select {
		case <-ctx.Done():
			return
		case <-synced:
		}
		expire := time.NewTicker(expireInterval)
		defer expire.Stop()
		stats := time.NewTicker(reconcileInterval)
		defer stats.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-expire.C:
				g.expire(ctx)
			case <-stats.C:
				if err := g.RefreshStats(ctx); err != nil && ctx.Err() == nil {
					slog.Warn("media statistics failed", "err", err)
				}
			}
		}
	}()

	var once sync.Once
	return g.mtx.Events(ctx, func() {
		// Subscribed: list the current state once; later changes arrive as events.
		if err := g.Reconcile(ctx); err != nil && ctx.Err() == nil {
			slog.Warn("media reconcile failed", "err", err)
		}
		once.Do(func() { close(synced) })
	}, func(e mediamtx.Event) { g.HandleEvent(ctx, e) })
}

// Restore loads the streams that were active before a restart. They count as
// reconnecting until MediaMTX reports them online.
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
		g.changed()
	}
	return nil
}

type ending struct {
	id, reason string
	peak       int
}

// setOnlineLocked moves a stream between live and reconnecting.
func (g *Gateway) setOnlineLocked(s *liveStream, up bool, now time.Time) {
	switch {
	case up && s.state != Live:
		s.state, s.since = Live, now
	case !up && s.state == Live:
		s.state, s.since = Reconnecting, now
	default:
		return
	}
	g.changed()
}

// Reconcile re-synchronizes with MediaMTX's lists: streams go live or start
// their reconnect grace period, sessions are matched with MediaMTX's, and
// timeouts are applied.
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
	var kicks []string

	g.mu.Lock()
	for id, s := range g.streams {
		g.setOnlineLocked(s, online[mtxPath(id)], now)
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
			userID: q.Get("u"), tab: q.Get("t"), created: ms.Created, confirmed: true,
			// After a restart a held viewer looks paused by themselves; resuming
			// checks access again, so they cannot resume without it.
			paused: mediamtx.Tracks{Video: ms.VideoPaused, Audio: ms.AudioPaused},
		}
	}
	for id, sess := range g.sessions {
		if !present[id] && (sess.confirmed || now.Sub(sess.created) > sessionHandshakeTimeout) {
			delete(g.sessions, id)
		}
	}
	g.applyStatsLocked(paths, mtxSessions, now)
	g.updatePeaksLocked()
	g.mu.Unlock()

	for _, id := range kicks {
		slog.Info("closing unknown media session", "id", id)
		if err := g.mtx.Kick(ctx, id); err != nil {
			slog.Warn("kick failed", "id", id, "err", err)
		}
	}
	g.expire(ctx)
	return nil
}

// HandleEvent applies one event from MediaMTX's event stream.
func (g *Gateway) HandleEvent(ctx context.Context, e mediamtx.Event) {
	now := g.now()
	g.mu.Lock()
	defer g.mu.Unlock()
	switch e.Type {
	case mediamtx.EventStreamOnline, mediamtx.EventStreamOffline:
		id, ok := streamIDFromPath(e.Path)
		if s := g.streams[id]; ok && s != nil {
			g.setOnlineLocked(s, e.Type == mediamtx.EventStreamOnline, now)
		}
	case mediamtx.EventSessionCreated:
		id := e.SessionID()
		if sess, ok := g.sessions[id]; ok {
			sess.confirmed = true
		} else if id != "" {
			g.early[id] = earlyEvent{at: now}
		}
	case mediamtx.EventSessionClosed:
		id := e.SessionID()
		if _, ok := g.sessions[id]; ok {
			delete(g.sessions, id)
			g.updatePeaksLocked()
		} else if id != "" {
			g.early[id] = earlyEvent{at: now, closed: true}
		}
	}
}

// addSession records a session MediaMTX just created, unless its events
// already said that it closed.
func (g *Gateway) addSession(sess *session) {
	g.mu.Lock()
	defer g.mu.Unlock()
	if ev, ok := g.early[sess.mtxID]; ok {
		delete(g.early, sess.mtxID)
		if ev.closed {
			return
		}
		sess.confirmed = true
	}
	g.sessions[sess.mtxID] = sess
	g.updatePeaksLocked()
}

// expire ends streams that never started or did not reconnect in time,
// forgets sessions that never appeared and disconnects viewers held too long.
func (g *Gateway) expire(ctx context.Context) {
	now := g.now()
	var ends []ending
	var kicks []string

	g.mu.Lock()
	for id, s := range g.streams {
		switch {
		case s.state == Starting && now.Sub(s.since) > startTimeout:
			ends = append(ends, ending{id, EndFailedToStart, s.peak})
		case s.state == Reconnecting && now.Sub(s.since) > reconnectGrace:
			ends = append(ends, ending{id, EndDisconnected, s.peak})
		}
	}
	for id, sess := range g.sessions {
		switch {
		case !sess.confirmed && now.Sub(sess.created) > sessionHandshakeTimeout:
			delete(g.sessions, id)
		case sess.held && now.Sub(sess.heldAt) > holdTimeout:
			delete(g.sessions, id)
			kicks = append(kicks, id)
		}
	}
	for id, ev := range g.early {
		if now.Sub(ev.at) > earlyEventTTL {
			delete(g.early, id)
		}
	}
	g.mu.Unlock()

	for _, id := range kicks {
		if err := g.mtx.Kick(ctx, id); err != nil {
			slog.Warn("kick failed", "id", id, "err", err)
		}
	}
	for _, e := range ends {
		g.finish(ctx, e)
	}
}

func (g *Gateway) viewerCountsLocked() map[string]int {
	counts := map[string]int{}
	for _, s := range g.sessions {
		if s.watching() {
			counts[s.streamID]++
		}
	}
	return counts
}

// updatePeaksLocked records peak viewer counts after viewers changed.
func (g *Gateway) updatePeaksLocked() {
	g.changed()
	for id, n := range g.viewerCountsLocked() {
		if s := g.streams[id]; s != nil && n > s.peak {
			s.peak = n
		}
	}
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
	var roomID string
	if s := g.streams[e.id]; s != nil {
		roomID = s.rec.RoomID
	}
	delete(g.streams, e.id)
	// A replaced stream is followed by the user's new one at once.
	idle := roomID != "" && e.reason != EndReplaced
	for _, s := range g.streams {
		if s.rec.RoomID == roomID {
			idle = false
		}
	}
	var ids []string
	for id, sess := range g.sessions {
		if sess.streamID == e.id {
			ids = append(ids, id)
			delete(g.sessions, id)
		}
	}
	g.changed()
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
	if idle && g.OnRoomIdle != nil {
		g.OnRoomIdle(ctx, roomID)
	}
}

// Reevaluate applies a change to room's access rules: viewers who may no
// longer watch are held (paused, and disconnected after two minutes) and
// get their media back if they regain access in the meantime; streams whose
// streamer may no longer stream there are ended.
func (g *Gateway) Reevaluate(ctx context.Context, room store.Room) {
	type viewer struct {
		mtxID, userID string
		held          bool
	}
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
			viewers = append(viewers, viewer{id, sess.userID, sess.held})
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
		switch {
		case !ok && !v.held:
			g.hold(ctx, v.mtxID)
		case ok && v.held:
			g.release(ctx, v.mtxID)
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
			g.changed()
		}
	}
}

func (g *Gateway) kickSession(ctx context.Context, mtxID string) {
	g.mu.Lock()
	delete(g.sessions, mtxID)
	g.changed()
	g.mu.Unlock()
	if err := g.mtx.Kick(ctx, mtxID); err != nil {
		slog.Warn("kick failed", "id", mtxID, "err", err)
	}
}

// CloseTabSessions closes the media sessions that userID's browser tab
// opened, once that tab's realtime connection is gone, so that a viewer who
// closed the tab without saying so stops counting at once instead of when
// ICE times out. It returns how many sessions it closed.
func (g *Gateway) CloseTabSessions(ctx context.Context, userID, tab string) int {
	if tab == "" {
		return 0
	}
	var ids []string
	g.mu.Lock()
	for id, sess := range g.sessions {
		if !sess.publish && sess.tab == tab && sess.userID == userID {
			ids = append(ids, id)
		}
	}
	g.mu.Unlock()
	for _, id := range ids {
		g.kickSession(ctx, id)
	}
	return len(ids)
}

func (g *Gateway) snapshotLocked(id string, s *liveStream, counts map[string]int) LiveStream {
	return LiveStream{Stream: s.rec, State: s.state, Viewers: counts[id], Stats: s.stats.public()}
}

// RoomStreams lists the active streams of a room, oldest first.
func (g *Gateway) RoomStreams(roomID string) []LiveStream {
	g.mu.Lock()
	defer g.mu.Unlock()
	counts := g.viewerCountsLocked()
	var out []LiveStream
	for id, s := range g.streams {
		if s.rec.RoomID == roomID {
			out = append(out, g.snapshotLocked(id, s, counts))
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
	return g.snapshotLocked(streamID, s, g.viewerCountsLocked()), true
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
