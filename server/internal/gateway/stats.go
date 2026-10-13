package gateway

import (
	"context"
	"time"

	"github.com/RAdu2005/cupstream/server/internal/mediamtx"
)

// StreamStats describes what MediaMTX receives from a stream's publisher.
type StreamStats struct {
	// Bitrate received from the publisher, in bits per second.
	Bitrate int64
	// LossRatio of the publisher's packets over the last interval (0-1).
	LossRatio float64
	// KeyFrameInterval is the longest recent interval between key frames;
	// zero when unknown. Publishers that ignore key frame requests (OBS,
	// FFmpeg) make new viewers wait up to this long.
	KeyFrameInterval time.Duration
}

// ViewerStats describes the media a viewer receives, from MediaMTX's
// counters and the viewer's receiver reports.
type ViewerStats struct {
	SessionID string
	UserID    string
	Watching  bool
	// Bitrate sent to the viewer, in bits per second.
	Bitrate   int64
	LossRatio float64
	Jitter    time.Duration
	RTT       time.Duration
}

type counter struct {
	bytes, packets, lost uint64
	at                   time.Time
}

type streamStats struct {
	pub StreamStats
	in  counter
}

func (s streamStats) public() StreamStats { return s.pub }

type sessionStats struct {
	pub ViewerStats
	out counter
}

func bitrate(prev counter, bytes uint64, now time.Time) int64 {
	dt := now.Sub(prev.at).Seconds()
	if prev.at.IsZero() || dt <= 0 || bytes < prev.bytes {
		return 0
	}
	return int64(float64(bytes-prev.bytes) * 8 / dt)
}

func seconds(s float64) time.Duration { return time.Duration(s * float64(time.Second)) }

// applyStatsLocked updates statistics from MediaMTX's lists. Rates are
// computed from byte counters between two calls.
func (g *Gateway) applyStatsLocked(paths []mediamtx.Path, sessions []mediamtx.Session, now time.Time) {
	for _, p := range paths {
		id, ok := streamIDFromPath(p.Name)
		if s := g.streams[id]; ok && s != nil {
			s.stats.pub.KeyFrameInterval = 0
			if p.KeyFrameInterval != nil {
				s.stats.pub.KeyFrameInterval = seconds(*p.KeyFrameInterval)
			}
		}
	}
	for _, ms := range sessions {
		sess := g.sessions[ms.ID]
		if sess == nil {
			continue
		}
		if sess.publish {
			s := g.streams[sess.streamID]
			if s == nil {
				continue
			}
			prev := s.stats.in
			s.stats.pub.Bitrate = bitrate(prev, ms.InboundBytes, now)
			s.stats.pub.LossRatio = 0
			if !prev.at.IsZero() && ms.InboundRTPPackets >= prev.packets && ms.InboundRTPPacketsLost >= prev.lost {
				got, lost := ms.InboundRTPPackets-prev.packets, ms.InboundRTPPacketsLost-prev.lost
				if got+lost > 0 {
					s.stats.pub.LossRatio = float64(lost) / float64(got+lost)
				}
			}
			s.stats.in = counter{bytes: ms.InboundBytes, packets: ms.InboundRTPPackets, lost: ms.InboundRTPPacketsLost, at: now}
			continue
		}
		sess.stats.pub = ViewerStats{
			SessionID: sess.mtxID,
			UserID:    sess.userID,
			Bitrate:   bitrate(sess.stats.out, ms.OutboundBytes, now),
			LossRatio: ms.OutboundRTPPacketsLossRatio,
			Jitter:    seconds(ms.OutboundRTPPacketsJitter),
			RTT:       seconds(ms.OutboundRoundTripTime),
		}
		sess.stats.out = counter{bytes: ms.OutboundBytes, at: now}
	}
}

// RefreshStats updates statistics without changing live state.
func (g *Gateway) RefreshStats(ctx context.Context) error {
	g.mu.Lock()
	idle := len(g.streams) == 0
	g.mu.Unlock()
	if idle {
		return nil
	}
	paths, err := g.mtx.Paths(ctx)
	if err != nil {
		return err
	}
	sessions, err := g.mtx.WebRTCSessions(ctx)
	if err != nil {
		return err
	}
	g.mu.Lock()
	g.applyStatsLocked(paths, sessions, g.now())
	g.mu.Unlock()
	return nil
}

// ViewerStats returns the statistics of a stream's viewers, for adapting
// the sender's bitrate to what its viewers can receive.
func (g *Gateway) ViewerStats(streamID string) []ViewerStats {
	g.mu.Lock()
	defer g.mu.Unlock()
	var out []ViewerStats
	for _, sess := range g.sessions {
		if sess.streamID == streamID && !sess.publish {
			v := sess.stats.pub
			v.SessionID, v.UserID, v.Watching = sess.mtxID, sess.userID, sess.watching()
			out = append(out, v)
		}
	}
	return out
}
