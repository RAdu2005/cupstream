package main

import (
	"context"
	"errors"
	"net/http"
	"sync"
	"sync/atomic"
	"time"

	"github.com/pion/webrtc/v4"
)

// viewer is one synthetic WHEP viewer. It does not decode: it counts what
// arrives, the packets lost, and when the first keyframe came.
type viewer struct {
	streamID string
	location string
	pc       *webrtc.PeerConnection
	hc       *http.Client
	err      error

	opened   time.Time
	firstKey atomic.Int64 // Unix nanoseconds; 0 until a keyframe arrived
	bytes    atomic.Int64
	packets  atomic.Int64
	lost     atomic.Int64

	// Read by the reporter only.
	lastBytes int64
	lastLost  int64
	lastPkts  int64
}

// watch opens a viewer of stream id, signed out, like a visitor in a room
// open to everyone. With -prewarm it opens paused without a preview frame
// and resumes a second later, like a room page switching to a pre-warmed
// stream.
func watch(ctx context.Context, o options, id string) *viewer {
	v := &viewer{streamID: id, hc: newClient(), opened: time.Now()}
	pc, err := newAPI(o).NewPeerConnection(webrtc.Configuration{})
	if err != nil {
		v.err = err
		return v
	}
	v.pc = pc
	for _, kind := range []webrtc.RTPCodecType{webrtc.RTPCodecTypeVideo, webrtc.RTPCodecTypeAudio} {
		if _, err := pc.AddTransceiverFromKind(kind, webrtc.RTPTransceiverInit{Direction: webrtc.RTPTransceiverDirectionRecvonly}); err != nil {
			v.err = err
			return v
		}
	}
	pc.OnTrack(func(t *webrtc.TrackRemote, _ *webrtc.RTPReceiver) { go v.read(t) })

	target := o.server.JoinPath("/api/streams", id, "whep").String()
	if o.paused {
		target += "?paused=true&preview=false"
	}
	if v.location, v.err = negotiate(ctx, v.hc, pc, target, ""); v.err != nil {
		return v
	}
	if o.paused {
		select {
		case <-ctx.Done():
			return v
		case <-time.After(time.Second):
		}
		v.opened = time.Now()
		req, _ := http.NewRequestWithContext(ctx, http.MethodPost, v.location+"/resume", nil)
		resp, err := v.hc.Do(req)
		if err != nil {
			v.err = err
			return v
		}
		resp.Body.Close()
		if resp.StatusCode != http.StatusNoContent {
			v.err = &httpError{resp.StatusCode, "resume"}
		}
	}
	return v
}

func (v *viewer) read(t *webrtc.TrackRemote) {
	video := t.Kind() == webrtc.RTPCodecTypeVideo
	var last uint16
	first := true
	for {
		pkt, _, err := t.ReadRTP()
		if err != nil {
			return
		}
		v.bytes.Add(int64(pkt.MarshalSize()))
		v.packets.Add(1)
		if !first {
			// A gap is loss; a step back is reordering.
			if gap := pkt.SequenceNumber - last - 1; gap > 0 && gap < 1000 {
				v.lost.Add(int64(gap))
			}
		}
		if first || pkt.SequenceNumber-last < 1000 {
			last = pkt.SequenceNumber
		}
		first = false
		if video && v.firstKey.Load() == 0 && isKeyframe(pkt.Payload) {
			v.firstKey.Store(time.Now().UnixNano())
		}
	}
}

// isKeyframe reports whether an H.264 RTP payload starts an IDR picture.
func isKeyframe(p []byte) bool {
	if len(p) < 2 {
		return false
	}
	switch p[0] & 0x1f {
	case 5:
		return true
	case 24: // STAP-A: 16-bit sizes, then NAL units
		for i := 1; i+2 < len(p); {
			n := int(p[i])<<8 | int(p[i+1])
			if i+2 < len(p) && p[i+2]&0x1f == 5 {
				return true
			}
			i += 2 + n
		}
	case 28: // FU-A: the start of a fragmented NAL unit
		return p[1]&0x80 != 0 && p[1]&0x1f == 5
	}
	return false
}

// close ends the viewer's session like a browser closing the page.
func (v *viewer) close() {
	if v.location != "" {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		req, _ := http.NewRequestWithContext(ctx, http.MethodDelete, v.location, nil)
		if resp, err := v.hc.Do(req); err == nil {
			resp.Body.Close()
		}
		cancel()
	}
	if v.pc != nil {
		v.pc.Close()
	}
}

// refused reports whether the server turned the viewer away for capacity.
func (v *viewer) refused() bool {
	var he *httpError
	return errors.As(v.err, &he) && he.status == http.StatusServiceUnavailable
}

func (v *viewer) rateLimited() bool {
	var he *httpError
	return errors.As(v.err, &he) && he.status == http.StatusTooManyRequests
}

// closeAll closes viewers, a few at a time.
func closeAll(vs []*viewer) {
	sem := make(chan struct{}, 32)
	var wg sync.WaitGroup
	for _, v := range vs {
		wg.Add(1)
		sem <- struct{}{}
		go func() {
			defer wg.Done()
			v.close()
			<-sem
		}()
	}
	wg.Wait()
}
