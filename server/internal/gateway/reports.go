package gateway

import (
	"math"
	"time"

	"github.com/RAdu2005/cupstream/server/internal/api"
)

// Viewers' browsers report what they receive every two seconds (overview,
// 7.2): loss, freezes and dropped frames per stream, and whether their own
// connection is the bottleneck. The server's own statistics only see
// packets; these say what the viewer experiences.

// A report older than this is stale: the viewer stopped playing or left.
const reportTTL = 6 * time.Second

type viewerReport struct {
	at          time.Time
	limited     bool
	capacityBps int64
	stream      api.StreamReport
}

// ViewerReport is the latest report of one viewer session.
type ViewerReport struct {
	SessionID string
	UserID    string
	At        time.Time
	// Limited: the viewer's own connection is the bottleneck, so this
	// viewer should not lower the sender's bitrate.
	Limited     bool
	CapacityBps int64
	Stream      api.StreamReport
}

func clamp(v, lo, hi float64) float64 {
	if math.IsNaN(v) {
		return lo
	}
	return min(max(v, lo), hi)
}

// cleanReport bounds a browser's numbers.
func cleanReport(r api.StreamReport) api.StreamReport {
	r.BitrateBps = int64(clamp(float64(r.BitrateBps), 0, 1e10))
	r.LossRatio = clamp(r.LossRatio, 0, 1)
	r.Freezes = int(clamp(float64(r.Freezes), 0, 1000))
	r.FramesDropped = int(clamp(float64(r.FramesDropped), 0, 1e6))
	r.FramesPerSecond = clamp(r.FramesPerSecond, 0, 1000)
	r.JitterMs = clamp(r.JitterMs, 0, 1e5)
	return r
}

// Report records a viewer's report on the sessions it is about: those of
// the same browser tab (and user) playing the reported streams. It returns
// how many streams it matched.
func (g *Gateway) Report(userID, tab string, r api.ViewerReport) int {
	if tab == "" {
		return 0
	}
	now := g.now()
	byStream := map[string]api.StreamReport{}
	for _, s := range r.Streams {
		byStream[s.StreamID] = cleanReport(s)
	}
	n := 0
	g.mu.Lock()
	defer g.mu.Unlock()
	for _, sess := range g.sessions {
		sr, ok := byStream[sess.streamID]
		if !ok || sess.publish || sess.tab != tab || sess.userID != userID {
			continue
		}
		sess.report = viewerReport{at: now, limited: r.Limited, capacityBps: max(r.CapacityBps, 0), stream: sr}
		n++
	}
	return n
}

// ViewerReports returns the recent reports of a stream's viewers who are
// watching it.
func (g *Gateway) ViewerReports(streamID string) []ViewerReport {
	now := g.now()
	g.mu.Lock()
	defer g.mu.Unlock()
	var out []ViewerReport
	for _, sess := range g.sessions {
		rep := sess.report
		if sess.streamID != streamID || !sess.watching() || rep.at.IsZero() || now.Sub(rep.at) > reportTTL {
			continue
		}
		out = append(out, ViewerReport{
			SessionID: sess.mtxID, UserID: sess.userID, At: rep.at,
			Limited: rep.limited, CapacityBps: rep.capacityBps, Stream: rep.stream,
		})
	}
	return out
}
