// Package mtxtest is a fake MediaMTX for tests: the control API endpoints and
// the WHIP/WHEP signaling that CupStream uses, without any media.
package mtxtest

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/RAdu2005/cupstream/server/internal/mediamtx"
)

type Session struct {
	ID, Secret, Path, State, Query string
	Offer                          string
	Patches                        []string // Content-Type of each PATCH
	VideoPaused, AudioPaused       bool
	// Counters reported in the session list.
	InboundBytes, InboundRTPPackets, InboundRTPPacketsLost, OutboundBytes uint64
	OutboundLossRatio                                                     float64
	OutboundRTT                                                           float64 // seconds
}

// Call is a pause or resume request.
type Call struct {
	Action, ID   string
	Video, Audio bool
}

type Fake struct {
	*httptest.Server
	URL *url.URL
	// Version is reported by /v3/info; it decides the features CupStream uses.
	Version string

	mu        sync.Mutex
	sessions  map[string]*Session // by ID
	kicked    []string
	calls     []Call
	keyFrames map[string]float64 // key frame interval by path
	subs      map[chan string]struct{}
}

func New(t *testing.T) *Fake {
	f := &Fake{
		Version:   "v1.21.2-cupstream.2",
		sessions:  map[string]*Session{},
		keyFrames: map[string]float64{},
		subs:      map[chan string]struct{}{},
	}
	f.Server = httptest.NewServer(http.HandlerFunc(f.serve))
	t.Cleanup(func() {
		f.DisconnectEvents()
		f.Close()
	})
	f.URL, _ = url.Parse(f.Server.URL)
	return f
}

func (f *Fake) serve(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path == "/v3/events" {
		f.events(w, r)
		return
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	p := r.URL.Path
	switch {
	case p == "/v3/info":
		json.NewEncoder(w).Encode(map[string]any{"version": f.Version})
	case p == "/v3/paths/list":
		items := []map[string]any{}
		for name := range f.onlinePathsLocked() {
			item := map[string]any{"name": name, "online": true, "keyFrameInterval": nil}
			if v, ok := f.keyFrames[name]; ok {
				item["keyFrameInterval"] = v
			}
			items = append(items, item)
		}
		json.NewEncoder(w).Encode(map[string]any{"items": items})
	case p == "/v3/webrtc/sessions/list":
		items := []map[string]any{}
		for _, s := range f.sessions {
			items = append(items, map[string]any{
				"id": s.ID, "path": s.Path, "state": s.State, "query": s.Query, "created": time.Now(),
				"videoPaused": s.VideoPaused, "audioPaused": s.AudioPaused,
				"paused":                      s.VideoPaused && s.AudioPaused,
				"inboundBytes":                s.InboundBytes,
				"inboundRTPPackets":           s.InboundRTPPackets,
				"inboundRTPPacketsLost":       s.InboundRTPPacketsLost,
				"outboundBytes":               s.OutboundBytes,
				"outboundRTPPacketsLossRatio": s.OutboundLossRatio,
				"outboundRoundTripTime":       s.OutboundRTT,
			})
		}
		json.NewEncoder(w).Encode(map[string]any{"items": items})
	case strings.HasPrefix(p, "/v3/webrtc/sessions/kick/"):
		id := strings.TrimPrefix(p, "/v3/webrtc/sessions/kick/")
		f.kicked = append(f.kicked, id)
		s, ok := f.sessions[id]
		if !ok {
			notFound(w)
			return
		}
		f.closeLocked(s)
	case strings.HasPrefix(p, "/v3/webrtc/sessions/pause/"), strings.HasPrefix(p, "/v3/webrtc/sessions/resume/"):
		f.setPausedLocked(w, r)
	default:
		f.signal(w, r)
	}
}

func notFound(w http.ResponseWriter) {
	w.WriteHeader(http.StatusNotFound)
	io.WriteString(w, `{"status":"error","error":"session not found"}`)
}

func (f *Fake) setPausedLocked(w http.ResponseWriter, r *http.Request) {
	if !mediamtx.FeaturesOf(f.Version).Pause {
		// Builds before 2 have no such endpoints.
		http.NotFound(w, r)
		return
	}
	parts := strings.Split(r.URL.Path, "/") // "", v3, webrtc, sessions, action, id
	action, id := parts[4], parts[5]
	t := struct{ Video, Audio *bool }{}
	video, audio := true, true
	if err := json.NewDecoder(r.Body).Decode(&t); err == nil && (t.Video != nil || t.Audio != nil) {
		video, audio = t.Video != nil && *t.Video, t.Audio != nil && *t.Audio
	}
	s, ok := f.sessions[id]
	if !ok {
		notFound(w)
		return
	}
	if s.State != "read" {
		w.WriteHeader(http.StatusBadRequest)
		io.WriteString(w, `{"status":"error","error":"session is not reading"}`)
		return
	}
	f.calls = append(f.calls, Call{Action: action, ID: id, Video: video, Audio: audio})
	if video {
		s.VideoPaused = action == "pause"
	}
	if audio {
		s.AudioPaused = action == "pause"
	}
	io.WriteString(w, `{"status":"ok"}`)
}

func (f *Fake) signal(w http.ResponseWriter, r *http.Request) {
	parts := strings.Split(strings.Trim(r.URL.Path, "/"), "/") // s, <id>, whip|whep[, secret]
	if len(parts) < 3 || parts[0] != "s" {
		http.NotFound(w, r)
		return
	}
	path, kind := parts[0]+"/"+parts[1], parts[2]
	if len(parts) == 3 && r.Method == http.MethodPost {
		body, _ := io.ReadAll(r.Body)
		s := &Session{ID: uuid.NewString(), Secret: uuid.NewString(), Path: path, State: "read", Query: r.URL.RawQuery, Offer: string(body)}
		if kind == "whip" {
			s.State = "publish"
			// Like MediaMTX with overridePublisher off.
			if f.onlinePathsLocked()[path] {
				w.WriteHeader(http.StatusBadRequest)
				fmt.Fprintf(w, `{"status":"error","error":"someone is already publishing to path '%s'"}`, path)
				return
			}
		} else if !f.onlinePathsLocked()[path] {
			w.WriteHeader(http.StatusNotFound)
			io.WriteString(w, `{"status":"error","error":"no stream is available on path"}`)
			return
		} else if r.URL.Query().Get("paused") == "true" {
			s.VideoPaused, s.AudioPaused = true, true
		}
		f.sessions[s.ID] = s
		// Like MediaMTX, the session is announced before the answer.
		f.emitLocked("session.created", map[string]any{"path": path, "query": s.Query, "state": s.State,
			"reader": map[string]any{"type": "webRTCSession", "id": s.ID}})
		if s.State == "publish" {
			f.emitLocked("stream.online", map[string]any{"path": path})
		}
		w.Header().Set("Location", fmt.Sprintf("/%s/%s/%s?%s", path, kind, s.Secret, r.URL.RawQuery))
		w.Header().Set("ID", s.ID)
		w.Header().Set("ETag", "*")
		w.Header().Set("Content-Type", "application/sdp")
		w.WriteHeader(http.StatusCreated)
		io.WriteString(w, "v=0\r\no=answer\r\n")
		return
	}
	if len(parts) != 4 {
		http.NotFound(w, r)
		return
	}
	var s *Session
	for _, c := range f.sessions {
		if c.Secret == parts[3] {
			s = c
		}
	}
	if s == nil {
		notFound(w)
		return
	}
	switch r.Method {
	case http.MethodPatch:
		s.Patches = append(s.Patches, r.Header.Get("Content-Type"))
		w.WriteHeader(http.StatusNoContent)
	case http.MethodDelete:
		f.closeLocked(s)
		w.WriteHeader(http.StatusOK)
	default:
		w.WriteHeader(http.StatusMethodNotAllowed)
	}
}

// closeLocked closes a session; like MediaMTX, closing the publisher closes
// the path's readers.
func (f *Fake) closeLocked(s *Session) {
	if _, ok := f.sessions[s.ID]; !ok {
		return
	}
	delete(f.sessions, s.ID)
	f.emitLocked("session.closed", map[string]any{"path": s.Path, "state": s.State,
		"reader": map[string]any{"type": "webRTCSession", "id": s.ID}})
	if s.State == "publish" {
		f.emitLocked("stream.offline", map[string]any{"path": s.Path})
		for _, o := range f.sessions {
			if o.Path == s.Path {
				f.closeLocked(o)
			}
		}
	}
}

func (f *Fake) onlinePathsLocked() map[string]bool {
	online := map[string]bool{}
	for _, s := range f.sessions {
		if s.State == "publish" {
			online[s.Path] = true
		}
	}
	return online
}

func (f *Fake) emitLocked(typ string, fields map[string]any) {
	fields["type"] = typ
	fields["time"] = time.Now()
	b, _ := json.Marshal(fields)
	msg := fmt.Sprintf("event: %s\ndata: %s\n\n", typ, b)
	for c := range f.subs {
		select {
		case c <- msg:
		default:
		}
	}
}

func (f *Fake) events(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	if !mediamtx.FeaturesOf(f.Version).Pause {
		f.mu.Unlock()
		http.NotFound(w, r)
		return
	}
	c := make(chan string, 100)
	f.subs[c] = struct{}{}
	f.mu.Unlock()

	w.Header().Set("Content-Type", "text/event-stream")
	io.WriteString(w, ": comment\n\nevent: ready\ndata: {}\n\n")
	w.(http.Flusher).Flush()
	for {
		select {
		case msg, ok := <-c:
			if !ok {
				return
			}
			io.WriteString(w, msg)
			w.(http.Flusher).Flush()
		case <-r.Context().Done():
			f.mu.Lock()
			delete(f.subs, c)
			f.mu.Unlock()
			return
		}
	}
}

// DisconnectEvents ends every event stream, as a MediaMTX restart would.
func (f *Fake) DisconnectEvents() {
	f.mu.Lock()
	defer f.mu.Unlock()
	for c := range f.subs {
		close(c)
		delete(f.subs, c)
	}
}

// EventSubscribers returns the number of open event streams.
func (f *Fake) EventSubscribers() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.subs)
}

// SetVersion changes the reported version.
func (f *Fake) SetVersion(v string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.Version = v
}

// Sessions returns copies of the open sessions on path.
func (f *Fake) Sessions(path string) []Session {
	f.mu.Lock()
	defer f.mu.Unlock()
	var out []Session
	for _, s := range f.sessions {
		if s.Path == path {
			out = append(out, *s)
		}
	}
	return out
}

// Session returns a copy of a session.
func (f *Fake) Session(id string) (Session, bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	s, ok := f.sessions[id]
	if !ok {
		return Session{}, false
	}
	return *s, true
}

// Update changes a session, e.g. its counters.
func (f *Fake) Update(id string, change func(*Session)) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if s, ok := f.sessions[id]; ok {
		change(s)
	}
}

// SetKeyFrameInterval sets the key frame interval reported for a path.
func (f *Fake) SetKeyFrameInterval(path string, seconds float64) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.keyFrames[path] = seconds
}

// Kicked returns the IDs of kicked sessions.
func (f *Fake) Kicked() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string(nil), f.kicked...)
}

// Calls returns the pause and resume requests.
func (f *Fake) Calls() []Call {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]Call(nil), f.calls...)
}

// Drop closes a session as if the client went away.
func (f *Fake) Drop(id string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if s, ok := f.sessions[id]; ok {
		f.closeLocked(s)
	}
}

// DropSilently removes a session without telling event subscribers, as if
// MediaMTX's events were lost.
func (f *Fake) DropSilently(id string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	delete(f.sessions, id)
}
