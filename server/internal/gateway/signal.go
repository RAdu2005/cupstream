package gateway

import (
	"bytes"
	"context"
	"crypto/subtle"
	"database/sql"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"mime"
	"net"
	"net/http"
	"net/url"
	"path"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/RAdu2005/cupstream/server/internal/access"
	"github.com/RAdu2005/cupstream/server/internal/api"
	"github.com/RAdu2005/cupstream/server/internal/auth"
	"github.com/RAdu2005/cupstream/server/internal/mediamtx"
	"github.com/RAdu2005/cupstream/server/internal/store"
)

const (
	sdpType     = "application/sdp"
	trickleType = "application/trickle-ice-sdpfrag"
	maxSDP      = 64 << 10
)

var (
	reStreamID = regexp.MustCompile(`^[a-z2-7]{16}$`)
	reSecret   = regexp.MustCompile(`^[0-9a-f-]{36}$`)
	// ValidTab matches the browser tab IDs of the realtime WebSocket and WHEP
	// requests (?tab=).
	ValidTab = regexp.MustCompile(`^[A-Za-z0-9_-]{8,64}$`)
)

func writeErr(w http.ResponseWriter, status int, code, msg string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(api.ErrorResponse{Error: msg, Code: code})
}

func bearer(r *http.Request) string {
	h := r.Header.Get("Authorization")
	if t, ok := strings.CutPrefix(h, "Bearer "); ok {
		return strings.TrimSpace(t)
	}
	return ""
}

// HandleOBSPublish serves POST /whip: OBS publishes with its stream key as
// the bearer token. The destination follows the user's start mode, or the
// room given as ?room=<slug>.
func (g *Gateway) HandleOBSPublish(w http.ResponseWriter, r *http.Request) {
	key := bearer(r)
	if key == "" {
		writeErr(w, http.StatusUnauthorized, "", "set your CupStream stream key as the Bearer token")
		return
	}
	user, err := g.db.GetUserByStreamKey(r.Context(), auth.HashToken(key))
	if errors.Is(err, sql.ErrNoRows) {
		writeErr(w, http.StatusUnauthorized, "", "unknown or revoked stream key")
		return
	}
	if err != nil {
		g.internalError(w, r, err)
		return
	}

	started, err := g.Start(r.Context(), StartRequest{User: user, Source: SourceOBS, RoomSlug: r.URL.Query().Get("room")})
	if err != nil {
		g.WriteStartError(w, r, err)
		return
	}
	if !g.forwardOffer(w, r, started.Stream, true, user.ID) && !started.Reconnect {
		g.EndStream(r.Context(), started.Stream.ID, EndFailedToStart)
	}
}

// HandleTokenPublish serves POST /api/streams/{id}/whip for app and web
// senders, with the stream's publish token as the bearer token. Reconnects
// use the same URL and token.
func (g *Gateway) HandleTokenPublish(w http.ResponseWriter, r *http.Request) {
	ls, ok := g.Stream(r.PathValue("id"))
	if !ok {
		writeErr(w, http.StatusNotFound, "", errStreamGone.Error())
		return
	}
	rec := ls.Stream
	if rec.PublishTokenHash == nil || subtle.ConstantTimeCompare(auth.HashToken(bearer(r)), rec.PublishTokenHash) != 1 {
		writeErr(w, http.StatusUnauthorized, "", "invalid publish token")
		return
	}
	room, err := g.db.GetRoom(r.Context(), rec.RoomID)
	if err != nil {
		g.internalError(w, r, err)
		return
	}
	// Access may have changed since the stream was created.
	reason, err := g.rooms.Check(r.Context(), room, rec.UserID, access.Stream)
	if err != nil {
		g.internalError(w, r, err)
		return
	}
	if reason != access.OK {
		writeErr(w, http.StatusForbidden, string(reason), "you may no longer stream in this room")
		return
	}
	g.forwardOffer(w, r, rec, true, rec.UserID)
}

// Play serves POST /api/streams/{id}/whep for userID ("" when signed out).
func (g *Gateway) Play(w http.ResponseWriter, r *http.Request, userID string) {
	ls, ok := g.Stream(r.PathValue("id"))
	if !ok {
		writeErr(w, http.StatusNotFound, "", errStreamGone.Error())
		return
	}
	room, err := g.db.GetRoom(r.Context(), ls.Stream.RoomID)
	if err != nil {
		g.internalError(w, r, err)
		return
	}
	reason, err := g.rooms.Check(r.Context(), room, userID, access.Watch)
	if err != nil {
		g.internalError(w, r, err)
		return
	}
	if reason != access.OK {
		status := http.StatusForbidden
		if reason == access.SignInRequired {
			status = http.StatusUnauthorized
		}
		writeErr(w, status, string(reason), "you may not watch this stream")
		return
	}
	g.forwardOffer(w, r, ls.Stream, false, userID)
}

// playQuery reads ?paused=true, ?preview=false and ?tab=<id> from a WHEP
// request: a pre-warmed session opens paused, and without a preview frame it
// sends nothing until resumed. The tab is the browser tab's realtime
// connection, which the session ends with.
func playQuery(r *http.Request) (paused, noPreview bool, tab string, err error) {
	q := r.URL.Query()
	if tab = q.Get("tab"); tab != "" && !ValidTab.MatchString(tab) {
		return false, false, "", errors.New("tab is not a valid tab ID")
	}
	if v := q.Get("paused"); v != "" {
		if paused, err = strconv.ParseBool(v); err != nil {
			return false, false, "", errors.New("paused is true or false")
		}
	}
	if v := q.Get("preview"); v != "" {
		preview, err := strconv.ParseBool(v)
		if err != nil {
			return false, false, "", errors.New("preview is true or false")
		}
		noPreview = !preview
	}
	return paused, noPreview, tab, nil
}

// forwardOffer relays a WHIP/WHEP offer to MediaMTX, records the session and
// answers with a Location on this server. It reports whether it succeeded.
func (g *Gateway) forwardOffer(w http.ResponseWriter, r *http.Request, rec store.Stream, publish bool, userID string) bool {
	if mt, _, _ := mime.ParseMediaType(r.Header.Get("Content-Type")); mt != sdpType {
		writeErr(w, http.StatusUnsupportedMediaType, "", "the offer must be "+sdpType)
		return false
	}
	body, err := io.ReadAll(io.LimitReader(r.Body, maxSDP+1))
	if err != nil {
		writeErr(w, http.StatusBadRequest, "", "could not read the offer")
		return false
	}
	if len(body) > maxSDP {
		writeErr(w, http.StatusRequestEntityTooLarge, "", "offer too large")
		return false
	}
	offer := string(body)
	kind := "whep"
	query := url.Values{"u": {userID}}
	var paused Tracks
	var tab string
	if !publish {
		p, noPreview, t, err := playQuery(r)
		if err != nil {
			writeErr(w, http.StatusBadRequest, "", err.Error())
			return false
		}
		if tab = t; tab != "" {
			// Kept in MediaMTX's session list, so it survives a restart.
			query.Set("t", tab)
		}
		if !p && g.tooManyStreams(userID, tab, "") {
			writeErr(w, http.StatusConflict, CodeTooManyStreams, errTooManyStreams.Error())
			return false
		}
		if p {
			if !g.Features().Pause {
				writeErr(w, http.StatusNotImplemented, "pause_unsupported", errPauseUnsupported.Error())
				return false
			}
			paused = mediamtx.AllTracks
			query.Set("paused", "true")
			if noPreview {
				query.Set("preview", "false")
			}
		}
	}
	if publish {
		kind = "whip"
		if offer, err = filterPublishOffer(offer); err != nil {
			writeErr(w, http.StatusUnsupportedMediaType, "unsupported_codec", err.Error())
			return false
		}
	}

	header := http.Header{"Content-Type": {sdpType}, "X-Forwarded-For": {clientIP(r)}}
	signal := func() (*http.Response, error) {
		return g.mtx.Signal(r.Context(), http.MethodPost, mtxPath(rec.ID)+"/"+kind, query, header, strings.NewReader(offer))
	}
	var resp *http.Response
	if publish {
		resp, err = g.replacePublisher(r.Context(), rec.ID, signal)
	} else {
		resp, err = signal()
	}
	if err != nil {
		slog.ErrorContext(r.Context(), "mediamtx unreachable", "err", err)
		writeErr(w, http.StatusBadGateway, "", "the media server is unavailable")
		return false
	}
	defer resp.Body.Close()
	answer, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		writeErr(w, http.StatusBadGateway, "", "the media server did not answer")
		return false
	}
	if resp.StatusCode != http.StatusCreated {
		relayMediaError(w, resp.StatusCode, answer)
		return false
	}

	loc, err := url.Parse(resp.Header.Get("Location"))
	secret := ""
	if err == nil {
		secret = path.Base(loc.Path)
	}
	mtxID := resp.Header.Get("ID")
	if !reSecret.MatchString(secret) || mtxID == "" {
		slog.ErrorContext(r.Context(), "unexpected mediamtx answer", "location", resp.Header.Get("Location"), "id", mtxID)
		writeErr(w, http.StatusBadGateway, "", "unexpected answer from the media server")
		return false
	}

	g.addSession(&session{mtxID: mtxID, secret: secret, streamID: rec.ID, publish: publish, userID: userID, tab: tab, created: g.now(), paused: paused})

	h := w.Header()
	h.Set("Content-Type", sdpType)
	h.Set("Location", sessionURL(r, rec.ID, kind, secret))
	h.Set("Accept-Patch", trickleType)
	if v := resp.Header.Get("ETag"); v != "" {
		h.Set("ETag", v)
	}
	if v := resp.Header.Values("Link"); len(v) > 0 {
		h["Link"] = v
	}
	// A fixed length rather than chunked encoding: some WHIP clients
	// (ffmpeg's muxer, for one) cannot read chunked answers.
	h.Set("Content-Length", strconv.Itoa(len(answer)))
	w.WriteHeader(http.StatusCreated)
	w.Write(answer)
	return true
}

// publisherRetries bounds how long a new publisher waits for MediaMTX to
// let go of the one it replaces.
const publisherRetries = 10

// replacePublisher forwards a WHIP offer for a stream that may still have a
// publisher, such as OBS reconnecting before MediaMTX noticed that its old
// connection died. MediaMTX refuses a second publisher (overridePublisher
// is off, so nothing else can take over a path), so the old one is closed
// first, and again if MediaMTX still reports it.
func (g *Gateway) replacePublisher(ctx context.Context, streamID string, signal func() (*http.Response, error)) (*http.Response, error) {
	g.closePublishers(ctx, streamID, nil)
	for attempt := 1; ; attempt++ {
		resp, err := signal()
		if err != nil || resp.StatusCode != http.StatusBadRequest || attempt == publisherRetries {
			return resp, err
		}
		body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
		resp.Body.Close()
		if err != nil {
			return nil, err
		}
		if !strings.Contains(string(body), "already publishing") {
			resp.Body = io.NopCloser(bytes.NewReader(body))
			return resp, nil
		}
		mtxSessions, err := g.mtx.WebRTCSessions(ctx)
		if err != nil {
			return nil, err
		}
		g.closePublishers(ctx, streamID, mtxSessions)
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(time.Duration(attempt) * 50 * time.Millisecond):
		}
	}
}

// closePublishers closes the publishing sessions of a stream: the ones the
// gateway knows and, when given, the ones in MediaMTX's list.
func (g *Gateway) closePublishers(ctx context.Context, streamID string, listed []mediamtx.Session) {
	ids := map[string]bool{}
	g.mu.Lock()
	for id, sess := range g.sessions {
		if sess.streamID == streamID && sess.publish {
			ids[id] = true
			delete(g.sessions, id)
		}
	}
	g.mu.Unlock()
	for _, ms := range listed {
		if ms.Path == mtxPath(streamID) && ms.State == "publish" {
			ids[ms.ID] = true
		}
	}
	for id := range ids {
		slog.InfoContext(ctx, "closing the previous publisher", "stream", streamID, "id", id)
		if err := g.mtx.Kick(ctx, id); err != nil {
			slog.WarnContext(ctx, "kick failed", "id", id, "err", err)
		}
	}
}

// clientIP is the address of the client, passed to MediaMTX as
// X-Forwarded-For so that its logs and session list show real clients.
func clientIP(r *http.Request) string {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return host
}

// sessionURL is the absolute URL of a session resource on this server, built
// from the request so it matches the host the client used.
func sessionURL(r *http.Request, streamID, kind, secret string) string {
	scheme := "http"
	if r.TLS != nil || strings.EqualFold(r.Header.Get("X-Forwarded-Proto"), "https") {
		scheme = "https"
	}
	return (&url.URL{Scheme: scheme, Host: r.Host, Path: "/api/media/" + streamID + "/" + kind + "/" + secret}).String()
}

func relayMediaError(w http.ResponseWriter, status int, body []byte) {
	var e struct {
		Error string `json:"error"`
	}
	msg := "the media server refused the request"
	if json.Unmarshal(body, &e) == nil && e.Error != "" {
		msg = e.Error
	}
	if status >= 500 {
		status = http.StatusBadGateway
	}
	writeErr(w, status, "", msg)
}

// HandleSession serves PATCH and DELETE on /api/media/{stream}/{kind}/{secret}.
// The secret is a capability: only the client that created the session
// knows it. Only trickle-ICE PATCHes pass; pausing and resuming are for
// the server alone.
func (g *Gateway) HandleSession(w http.ResponseWriter, r *http.Request) {
	streamID, kind, secret := r.PathValue("stream"), r.PathValue("kind"), r.PathValue("secret")
	if !reStreamID.MatchString(streamID) || (kind != "whip" && kind != "whep") || !reSecret.MatchString(secret) {
		writeErr(w, http.StatusNotFound, "", "no such session")
		return
	}
	header := http.Header{}
	var body io.Reader
	if r.Method == http.MethodPatch {
		if mt, _, _ := mime.ParseMediaType(r.Header.Get("Content-Type")); mt != trickleType {
			writeErr(w, http.StatusUnsupportedMediaType, "", "only "+trickleType+" PATCH requests are accepted")
			return
		}
		header.Set("Content-Type", trickleType)
		if v := r.Header.Get("If-Match"); v != "" {
			header.Set("If-Match", v)
		}
		body = io.LimitReader(r.Body, maxSDP)
	}

	resp, err := g.mtx.Signal(r.Context(), r.Method, mtxPath(streamID)+"/"+kind+"/"+secret, nil, header, body)
	if err != nil {
		writeErr(w, http.StatusBadGateway, "", "the media server is unavailable")
		return
	}
	defer resp.Body.Close()
	out, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))

	if r.Method == http.MethodDelete && resp.StatusCode == http.StatusOK {
		if kind == "whip" {
			g.EndStream(context.WithoutCancel(r.Context()), streamID, EndStopped)
		} else {
			g.forgetSecret(secret)
		}
	}
	if resp.StatusCode >= 400 {
		relayMediaError(w, resp.StatusCode, out)
		return
	}
	for _, k := range []string{"Content-Type", "ETag"} {
		if v := resp.Header.Get(k); v != "" {
			w.Header().Set(k, v)
		}
	}
	w.Header().Set("Content-Length", strconv.Itoa(len(out)))
	w.WriteHeader(resp.StatusCode)
	w.Write(out)
}

// WriteStartError answers a failed Start with the matching status and code.
func (g *Gateway) WriteStartError(w http.ResponseWriter, r *http.Request, err error) {
	var live *AlreadyLiveError
	var dest *DestinationError
	switch {
	case errors.As(err, &live):
		writeErr(w, http.StatusConflict, "already_live", live.Error())
	case errors.As(err, &dest):
		status := http.StatusForbidden
		if dest.Code == CodeDiscordUnavailable || dest.Code == CodeRoomFull {
			status = http.StatusConflict
		}
		writeErr(w, status, dest.Code, dest.Message)
	default:
		g.internalError(w, r, err)
	}
}

func (g *Gateway) internalError(w http.ResponseWriter, r *http.Request, err error) {
	slog.ErrorContext(r.Context(), "gateway error", "err", err)
	writeErr(w, http.StatusInternalServerError, "", "internal error")
}
