package gateway

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"

	"github.com/RAdu2005/cupstream/server/internal/access"
	"github.com/RAdu2005/cupstream/server/internal/api"
	"github.com/RAdu2005/cupstream/server/internal/mediamtx"
)

// Pausing stops the media to one viewer while keeping their connection, so
// that it resumes in a fraction of a second. Viewers pause and resume their
// own sessions (background tiles, pre-warmed streams); the server holds
// viewers who lose access, and a held viewer cannot resume. Builds without
// pausing fall back to closing sessions.

var (
	errNoSession        = errors.New("no such session")
	errPauseUnsupported = errors.New("the media server cannot pause sessions")
)

// accessError means the viewer may no longer watch; their session is held.
type accessError struct{ reason access.Reason }

func (e *accessError) Error() string { return "you may no longer watch this stream" }

// or returns the tracks selected in either a or b.
func or(a, b mediamtx.Tracks) mediamtx.Tracks {
	return mediamtx.Tracks{Video: a.Video || b.Video, Audio: a.Audio || b.Audio}
}

// minus returns the tracks selected in a but not in b.
func minus(a, b mediamtx.Tracks) mediamtx.Tracks {
	return mediamtx.Tracks{Video: a.Video && !b.Video, Audio: a.Audio && !b.Audio}
}

// Tracks selects video, audio or both.
type Tracks = mediamtx.Tracks

func none(t Tracks) bool { return !t.Video && !t.Audio }

// effective returns the tracks MediaMTX currently holds back.
func (s *session) effective() Tracks {
	if s.held {
		return mediamtx.AllTracks
	}
	return s.paused
}

// applyPause brings MediaMTX from the paused tracks before to after. Tracks
// that are already in the right state are left alone: resuming a playing
// track would make it wait for a key frame.
func (g *Gateway) applyPause(ctx context.Context, mtxID string, before, after Tracks) error {
	if t := minus(after, before); !none(t) {
		if err := g.mtx.Pause(ctx, mtxID, t); err != nil {
			return err
		}
	}
	if t := minus(before, after); !none(t) {
		if err := g.mtx.Resume(ctx, mtxID, t); err != nil {
			return err
		}
	}
	return nil
}

// viewerSession finds a viewer's session by its secret.
func (g *Gateway) viewerSession(streamID, secret string) (session, bool) {
	g.mu.Lock()
	defer g.mu.Unlock()
	for _, sess := range g.sessions {
		if sess.secret == secret && sess.streamID == streamID && !sess.publish {
			return *sess, true
		}
	}
	return session{}, false
}

// SetViewerPaused pauses or resumes tracks of a viewer's own session.
// Resuming checks access again: a viewer who lost it is held instead.
func (g *Gateway) SetViewerPaused(ctx context.Context, streamID, secret string, t Tracks, paused bool) error {
	if !g.Features().Pause {
		return errPauseUnsupported
	}
	sess, ok := g.viewerSession(streamID, secret)
	if !ok {
		return errNoSession
	}

	if !paused {
		ls, ok := g.Stream(streamID)
		if !ok {
			return errNoSession
		}
		room, err := g.db.GetRoom(ctx, ls.Stream.RoomID)
		if err != nil {
			return err
		}
		reason, err := g.rooms.Check(ctx, room, sess.userID, access.Watch)
		if err != nil {
			return err
		}
		if reason != access.OK {
			g.hold(ctx, sess.mtxID)
			return &accessError{reason}
		}
	}

	g.pauseMu.Lock()
	defer g.pauseMu.Unlock()
	g.mu.Lock()
	cur, ok := g.sessions[sess.mtxID]
	if !ok {
		g.mu.Unlock()
		return errNoSession
	}
	before := cur.effective()
	next := minus(cur.paused, t)
	if paused {
		next = or(cur.paused, t)
	}
	after := next
	if cur.held && paused {
		after = mediamtx.AllTracks
	}
	// Resuming a session that sent nothing makes it one more stream.
	if !paused && !cur.watching() && !(after.Video && after.Audio) && g.watchingLocked(cur.userID, cur.tab, cur.mtxID) >= MaxWatching {
		g.mu.Unlock()
		return errTooManyStreams
	}
	g.mu.Unlock()
	if !paused && t.Video && before.Video && !g.admit(streamID) {
		return errServerFull
	}

	if err := g.applyPause(ctx, sess.mtxID, before, after); err != nil {
		if errors.Is(err, mediamtx.ErrNotFound) {
			g.forgetSecret(secret)
			return errNoSession
		}
		return err
	}

	g.mu.Lock()
	if cur, ok := g.sessions[sess.mtxID]; ok {
		cur.paused = next
		if !paused {
			cur.held = false // access was checked above
		}
		g.updatePeaksLocked()
	}
	g.mu.Unlock()
	return nil
}

// hold pauses all of a viewer's media because they lost access; without
// pausing, the session is closed instead.
func (g *Gateway) hold(ctx context.Context, mtxID string) {
	if !g.Features().Pause {
		g.kickSession(ctx, mtxID)
		return
	}
	g.pauseMu.Lock()
	defer g.pauseMu.Unlock()
	g.mu.Lock()
	sess, ok := g.sessions[mtxID]
	if !ok || sess.held {
		g.mu.Unlock()
		return
	}
	before := sess.effective()
	g.mu.Unlock()

	if err := g.applyPause(ctx, mtxID, before, mediamtx.AllTracks); err != nil {
		slog.Warn("holding a viewer failed; closing the session", "id", mtxID, "err", err)
		g.kickSession(ctx, mtxID)
		return
	}
	g.mu.Lock()
	if sess, ok := g.sessions[mtxID]; ok {
		sess.held, sess.heldAt = true, g.now()
		g.changed()
	}
	g.mu.Unlock()
}

// release gives a held viewer their media back, except the tracks they
// paused themselves.
func (g *Gateway) release(ctx context.Context, mtxID string) {
	g.pauseMu.Lock()
	defer g.pauseMu.Unlock()
	g.mu.Lock()
	sess, ok := g.sessions[mtxID]
	if !ok || !sess.held {
		g.mu.Unlock()
		return
	}
	paused := sess.paused
	g.mu.Unlock()

	if err := g.applyPause(ctx, mtxID, mediamtx.AllTracks, paused); err != nil {
		slog.Warn("releasing a viewer failed", "id", mtxID, "err", err)
		return
	}
	g.mu.Lock()
	if sess, ok := g.sessions[mtxID]; ok {
		sess.held = false
		g.updatePeaksLocked()
	}
	g.mu.Unlock()
}

// HandleSessionControl serves POST /api/media/{stream}/whep/{secret}/pause
// and .../resume. The optional body {"video": bool, "audio": bool} selects
// the tracks; without it, both are selected. Like PATCH and DELETE, the
// secret in the URL authorizes the request.
func (g *Gateway) HandleSessionControl(w http.ResponseWriter, r *http.Request) {
	streamID, secret, action := r.PathValue("stream"), r.PathValue("secret"), r.PathValue("action")
	if !reStreamID.MatchString(streamID) || !reSecret.MatchString(secret) || (action != "pause" && action != "resume") {
		writeErr(w, http.StatusNotFound, "", "no such session")
		return
	}
	t := mediamtx.AllTracks
	var body api.PauseTracks
	dec := json.NewDecoder(io.LimitReader(r.Body, 1024))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&body); err != nil && !errors.Is(err, io.EOF) {
		writeErr(w, http.StatusBadRequest, "", "the body is {\"video\": bool, \"audio\": bool}")
		return
	}
	if body.Video != nil || body.Audio != nil {
		t = mediamtx.Tracks{Video: body.Video != nil && *body.Video, Audio: body.Audio != nil && *body.Audio}
	}

	var ae *accessError
	switch err := g.SetViewerPaused(r.Context(), streamID, secret, t, action == "pause"); {
	case err == nil:
		w.WriteHeader(http.StatusNoContent)
	case errors.Is(err, errNoSession):
		writeErr(w, http.StatusNotFound, "", err.Error())
	case errors.Is(err, errTooManyStreams):
		writeErr(w, http.StatusConflict, CodeTooManyStreams, err.Error())
	case errors.Is(err, errPauseUnsupported):
		writeErr(w, http.StatusNotImplemented, "pause_unsupported", err.Error())
	case errors.Is(err, errServerFull):
		writeServerFull(w)
	case errors.As(err, &ae):
		writeErr(w, http.StatusForbidden, string(ae.reason), ae.Error())
	default:
		g.internalError(w, r, err)
	}
}
