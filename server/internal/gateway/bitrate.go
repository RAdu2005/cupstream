package gateway

import "time"

// Sender bitrate control (overview 7.3). MediaMTX forwards one encoding to
// every viewer, so quality adapts at the sender: after every statistics
// refresh the gateway looks at what each viewer of an app or web stream
// receives and sets a target, the most the sender should send. The
// realtime hub sends it to the streamer; browsers apply it with
// RTCRtpSender.setParameters, the desktop app through its encoder. OBS
// cannot be controlled remotely.
//
// The target falls when enough viewers lose packets or build up delay, and
// rises again slowly once they are fine, until it no longer limits anything
// and is lifted. One viewer cannot lower the quality for everyone: the
// target only falls when more than a quarter of the viewers struggle,
// viewers whose own connection is full are left out, and it never goes
// below MinTargetBitrate. The sender's own congestion control still handles
// its upload; this only caps it.
const (
	// MinTargetBitrate is the lowest target, in bits per second.
	MinTargetBitrate = 1_000_000
	// A viewer is congested when its smoothed packet loss reaches this, or
	// its round trip grows this much above the lowest seen.
	congestedLoss  = 0.05
	congestedDelay = 250 * time.Millisecond
	// The target falls when more than this share of viewers are congested.
	congestedShare = 0.25
	// A fall sets the target to this share of what the sender sends.
	decreaseFactor = 0.85
	// After a fall, the next waits this long: the sender needs a moment to
	// apply it, and receiver reports a moment more to show the effect.
	decreaseHoldoff = 4 * time.Second
	// With no congestion for this long, the target rises by increaseFactor,
	// and again after each further period.
	increaseAfter  = 10 * time.Second
	increaseFactor = 1.15
	// A target this far above what the sender sends limits nothing, so it
	// is lifted.
	liftAbove = 1.5
	// Weight of a new loss report in a viewer's smoothed loss.
	lossSmoothing = 0.5
)

// Target is the bitrate a sender is asked to stay at or below.
type Target struct {
	// Bitrate in bits per second; 0 means no limit.
	Bitrate int64
	// Viewers taken into account, and how many of them were congested at
	// the last refresh. Viewers over their own capacity are not counted.
	Viewers, Congested int
}

// bitrateControl is a stream's target and its history.
type bitrateControl struct {
	Target
	changed time.Time // the last fall or rise
	calm    time.Time // since when no fall was called for; zero while congested
}

// step moves the target after a statistics refresh. sending is the bitrate
// the server receives from the sender (0 when unknown).
func (c bitrateControl) step(viewers, congested int, sending int64, now time.Time) bitrateControl {
	c.Viewers, c.Congested = viewers, congested
	if congested > 0 && float64(congested) > congestedShare*float64(viewers) {
		c.calm = time.Time{}
		if c.Bitrate != 0 && now.Sub(c.changed) < decreaseHoldoff {
			return c
		}
		base := sending
		if c.Bitrate != 0 && (base == 0 || c.Bitrate < base) {
			base = c.Bitrate
		}
		if base == 0 {
			return c // nothing to go by yet
		}
		next := max(MinTargetBitrate, int64(float64(base)*decreaseFactor))
		if c.Bitrate == 0 || next < c.Bitrate {
			c.Bitrate, c.changed = next, now
		}
		return c
	}

	if c.calm.IsZero() {
		c.calm = now
	}
	if c.Bitrate == 0 || now.Sub(c.calm) < increaseAfter || now.Sub(c.changed) < increaseAfter {
		return c
	}
	c.Bitrate, c.changed = int64(float64(c.Bitrate)*increaseFactor), now
	if sending > 0 && float64(c.Bitrate) > liftAbove*float64(sending) {
		c.Bitrate = 0
	}
	return c
}

// viewerKey groups the sessions of one viewer: their browser tab, or the
// session itself when it says nothing about where it comes from.
func viewerKey(sess *session) string {
	if sess.userID == "" && sess.tab == "" {
		return "session:" + sess.mtxID
	}
	return sess.userID + "/" + sess.tab
}

// congested reports whether a viewer session loses packets or builds up
// delay.
func (s *sessionStats) congested() bool {
	rtt := s.pub.RTT
	return s.loss >= congestedLoss || (rtt > 0 && s.minRTT > 0 && rtt-s.minRTT >= congestedDelay)
}

// updateTargetsLocked moves every controllable stream's target after a
// statistics refresh.
func (g *Gateway) updateTargetsLocked(now time.Time) {
	// A viewer who watches several streams and struggles with all of them is
	// over their own capacity: lowering one sender would not help them.
	type group struct{ watching, congested int }
	groups := map[string]*group{}
	for _, sess := range g.sessions {
		if !sess.watching() {
			continue
		}
		k := viewerKey(sess)
		if groups[k] == nil {
			groups[k] = &group{}
		}
		groups[k].watching++
		if sess.stats.congested() {
			groups[k].congested++
		}
	}
	overCapacity := func(sess *session) bool {
		gr := groups[viewerKey(sess)]
		return gr.watching > 1 && gr.congested == gr.watching
	}

	type tally struct{ viewers, congested int }
	tallies := map[string]*tally{}
	for _, sess := range g.sessions {
		if !sess.watching() || overCapacity(sess) {
			continue
		}
		t := tallies[sess.streamID]
		if t == nil {
			t = &tally{}
			tallies[sess.streamID] = t
		}
		t.viewers++
		if sess.stats.congested() {
			t.congested++
		}
	}
	for id, s := range g.streams {
		if Source(s.rec.Source) == SourceOBS {
			continue
		}
		t := tallies[id]
		if t == nil {
			t = &tally{}
		}
		s.bitrate = s.bitrate.step(t.viewers, t.congested, s.stats.pub.Bitrate, now)
	}
}

// BitrateTarget returns the target of an app or web stream.
func (g *Gateway) BitrateTarget(streamID string) (Target, bool) {
	g.mu.Lock()
	defer g.mu.Unlock()
	s, ok := g.streams[streamID]
	if !ok || Source(s.rec.Source) == SourceOBS {
		return Target{}, false
	}
	return s.bitrate.Target, true
}
