// Package mediamtx talks to MediaMTX's control API and WebRTC (WHIP/WHEP)
// listener, both on localhost only.
package mediamtx

import (
	"context"
	"encoding/json"
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
}

func New(api, webrtc *url.URL) *Client {
	return &Client{api: api, webrtc: webrtc, http: &http.Client{Timeout: 10 * time.Second}}
}

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
}

func (c *Client) WebRTCSessions(ctx context.Context) ([]Session, error) {
	var res struct {
		Items []Session `json:"items"`
	}
	err := c.get(ctx, "/v3/webrtcsessions/list?itemsPerPage=10000", &res)
	return res.Items, err
}

// Kick closes a WebRTC session. A missing session is not an error.
func (c *Client) Kick(ctx context.Context, id string) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.api.JoinPath("/v3/webrtcsessions/kick", id).String(), nil)
	if err != nil {
		return err
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusNotFound {
		return fmt.Errorf("mediamtx kick %s: %s", id, resp.Status)
	}
	return nil
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
