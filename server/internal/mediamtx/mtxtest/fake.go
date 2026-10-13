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
)

type Session struct {
	ID, Secret, Path, State, Query string
	Offer                          string
	Patches                        []string // Content-Type of each PATCH
}

type Fake struct {
	*httptest.Server
	URL *url.URL

	mu       sync.Mutex
	sessions map[string]*Session // by ID
	kicked   []string
}

func New(t *testing.T) *Fake {
	f := &Fake{sessions: map[string]*Session{}}
	f.Server = httptest.NewServer(http.HandlerFunc(f.serve))
	t.Cleanup(f.Close)
	f.URL, _ = url.Parse(f.Server.URL)
	return f
}

func (f *Fake) serve(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	defer f.mu.Unlock()
	p := r.URL.Path
	switch {
	case p == "/v3/info":
		io.WriteString(w, `{"version":"v1.21.2-cupstream.1"}`)
	case p == "/v3/paths/list":
		online := map[string]bool{}
		for _, s := range f.sessions {
			if s.State == "publish" {
				online[s.Path] = true
			}
		}
		items := []map[string]any{}
		for name := range online {
			items = append(items, map[string]any{"name": name, "online": true})
		}
		json.NewEncoder(w).Encode(map[string]any{"items": items})
	case p == "/v3/webrtcsessions/list":
		items := []map[string]any{}
		for _, s := range f.sessions {
			items = append(items, map[string]any{
				"id": s.ID, "path": s.Path, "state": s.State, "query": s.Query, "created": time.Now(),
			})
		}
		json.NewEncoder(w).Encode(map[string]any{"items": items})
	case strings.HasPrefix(p, "/v3/webrtcsessions/kick/"):
		id := strings.TrimPrefix(p, "/v3/webrtcsessions/kick/")
		f.kicked = append(f.kicked, id)
		if s, ok := f.sessions[id]; ok && s.State == "publish" {
			// Like MediaMTX: closing the publisher closes the path's readers.
			for rid, rs := range f.sessions {
				if rs.Path == s.Path {
					delete(f.sessions, rid)
				}
			}
		}
		delete(f.sessions, id)
	default:
		f.signal(w, r)
	}
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
			// Like MediaMTX's overridePublisher: a new publisher replaces the old.
			for id, o := range f.sessions {
				if o.Path == path && o.State == "publish" {
					delete(f.sessions, id)
				}
			}
		} else if !f.onlineLocked(path) {
			w.WriteHeader(http.StatusNotFound)
			io.WriteString(w, `{"status":"error","error":"no stream is available on path"}`)
			return
		}
		f.sessions[s.ID] = s
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
		w.WriteHeader(http.StatusNotFound)
		io.WriteString(w, `{"status":"error","error":"session not found"}`)
		return
	}
	switch r.Method {
	case http.MethodPatch:
		s.Patches = append(s.Patches, r.Header.Get("Content-Type"))
		w.WriteHeader(http.StatusNoContent)
	case http.MethodDelete:
		delete(f.sessions, s.ID)
		if s.State == "publish" {
			for id, o := range f.sessions {
				if o.Path == s.Path {
					delete(f.sessions, id)
				}
			}
		}
		w.WriteHeader(http.StatusOK)
	default:
		w.WriteHeader(http.StatusMethodNotAllowed)
	}
}

func (f *Fake) onlineLocked(path string) bool {
	for _, s := range f.sessions {
		if s.Path == path && s.State == "publish" {
			return true
		}
	}
	return false
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

// Kicked returns the IDs of kicked sessions.
func (f *Fake) Kicked() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string(nil), f.kicked...)
}

// Drop closes a session as if the client went away.
func (f *Fake) Drop(id string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	delete(f.sessions, id)
}
