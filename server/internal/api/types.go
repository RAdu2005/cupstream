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

// Me is the response of GET /api/me.
type Me struct {
	User User `json:"user"`
}

// AuthProviders is the response of GET /api/auth/providers: the sign-in
// methods this server offers.
type AuthProviders struct {
	Providers []string `json:"providers" tstype:"AuthProvider[]"`
}

// DevLoginRequest is the body of POST /api/auth/dev/login.
type DevLoginRequest struct {
	Handle string `json:"handle"`
}

// Health is the response of GET /healthz.
type Health struct {
	Status   string         `json:"status" tstype:"'ok' | 'degraded'"`
	Database bool           `json:"database"`
	MediaMTX MediaMTXHealth `json:"mediamtx"`
}

type MediaMTXHealth struct {
	Reachable bool   `json:"reachable"`
	Version   string `json:"version,omitempty"`
}

// ErrorResponse is the body of every non-2xx JSON response.
type ErrorResponse struct {
	Error string `json:"error"`
}
