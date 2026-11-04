package gateway

import (
	"errors"
	"net/http"
	"strings"
)

// Low-quality versions (overview 7.3, docs/low-quality.md). A stream can
// have a second, low-quality version for viewers on slow connections: the
// desktop app publishes one as a second WHIP session on the stream's low
// path, s/<streamId>/low. It belongs to the stream: it cannot be published
// without the stream's own publisher, it ends with the stream, and it can
// end alone without ending the stream. For streams without one, the server
// can transcode one while someone watches it (transcode.go). Viewers pick
// either with ?quality=low.

// Variant is a version of a stream, with a MediaMTX path of its own.
type Variant string

const (
	// VariantMain is what the streamer sends: s/<streamId>.
	VariantMain Variant = ""
	// VariantLow is the low-quality version the sender publishes:
	// s/<streamId>/low.
	VariantLow Variant = "low"
	// VariantTranscode is the low-quality version the server transcodes
	// with FFmpeg while someone watches it: s/<streamId>/transcode.
	VariantTranscode Variant = "transcode"
)

// LowQuality says what a viewer who picks the low-quality version gets.
type LowQuality string

const (
	LowNone       LowQuality = "none"       // no low-quality version
	LowSent       LowQuality = "sent"       // the sender publishes one
	LowTranscoded LowQuality = "transcoded" // the server transcodes one on demand
)

// LowDefaultBitrate is assumed for a low-quality version whose bitrate is
// not known yet, in bits per second.
const LowDefaultBitrate = 1_500_000

// Error codes of low-quality requests.
const (
	// CodeLowQualityOff refuses a sender's low-quality version while the
	// server does not accept them (CUPSTREAM_LOW_QUALITY=off).
	CodeLowQualityOff = "low_quality_off"
	// CodeNoMainSession refuses a low-quality version published before the
	// stream itself.
	CodeNoMainSession = "no_main_session"
	// CodeLowQualityUnavailable refuses a viewer who asks for a low-quality
	// version the stream does not have.
	CodeLowQualityUnavailable = "low_quality_unavailable"
)

func mtxPath(streamID string, v Variant) string {
	if v == VariantMain {
		return "s/" + streamID
	}
	return "s/" + streamID + "/" + string(v)
}

// parsePath splits a MediaMTX path into its stream and variant.
func parsePath(p string) (streamID string, v Variant, ok bool) {
	rest, ok := strings.CutPrefix(p, "s/")
	if !ok {
		return "", "", false
	}
	streamID, suffix, hasSuffix := strings.Cut(rest, "/")
	if !hasSuffix {
		return streamID, VariantMain, true
	}
	if v = Variant(suffix); v != VariantLow && v != VariantTranscode {
		return "", "", false
	}
	return streamID, v, true
}

// variantParam reads ?quality= from a WHIP or WHEP request: empty or
// "source" for the stream itself, "low" for its low-quality version.
func variantParam(r *http.Request) (Variant, error) {
	switch q := r.URL.Query().Get("quality"); q {
	case "", "source":
		return VariantMain, nil
	case "low":
		return VariantLow, nil
	}
	return "", errors.New("quality is source or low")
}

// sessionVariant reads the variant from a session URL, /api/media/{stream}[/{variant}]/...
func sessionVariant(r *http.Request) (Variant, bool) {
	switch v := Variant(r.PathValue("variant")); v {
	case VariantMain, VariantLow, VariantTranscode:
		return v, true
	}
	return "", false
}

// lowQualityLocked says what the stream's viewers get if they pick the
// low-quality version: the sender's, or else a transcode if one may run.
// running is transcodingLocked's result.
func (g *Gateway) lowQualityLocked(id string, s *liveStream, running map[string]bool) LowQuality {
	switch {
	case !g.LowEnabled:
		return LowNone
	case s.lowOnline:
		return LowSent
	case g.canTranscodeLocked(id, s, running):
		return LowTranscoded
	}
	return LowNone
}

// hasPublisher reports whether the gateway knows a publishing session of
// the stream's variant.
func (g *Gateway) hasPublisher(streamID string, v Variant) bool {
	g.mu.Lock()
	defer g.mu.Unlock()
	for _, sess := range g.sessions {
		if sess.publish && sess.streamID == streamID && sess.variant == v {
			return true
		}
	}
	return false
}

// setLowOnlineLocked records whether the sender's low-quality version is
// being published.
func (g *Gateway) setLowOnlineLocked(s *liveStream, up bool) {
	if s.lowOnline == up {
		return
	}
	s.lowOnline = up
	if !up {
		s.lowStats = streamStats{}
	}
	g.changed()
}

// lowEnded applies the end of a stream's low-quality version, which its
// sender closed; the stream itself goes on.
func (g *Gateway) lowEnded(streamID string) {
	g.mu.Lock()
	defer g.mu.Unlock()
	if s := g.streams[streamID]; s != nil {
		g.setLowOnlineLocked(s, false)
	}
}
