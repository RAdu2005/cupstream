// Package mediamtx talks to MediaMTX's control API, which listens on
// localhost only.
package mediamtx

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"time"
)

type Client struct {
	api  *url.URL
	http *http.Client
}

func New(api *url.URL) *Client {
	return &Client{api: api, http: &http.Client{Timeout: 5 * time.Second}}
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

func (c *Client) get(ctx context.Context, path string, out any) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.api.JoinPath(path).String(), nil)
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
