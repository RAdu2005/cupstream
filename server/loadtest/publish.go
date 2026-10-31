package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync/atomic"
	"time"

	"github.com/pion/ice/v4"
	"github.com/pion/rtcp"
	"github.com/pion/transport/v5"
	"github.com/pion/transport/v5/stdnet"
	"github.com/pion/webrtc/v4"
	"github.com/pion/webrtc/v4/pkg/media"
	"github.com/pion/webrtc/v4/pkg/media/h264reader"
)

// clip is an H.264 test picture, encoded once and sent in a loop, so the
// harness spends no CPU on encoding during the test.
type clip struct {
	frames [][]byte // Annex B access units
	idr    []int    // indexes of the frames that start with a keyframe
	fps    int
}

func (c *clip) bitrate() int {
	n := 0
	for _, f := range c.frames {
		n += len(f)
	}
	return n * 8 * c.fps / len(c.frames)
}

// encodeClip encodes ten seconds of moving noise (which x264 cannot
// compress below the bitrate) at the requested size and bitrate: Constrained
// Baseline, no B-frames, a keyframe every two seconds. It is cached in the
// temporary directory.
func encodeClip(ctx context.Context, o options) (*clip, error) {
	name := fmt.Sprintf("cupstream-loadtest-%dx%d-%d-%d.h264", o.width, o.height, o.fps, o.bitrate)
	path := filepath.Join(os.TempDir(), name)
	if _, err := os.Stat(path); err != nil {
		log.Printf("encoding the test clip with ffmpeg (once): %s", path)
		b := strconv.Itoa(o.bitrate)
		cmd := exec.CommandContext(ctx, "ffmpeg", "-hide_banner", "-loglevel", "error", "-y",
			"-f", "lavfi", "-i", fmt.Sprintf("testsrc2=size=%dx%d:rate=%d,noise=alls=30:allf=t", o.width, o.height, o.fps),
			"-t", "10", "-c:v", "libx264", "-preset", "veryfast", "-profile:v", "baseline", "-bf", "0",
			"-g", strconv.Itoa(2*o.fps), "-b:v", b, "-maxrate", b, "-bufsize", b, "-pix_fmt", "yuv420p",
			"-x264-params", "slices=1", "-f", "h264", path+".tmp")
		cmd.Stderr = os.Stderr
		if err := cmd.Run(); err != nil {
			return nil, fmt.Errorf("ffmpeg: %w", err)
		}
		if err := os.Rename(path+".tmp", path); err != nil {
			return nil, err
		}
	}
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	r, err := h264reader.NewReader(f)
	if err != nil {
		return nil, err
	}

	c := &clip{fps: o.fps}
	var au bytes.Buffer
	key := false
	for {
		nal, err := r.NextNAL()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return nil, err
		}
		au.Write([]byte{0, 0, 0, 1})
		au.Write(nal.Data)
		switch nal.UnitType {
		case h264reader.NalUnitTypeCodedSliceIdr:
			key = true
			fallthrough
		case h264reader.NalUnitTypeCodedSliceNonIdr:
			if key {
				c.idr = append(c.idr, len(c.frames))
			}
			c.frames = append(c.frames, bytes.Clone(au.Bytes()))
			au.Reset()
			key = false
		}
	}
	if len(c.idr) == 0 || c.idr[0] != 0 {
		return nil, errors.New("the clip does not start with a keyframe")
	}
	return c, nil
}

type publisher struct {
	streamID string
	hc       *http.Client
	o        options
	pc       *webrtc.PeerConnection
	cancel   context.CancelFunc
	plis     atomic.Int64
}

// publish signs in as handle, opens their room to everyone, starts a
// stream and sends the clip to it over WHIP, like the browser's go-live
// dialog. Keyframe requests are answered by jumping to the next keyframe.
func publish(ctx context.Context, o options, c *clip, handle string) (*publisher, error) {
	hc := newClient()
	if err := postJSON(ctx, hc, o, "/api/auth/dev/login", map[string]string{"handle": handle}, nil); err != nil {
		return nil, err
	}
	var started struct {
		Stream   struct{ ID string }
		RoomSlug string
		Token    string
	}
	if err := postJSON(ctx, hc, o, "/api/streams", map[string]any{"source": "web", "room": "", "linkFallback": true, "replace": true}, &started); err != nil {
		return nil, err
	}
	if err := sendJSON(ctx, hc, o, http.MethodPatch, "/api/rooms/"+started.RoomSlug, map[string]string{"visibility": "everyone"}, nil); err != nil {
		return nil, err
	}

	pc, err := newAPI(o).NewPeerConnection(webrtc.Configuration{})
	if err != nil {
		return nil, err
	}
	track, err := webrtc.NewTrackLocalStaticSample(webrtc.RTPCodecCapability{MimeType: webrtc.MimeTypeH264, ClockRate: 90000,
		SDPFmtpLine: "level-asymmetry-allowed=1;packetization-mode=1;profile-level-id=42e01f"}, "video", "loadtest")
	if err != nil {
		return nil, err
	}
	tr, err := pc.AddTransceiverFromTrack(track, webrtc.RTPTransceiverInit{Direction: webrtc.RTPTransceiverDirectionSendonly})
	if err != nil {
		return nil, err
	}
	pctx, cancel := context.WithCancel(ctx)
	p := &publisher{streamID: started.Stream.ID, hc: hc, o: o, pc: pc, cancel: cancel}

	if _, err := negotiate(ctx, hc, pc, o.server.JoinPath("/api/streams", p.streamID, "whip").String(), started.Token); err != nil {
		p.close()
		return nil, err
	}
	go func() {
		for {
			pkts, _, err := tr.Sender().ReadRTCP()
			if err != nil {
				return
			}
			for _, pkt := range pkts {
				switch pkt.(type) {
				case *rtcp.PictureLossIndication, *rtcp.FullIntraRequest:
					p.plis.Add(1)
				}
			}
		}
	}()
	go p.send(pctx, c, track)
	return p, nil
}

func (p *publisher) send(ctx context.Context, c *clip, track *webrtc.TrackLocalStaticSample) {
	d := time.Second / time.Duration(c.fps)
	t := time.NewTicker(d)
	defer t.Stop()
	i, answered := 0, int64(0)
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
		if n := p.plis.Load(); n != answered {
			answered = n
			// The next keyframe in the clip, as an encoder would make one.
			next := 0
			for _, k := range c.idr {
				if k > i {
					next = k
					break
				}
			}
			i = next
		}
		if err := track.WriteSample(media.Sample{Data: c.frames[i], Duration: d}); err != nil {
			return
		}
		i = (i + 1) % len(c.frames)
	}
}

func (p *publisher) close() {
	p.cancel()
	p.pc.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	sendJSON(ctx, p.hc, p.o, http.MethodDelete, "/api/streams/"+p.streamID, nil, nil)
}

// newAPI returns a WebRTC API for the harness: IPv4 UDP host candidates, no
// mDNS, only the chosen interface if any, and larger socket buffers. With
// the default 208 KiB, the harness drops packets when a keyframe reaches
// every viewer at once, and measures itself rather than the server.
func newAPI(o options) *webrtc.API {
	var se webrtc.SettingEngine
	se.SetNetworkTypes([]webrtc.NetworkType{webrtc.NetworkTypeUDP4})
	se.SetICEMulticastDNSMode(ice.MulticastDNSModeDisabled)
	if o.iface != "" {
		se.SetInterfaceFilter(func(name string) bool { return name == o.iface })
	}
	se.SetIncludeLoopbackCandidate(true)
	if n, err := stdnet.NewNet(); err == nil {
		se.SetNet(bufferedNet{n, o.rcvbuf})
	}
	return webrtc.NewAPI(webrtc.WithSettingEngine(se))
}

// bufferedNet opens UDP sockets with a larger receive buffer.
type bufferedNet struct {
	*stdnet.Net
	rcvbuf int
}

func (n bufferedNet) ListenUDP(network string, laddr *net.UDPAddr) (transport.UDPConn, error) {
	c, err := n.Net.ListenUDP(network, laddr)
	if err == nil && n.rcvbuf > 0 {
		c.SetReadBuffer(n.rcvbuf)
	}
	return c, err
}

// negotiate sends pc's offer to target (WHIP or WHEP) and applies the answer.
// It returns the session's Location.
func negotiate(ctx context.Context, hc *http.Client, pc *webrtc.PeerConnection, target, token string) (string, error) {
	offer, err := pc.CreateOffer(nil)
	if err != nil {
		return "", err
	}
	gathered := webrtc.GatheringCompletePromise(pc)
	if err := pc.SetLocalDescription(offer); err != nil {
		return "", err
	}
	select {
	case <-gathered:
	case <-ctx.Done():
		return "", ctx.Err()
	}

	req, _ := http.NewRequestWithContext(ctx, http.MethodPost, target, strings.NewReader(pc.LocalDescription().SDP))
	req.Header.Set("Content-Type", "application/sdp")
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	resp, err := hc.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusCreated {
		return "", &httpError{resp.StatusCode, strings.TrimSpace(string(body))}
	}
	if err := pc.SetRemoteDescription(webrtc.SessionDescription{Type: webrtc.SDPTypeAnswer, SDP: string(body)}); err != nil {
		return "", err
	}
	return resp.Header.Get("Location"), nil
}

type httpError struct {
	status int
	body   string
}

func (e *httpError) Error() string { return fmt.Sprintf("%d %s", e.status, e.body) }

func postJSON(ctx context.Context, hc *http.Client, o options, path string, in, out any) error {
	return sendJSON(ctx, hc, o, http.MethodPost, path, in, out)
}

func sendJSON(ctx context.Context, hc *http.Client, o options, method, path string, in, out any) error {
	var body io.Reader
	if in != nil {
		b, _ := json.Marshal(in)
		body = bytes.NewReader(b)
	}
	req, _ := http.NewRequestWithContext(ctx, method, o.server.JoinPath(path).String(), body)
	if in != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	// Without browser headers (Origin, Sec-Fetch-Site), the server's
	// cross-origin protection lets the request through, as it does for OBS.
	resp, err := hc.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(resp.Body)
	if resp.StatusCode >= 300 {
		return fmt.Errorf("%s %s: %w", method, path, &httpError{resp.StatusCode, strings.TrimSpace(string(raw))})
	}
	if out != nil {
		return json.Unmarshal(raw, out)
	}
	return nil
}
