package gateway

import (
	"log/slog"
	"time"
)

// On-demand transcodes (overview 7.3, docs/low-quality.md). A stream whose
// sender publishes no low-quality version can get one from the server:
// MediaMTX runs FFmpeg (runOnDemand) on the stream's transcode path,
// s/<streamId>/transcode, while someone reads it, and stops it once nobody
// has for a few seconds. FFmpeg costs CPU, so at most MaxTranscodes run at
// once: when they are all taken, further streams offer no low-quality
// version, and the ones running carry on.

const (
	// A transcode a viewer was admitted to counts as running for this long
	// before MediaMTX reports its path online, so that two viewers cannot
	// take the last place at once.
	transcodeReserve = 30 * time.Second
	// A viewer of a transcode who receives nothing (paused, or held) for
	// this long is disconnected: their session alone keeps FFmpeg running.
	transcodeIdleTimeout = 30 * time.Second
)

// TranscodeUsage is how many transcodes run, and how many may.
type TranscodeUsage struct {
	Running, Max int
}

// SetMaxTranscodes changes how many transcodes may run at once. Lowering it
// stops none of the running ones; new ones wait until fewer run.
func (g *Gateway) SetMaxTranscodes(n int) {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.maxTranscodes = max(n, 0)
	g.changed() // which streams offer a low-quality version may change
}

// Transcodes returns how many transcodes run, and how many may.
func (g *Gateway) Transcodes() TranscodeUsage {
	g.mu.Lock()
	defer g.mu.Unlock()
	return TranscodeUsage{Running: len(g.transcodingLocked(g.now())), Max: g.maxTranscodes}
}

// transcodingLocked returns the streams whose transcode runs: MediaMTX
// reports its path online, it has viewers, or a viewer was just admitted
// to it.
func (g *Gateway) transcodingLocked(now time.Time) map[string]bool {
	out := map[string]bool{}
	for id, s := range g.streams {
		if s.transcodeOnline || now.Sub(s.transcodeReserved) < transcodeReserve {
			out[id] = true
		}
	}
	for _, sess := range g.sessions {
		if sess.variant == VariantTranscode && g.streams[sess.streamID] != nil {
			out[sess.streamID] = true
		}
	}
	return out
}

// canTranscodeLocked reports whether a viewer could get a transcode of the
// stream: its own runs already, or there is room for one more.
func (g *Gateway) canTranscodeLocked(id string, s *liveStream, running map[string]bool) bool {
	if g.maxTranscodes == 0 || s.state != Live {
		return false
	}
	return running[id] || len(running) < g.maxTranscodes
}

// reserveTranscode admits a viewer to the stream's transcode, starting one
// if there is room. It reports false when there is none.
func (g *Gateway) reserveTranscode(id string) bool {
	now := g.now()
	g.mu.Lock()
	defer g.mu.Unlock()
	s := g.streams[id]
	running := g.transcodingLocked(now)
	if s == nil || !g.canTranscodeLocked(id, s, running) {
		return false
	}
	if !running[id] {
		slog.Info("starting a transcode", "stream", id, "running", len(running)+1, "max", g.maxTranscodes)
		g.changed() // the cap may now be reached for other streams
	}
	s.transcodeReserved = now
	return true
}

// setTranscodeOnlineLocked records whether MediaMTX reports the stream's
// transcode path online, which it is while FFmpeg publishes to it.
func (g *Gateway) setTranscodeOnlineLocked(s *liveStream, up bool) {
	if s.transcodeOnline == up {
		return
	}
	s.transcodeOnline = up
	g.changed()
}

// idleTranscodeViewersLocked returns the viewers of transcodes who received
// nothing for transcodeIdleTimeout, to disconnect.
func (g *Gateway) idleTranscodeViewersLocked(now time.Time) []string {
	var out []string
	for id, sess := range g.sessions {
		if sess.variant != VariantTranscode {
			continue
		}
		switch {
		case sess.watching():
			sess.idleSince = time.Time{}
		case sess.idleSince.IsZero():
			sess.idleSince = now
		case now.Sub(sess.idleSince) > transcodeIdleTimeout:
			delete(g.sessions, id)
			out = append(out, id)
		}
	}
	if len(out) > 0 {
		g.changed()
	}
	return out
}
