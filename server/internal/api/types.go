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

// AppAuthorizeRequest is the body of POST /api/app/authorize: the signed-in
// user approves the desktop app's sign-in request, whose parameters the app
// put in the /app/authorize page's query string.
type AppAuthorizeRequest struct {
	RedirectURI         string `json:"redirectUri"` // the app's loopback address
	State               string `json:"state"`
	CodeChallenge       string `json:"codeChallenge"`
	CodeChallengeMethod string `json:"codeChallengeMethod" tstype:"'S256'"`
	DeviceName          string `json:"deviceName"`
}

// AppAuthorizeResponse tells the page where to send the browser: the app's
// redirect URI with a one-time code and the state.
type AppAuthorizeResponse struct {
	Redirect string `json:"redirect"`
}

// AppTokenRequest is the body of POST /api/app/token, sent by the desktop
// app to exchange its code.
type AppTokenRequest struct {
	Code         string `json:"code"`
	CodeVerifier string `json:"codeVerifier"`
	RedirectURI  string `json:"redirectUri"`
}

// AppToken is the desktop app's session: it sends the token as a Bearer
// token on /api/ requests, and the user can revoke it in their settings.
type AppToken struct {
	Token string `json:"token"`
	User  User   `json:"user"`
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
	// Viewers counts sessions receiving media; paused ones do not count.
	Viewers int `json:"viewers"`
	// Bitrate the server receives from the streamer, in bits per second.
	Bitrate int64 `json:"bitrate"`
	// LossRatio of the streamer's packets on the way to the server (0-1).
	LossRatio float64 `json:"lossRatio"`
	// KeyFrameInterval is the longest recent interval between key frames,
	// in seconds, when known. OBS ignores requests for key frames, so new
	// viewers of an OBS stream wait up to this long for the picture.
	KeyFrameInterval float64 `json:"keyFrameInterval,omitempty"`
}

// PauseTracks is the optional body of POST /api/media/{stream}/whep/{secret}/pause
// and .../resume. Without it, video and audio are both selected.
type PauseTracks struct {
	Video *bool `json:"video,omitempty"`
	Audio *bool `json:"audio,omitempty"`
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

// Realtime: one WebSocket per browser tab at GET /api/realtime?tab=<id>.
// The tab ID is random per page load; WHEP requests from the same tab pass
// it too (?tab=<id>), so the server can close a tab's media sessions when
// its WebSocket goes away. Rooms are named by their slug. The client sends
// ClientMessage; every server message has a "type".

// ClientMessage is a message from the browser.
type ClientMessage struct {
	Type string `json:"type" tstype:"'subscribe' | 'unsubscribe' | 'chat' | 'deleteChat'"`
	Room string `json:"room"`
	// Body of a chat message.
	Body string `json:"body,omitempty"`
	// ID of the chat message to delete.
	ID string `json:"id,omitempty"`
	// Ref is echoed in an error caused by this message.
	Ref string `json:"ref,omitempty"`
}

// ChatMessage is a message in a room's chat. CreatedAt is Unix seconds.
type ChatMessage struct {
	ID        string `json:"id"`
	User      User   `json:"user"`
	Body      string `json:"body"`
	CreatedAt int64  `json:"createdAt"`
}

// Presence is who is in a room right now: signed-in people, and the number
// of anonymous visitors (only rooms open to everyone have them).
type Presence struct {
	Members   []User `json:"members"`
	Anonymous int    `json:"anonymous"`
}

// Subscribed answers a subscription with the room's current state: its live
// streams, who is there and the chat of the last 24 hours, oldest first.
type Subscribed struct {
	Type     string        `json:"type" tstype:"'subscribed'"`
	Room     string        `json:"room"`
	Streams  []Stream      `json:"streams"`
	Presence Presence      `json:"presence"`
	Chat     []ChatMessage `json:"chat"`
}

// Refused means the subscription was refused or has ended because access
// changed. Code is an access reason ("not_allowed", "sign_in_required", …)
// or "not_found".
type Refused struct {
	Type  string `json:"type" tstype:"'refused'"`
	Room  string `json:"room"`
	Code  string `json:"code"`
	Error string `json:"error"`
}

// RoomChanged means the room's settings or the viewer's permissions may
// have changed; the client fetches the room again.
type RoomChanged struct {
	Type string `json:"type" tstype:"'roomChanged'"`
	Room string `json:"room"`
}

type PresenceEvent struct {
	Type     string   `json:"type" tstype:"'presence'"`
	Room     string   `json:"room"`
	Presence Presence `json:"presence"`
}

// StreamEvent is sent when a stream starts or changes: its state, viewer
// count or bitrate.
type StreamEvent struct {
	Type   string `json:"type" tstype:"'stream'"`
	Room   string `json:"room"`
	Stream Stream `json:"stream"`
}

type StreamEnded struct {
	Type     string `json:"type" tstype:"'streamEnded'"`
	Room     string `json:"room"`
	StreamID string `json:"streamId"`
}

// ViewerStats summarizes what a stream's viewers receive, from MediaMTX's
// counters and their receiver reports. Only the streamer gets it.
type ViewerStats struct {
	Type     string `json:"type" tstype:"'viewerStats'"`
	Room     string `json:"room"`
	StreamID string `json:"streamId"`
	// Viewers receiving media.
	Viewers int `json:"viewers"`
	// Bitrate sent to all viewers together, in bits per second.
	Bitrate int64 `json:"bitrate"`
	// The worst packet loss (0-1), jitter and round-trip time among them.
	MaxLossRatio float64 `json:"maxLossRatio"`
	MaxJitterMs  float64 `json:"maxJitterMs"`
	MaxRttMs     float64 `json:"maxRttMs"`
}

type ChatEvent struct {
	Type    string      `json:"type" tstype:"'chat'"`
	Room    string      `json:"room"`
	Message ChatMessage `json:"message"`
}

type ChatDeleted struct {
	Type string `json:"type" tstype:"'chatDeleted'"`
	Room string `json:"room"`
	ID   string `json:"id"`
}

// RealtimeError answers a client message that failed. Code is e.g.
// "rate_limited", "sign_in_required", "not_allowed" or "invalid".
type RealtimeError struct {
	Type  string `json:"type" tstype:"'error'"`
	Room  string `json:"room,omitempty"`
	Ref   string `json:"ref,omitempty"`
	Code  string `json:"code"`
	Error string `json:"error"`
}
