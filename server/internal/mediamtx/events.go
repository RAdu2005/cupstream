package mediamtx

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"time"
)

// Features are what the running MediaMTX build supports beyond upstream.
type Features struct {
	// Pause: pausing and resuming viewer sessions through the API.
	Pause bool
	// Events: the GET /v3/events stream.
	Events bool
}

// The suffix after the build number is what `git describe` adds to
// development builds made after a tag.
var reCupStreamBuild = regexp.MustCompile(`-cupstream\.(\d+)(-.*)?$`)

// FeaturesOf derives the features from the version MediaMTX reports. Our
// builds are tagged vX.Y.Z-cupstream.N; pausing through the API and the
// event stream arrived in build 2. Anything else gets the fallbacks:
// closing sessions instead of pausing them, and polling.
func FeaturesOf(version string) Features {
	m := reCupStreamBuild.FindStringSubmatch(version)
	if m == nil {
		return Features{}
	}
	n, _ := strconv.Atoi(m[1])
	return Features{Pause: n >= 2, Events: n >= 2}
}

// Event types CupStream acts on; MediaMTX sends others too.
const (
	EventStreamOnline   = "stream.online"
	EventStreamOffline  = "stream.offline"
	EventSessionCreated = "session.created"
	EventSessionClosed  = "session.closed"
)

type Event struct {
	Type  string    `json:"type"`
	Time  time.Time `json:"time"`
	Path  string    `json:"path"`
	Query string    `json:"query"`
	// Reader is the session of session events.
	Reader *struct {
		Type string `json:"type"`
		ID   string `json:"id"`
	} `json:"reader"`
	State string `json:"state"` // session events: "publish" or "read"
}

// SessionID returns the session ID of a session event.
func (e Event) SessionID() string {
	if e.Reader == nil {
		return ""
	}
	return e.Reader.ID
}

// ErrOverflow means CupStream did not read events fast enough and MediaMTX
// ended the stream; reconnect and re-synchronize.
var ErrOverflow = errors.New("mediamtx: event stream overflowed")

// eventsIdleTimeout ends a stream that went silent: MediaMTX sends a
// keepalive every 15 seconds.
const eventsIdleTimeout = 45 * time.Second

// Events follows MediaMTX's event stream until ctx is done or the stream
// ends. ready is called once subscribed, before any event: changes after
// that point are delivered, so the caller lists the current state then.
func (c *Client) Events(ctx context.Context, ready func(), handle func(Event)) error {
	ctx, cancel := context.WithCancelCause(ctx)
	defer cancel(nil)

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.api.JoinPath("/v3/events").String(), nil)
	if err != nil {
		return err
	}
	req.Header.Set("Accept", "text/event-stream")
	resp, err := c.stream.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("mediamtx /v3/events: %s", resp.Status)
	}

	errIdle := errors.New("mediamtx: event stream went silent")
	idle := time.AfterFunc(eventsIdleTimeout, func() { cancel(errIdle) })
	defer idle.Stop()

	sc := bufio.NewScanner(resp.Body)
	sc.Buffer(make([]byte, 64<<10), 1<<20)
	var name, data string
	for sc.Scan() {
		idle.Reset(eventsIdleTimeout)
		line := sc.Text()
		switch {
		case line == "":
			// End of an event.
			switch name {
			case "":
			case "ready":
				ready()
			case "overflow":
				return ErrOverflow
			default:
				var e Event
				if err := json.Unmarshal([]byte(data), &e); err == nil {
					handle(e)
				}
			}
			name, data = "", ""
		case strings.HasPrefix(line, ":"):
			// Comment (keepalive).
		case strings.HasPrefix(line, "event:"):
			name = strings.TrimSpace(strings.TrimPrefix(line, "event:"))
		case strings.HasPrefix(line, "data:"):
			data += strings.TrimPrefix(strings.TrimPrefix(line, "data:"), " ")
		}
	}
	if cause := context.Cause(ctx); cause != nil && ctx.Err() != nil {
		return cause
	}
	if err := sc.Err(); err != nil {
		return err
	}
	return errors.New("mediamtx: event stream ended")
}
