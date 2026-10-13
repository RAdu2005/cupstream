// Package mediamtx talks to MediaMTX's control API and WebRTC (WHIP/WHEP)
// listener, both on localhost only.
package mediamtx

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"time"
)

type Client struct {
	api    *url.URL
	webrtc *url.URL
	http   *http.Client
	// stream has no overall timeout, for the event stream.
	stream *http.Client
}

func New(api, webrtc *url.URL) *Client {
	return &Client{
		api: api, webrtc: webrtc,
		http:   &http.Client{Timeout: 10 * time.Second},
		stream: &http.Client{},
	}
}

// ErrNotFound means the session does not exist (any more).
var ErrNotFound = errors.New("mediamtx: not found")

// Info is the subset of GET /v3/info that CupStream uses.
type Info struct {
	Version string `json:"version"`
}

func (c *Client) Info(ctx context.Context) (Info, error) {
	var info Info
	err := c.get(ctx, "/v3/info", &info)
	return info, err
}

type Path struct {
	Name   string `json:"name"`
	Online bool   `json:"online"`
	// KeyFrameInterval is the longest recent interval between key frames,
	// in seconds (CupStream builds only; nil when unknown).
	KeyFrameInterval *float64 `json:"keyFrameInterval"`
}

// Paths lists the paths that currently exist (have a publisher or readers).
func (c *Client) Paths(ctx context.Context) ([]Path, error) {
	var res struct {
		Items []Path `json:"items"`
	}
	err := c.get(ctx, "/v3/paths/list?itemsPerPage=10000", &res)
	return res.Items, err
}

type Session struct {
	ID                        string    `json:"id"`
	Created                   time.Time `json:"created"`
	PeerConnectionEstablished bool      `json:"peerConnectionEstablished"`
	State                     string    `json:"state"` // "publish" or "read"
	Path                      string    `json:"path"`
	Query                     string    `json:"query"`

	InboundBytes          uint64 `json:"inboundBytes"`
	InboundRTPPackets     uint64 `json:"inboundRTPPackets"`
	InboundRTPPacketsLost uint64 `json:"inboundRTPPacketsLost"`
	OutboundBytes         uint64 `json:"outboundBytes"`

	// Reported by the viewer (CupStream builds only).
	OutboundRTPPacketsLossRatio float64 `json:"outboundRTPPacketsLossRatio"`
	OutboundRTPPacketsJitter    float64 `json:"outboundRTPPacketsJitter"` // seconds
	OutboundRoundTripTime       float64 `json:"outboundRoundTripTime"`    // seconds

	VideoPaused bool `json:"videoPaused"`
	AudioPaused bool `json:"audioPaused"`
}

func (c *Client) WebRTCSessions(ctx context.Context) ([]Session, error) {
	var res struct {
		Items []Session `json:"items"`
	}
	err := c.get(ctx, "/v3/webrtc/sessions/list?itemsPerPage=10000", &res)
	return res.Items, err
}

// Kick closes a WebRTC session. A missing session is not an error.
func (c *Client) Kick(ctx context.Context, id string) error {
	err := c.post(ctx, c.api.JoinPath("/v3/webrtc/sessions/kick", id), nil)
	if errors.Is(err, ErrNotFound) {
		return nil
	}
	return err
}

// Tracks selects the tracks of a viewer session to pause or resume.
type Tracks struct {
	Video bool `json:"video"`
	Audio bool `json:"audio"`
}

var AllTracks = Tracks{Video: true, Audio: true}

// Pause stops sending the selected tracks to a viewer, keeping its
// connection open (CupStream builds only). A missing session is ErrNotFound.
func (c *Client) Pause(ctx context.Context, id string, t Tracks) error {
	return c.post(ctx, c.api.JoinPath("/v3/webrtc/sessions/pause", id), t)
}

// Resume sends the selected tracks again, starting with a key frame.
func (c *Client) Resume(ctx context.Context, id string, t Tracks) error {
	return c.post(ctx, c.api.JoinPath("/v3/webrtc/sessions/resume", id), t)
}

// Signal sends a WHIP/WHEP request to MediaMTX's WebRTC listener. path is
// e.g. "s/abc/whip" or "s/abc/whep/<secret>".
func (c *Client) Signal(ctx context.Context, method, path string, query url.Values, header http.Header, body io.Reader) (*http.Response, error) {
	u := c.webrtc.JoinPath(path)
	u.RawQuery = query.Encode()
	req, err := http.NewRequestWithContext(ctx, method, u.String(), body)
	if err != nil {
		return nil, err
	}
	for k, v := range header {
		req.Header[k] = v
	}
	return c.http.Do(req)
}

func (c *Client) get(ctx context.Context, path string, out any) error {
	u, err := c.api.Parse(path)
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.api.ResolveReference(u).String(), nil)
	if err != nil {
		return err
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("mediamtx %s: %s", path, resp.Status)
	}
	return json.NewDecoder(resp.Body).Decode(out)
}

func (c *Client) post(ctx context.Context, u *url.URL, in any) error {
	var body io.Reader
	if in != nil {
		b, err := json.Marshal(in)
		if err != nil {
			return err
		}
		body = bytes.NewReader(b)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, u.String(), body)
	if err != nil {
		return err
	}
	if in != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	switch resp.StatusCode {
	case http.StatusOK:
		return nil
	case http.StatusNotFound:
		return ErrNotFound
	}
	return fmt.Errorf("mediamtx %s: %s", u.Path, resp.Status)
}
