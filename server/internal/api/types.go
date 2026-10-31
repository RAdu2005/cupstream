// Package api holds the JSON types of the HTTP API. TypeScript types for the
// website are generated from this package (tygo.yaml), so keep it free of
// logic.
package api

// User is a CupStream account as other users see it.
type User struct {
	ID          string `json:"id"`
	Handle      string `json:"handle"`
	DisplayName string `json:"displayName"`
	AvatarURL   string `json:"avatarUrl,omitempty"`
}

// Health is the response of GET /healthz.
type Health struct {
	Status   string         `json:"status" tstype:"'ok' | 'degraded'"`
	Version  string         `json:"version"` // commit the server was built from, or "dev"
	Database bool           `json:"database"`
	MediaMTX MediaMTXHealth `json:"mediamtx"`
}

type MediaMTXHealth struct {
	Reachable bool   `json:"reachable"`
	Version   string `json:"version,omitempty"`
	// Features of CupStream's MediaMTX build; without them the server closes
	// sessions instead of pausing them and polls instead of following events.
	Pause  bool `json:"pause"`
	Events bool `json:"events"`
}

// ErrorResponse is the body of every non-2xx JSON response.
type ErrorResponse struct {
	Error string `json:"error"`
	// Code is a machine-readable reason when the client can act on it, e.g.
	// "sign_in_required", "not_allowed", "already_live".
	Code string `json:"code,omitempty"`
}
