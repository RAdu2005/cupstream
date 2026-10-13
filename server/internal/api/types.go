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
	// Code is a machine-readable reason when the client can act on it, e.g.
	// "sign_in_required", "not_allowed", "already_live".
	Code string `json:"code,omitempty"`
}

// Identity is a sign-in method linked to the account.
type Identity struct {
	Provider string `json:"provider" tstype:"AuthProvider"`
	Username string `json:"username"`
}

type Settings struct {
	// Where a new stream goes: a Discord room when possible, or always the
	// user's link room.
	StartMode string `json:"startMode" tstype:"'discord_auto' | 'link'"`
	// What OBS does when Discord (auto) finds no Discord room.
	ObsFallback string `json:"obsFallback" tstype:"'link' | 'refuse'"`
}

// Account is the response of GET /api/account.
type Account struct {
	User       User       `json:"user"`
	Identities []Identity `json:"identities"`
	Settings   Settings   `json:"settings"`
	RoomSlug   string     `json:"roomSlug"` // the user's link room
}

type UpdateProfileRequest struct {
	Handle      string `json:"handle"`
	DisplayName string `json:"displayName"`
}

// Session is a signed-in browser or desktop app. Times are Unix seconds.
type Session struct {
	ID         string `json:"id"`
	Kind       string `json:"kind" tstype:"'web' | 'app'"`
	DeviceName string `json:"deviceName"`
	CreatedAt  int64  `json:"createdAt"`
	LastSeenAt int64  `json:"lastSeenAt"`
	Current    bool   `json:"current"`
}

// Guild is one of the user's Discord servers.
type Guild struct {
	ID                 string `json:"id"`
	Name               string `json:"name"`
	IconURL            string `json:"iconUrl,omitempty"`
	ApproxMembers      int64  `json:"approxMembers"`
	SuggestionsEnabled bool   `json:"suggestionsEnabled"`
}

type UpdateGuildRequest struct {
	SuggestionsEnabled bool `json:"suggestionsEnabled"`
}

// PendingMerge is offered after linking an identity that belongs to another
// account. Confirming moves that account's sign-in methods here and deletes
// it, with its room and stream key.
type PendingMerge struct {
	Provider string `json:"provider" tstype:"AuthProvider"`
	Username string `json:"username"`
	Other    User   `json:"other"`
}

type StreamKeyInfo struct {
	HasKey    bool   `json:"hasKey"`
	CreatedAt int64  `json:"createdAt,omitempty"`
	WhipURL   string `json:"whipUrl"`
}

// NewStreamKey is returned once, when a key is generated.
type NewStreamKey struct {
	Key     string `json:"key"`
	WhipURL string `json:"whipUrl"`
}

// Permissions says what the viewer may do in a room.
type Permissions struct {
	Watch           bool `json:"watch"`
	Stream          bool `json:"stream"`
	Chat            bool `json:"chat"`
	ModerateStreams bool `json:"moderateStreams"`
	ModerateChat    bool `json:"moderateChat"`
}

type Stream struct {
	ID        string `json:"id"`
	User      User   `json:"user"`
	Source    string `json:"source" tstype:"'app' | 'web' | 'obs'"`
	State     string `json:"state" tstype:"'starting' | 'live' | 'reconnecting'"`
	StartedAt int64  `json:"startedAt"`
	Viewers   int    `json:"viewers"`
}

// Room is the response of GET /api/rooms/{slug}.
type Room struct {
	ID          string      `json:"id"`
	Slug        string      `json:"slug"`
	Kind        string      `json:"kind" tstype:"'link' | 'discord'"`
	Owner       *User       `json:"owner,omitempty"`                          // link rooms
	Visibility  string      `json:"visibility,omitempty" tstype:"Visibility"` // link rooms
	Permissions Permissions `json:"permissions"`
	Streams     []Stream    `json:"streams"`
}

type UpdateRoomRequest struct {
	Visibility string `json:"visibility" tstype:"Visibility"`
}

type AllowedEntry struct {
	User      User `json:"user"`
	CanStream bool `json:"canStream"`
}

type SetAllowedRequest struct {
	CanStream bool `json:"canStream"`
}

// StartStreamRequest is the body of POST /api/streams (app and web senders).
type StartStreamRequest struct {
	Source string `json:"source" tstype:"'app' | 'web'"`
	// Room is a room slug; empty follows the start mode.
	Room string `json:"room,omitempty"`
	// LinkFallback accepts the link room when no Discord room is possible.
	LinkFallback bool `json:"linkFallback,omitempty"`
	// Replace ends the user's current stream elsewhere.
	Replace bool `json:"replace,omitempty"`
}

// StartStreamResponse tells the sender where to publish: POST the WHIP
// offer to WhipURL with "Authorization: Bearer <token>".
type StartStreamResponse struct {
	Stream   Stream `json:"stream"`
	RoomSlug string `json:"roomSlug"`
	WhipURL  string `json:"whipUrl"`
	Token    string `json:"token"`
}
