package gateway

import (
	"errors"
	"log/slog"
	"net/http"
)

// Near the server's bandwidth limit, new viewers are refused (overview 9.3):
// opening a session that plays at once, and resuming the video of a paused
// one (a pre-warmed stream or a background tile). Paused sessions cost
// nothing, so pre-warming is always allowed.

var errServerFull = errors.New("the server is full right now; try again in a few minutes")

// Admission decides whether one more viewer of a stream at bitrate bits per
// second (0 when not known yet) fits; see package capacity.
type Admission interface {
	Admit(bitrate int64) bool
}

// admit reports whether another viewer of the stream fits. Call it after
// the viewer's other checks, since an admitted viewer reserves bandwidth.
func (g *Gateway) admit(streamID string) bool {
	if g.Admission == nil {
		return true
	}
	ls, ok := g.Stream(streamID)
	if !ok || g.Admission.Admit(ls.Stats.Bitrate) {
		return true // a stream that ended is reported as such by the caller
	}
	slog.Warn("refused a viewer: the server is at its bandwidth limit", "stream", streamID, "bitrate", ls.Stats.Bitrate)
	return false
}

func writeServerFull(w http.ResponseWriter) {
	w.Header().Set("Retry-After", "60")
	writeErr(w, http.StatusServiceUnavailable, "server_full", errServerFull.Error())
}
