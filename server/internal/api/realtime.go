package api

// Realtime: one WebSocket per browser tab at GET /api/realtime?tab=<id>.
// The tab ID is random per page load; WHEP requests from the same tab pass
// it too (?tab=<id>), so the server can close a tab's media sessions when
// its WebSocket goes away. Rooms are named by their slug. The client sends
// ClientMessage; every server message has a "type".

// ClientMessage is a message from the browser.
type ClientMessage struct {
	Type string `json:"type" tstype:"'subscribe' | 'unsubscribe' | 'chat' | 'deleteChat' | 'appGoLive' | 'viewerReport'"`
	// The room; for appGoLive, optionally the room the app should stream
	// into instead of following the start mode.
	Room string `json:"room"`
	// Body of a chat message.
	Body string `json:"body,omitempty"`
	// ID of the chat message to delete, or for appGoLive, of the desktop
	// app to ask (an AppInstance ID; empty: the one that connected last).
	ID string `json:"id,omitempty"`
	// Ref is echoed in an error caused by this message.
	Ref string `json:"ref,omitempty"`
	// Report of a viewerReport message.
	Report *ViewerReport `json:"report,omitempty"`
}

// ViewerReport is what a viewer's browser measured over the last two
// seconds for each stream it plays in the room, from its WebRTC statistics
// (overview, 7.2). The page sends one every two seconds while it plays.
type ViewerReport struct {
	// Limited means the viewer's own connection is the bottleneck: every
	// stream it plays got worse together. Lowering a sender's bitrate would
	// not help such a viewer.
	Limited bool `json:"limited"`
	// CapacityBps is the viewer's estimated download capacity, from a
	// download test and the throughput it sustained; 0 when unknown.
	CapacityBps int64          `json:"capacityBps,omitempty"`
	Streams     []StreamReport `json:"streams"`
}

type StreamReport struct {
	StreamID        string  `json:"streamId"`
	BitrateBps      int64   `json:"bitrateBps"` // received, video and audio
	LossRatio       float64 `json:"lossRatio"`  // packets lost (0-1)
	Freezes         int     `json:"freezes"`    // video freezes
	FramesDropped   int     `json:"framesDropped"`
	FramesPerSecond float64 `json:"framesPerSecond"`
	JitterMs        float64 `json:"jitterMs"`
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
// changed. Code is an access reason ("not_allowed", "sign_in_required", …),
// "not_found", or "kicked" or "banned" when the room's owner removed them.
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

// BitrateTarget asks an app or web sender to send at most MaxBitrate,
// computed from what its viewers receive. Only the streamer gets it, every
// two seconds while they are subscribed to their stream's room. Browsers
// apply it with RTCRtpSender.setParameters and the desktop app through its
// encoder, each within its own maximum. OBS streams get none.
type BitrateTarget struct {
	Type     string `json:"type" tstype:"'bitrateTarget'"`
	Room     string `json:"room"`
	StreamID string `json:"streamId"`
	// MaxBitrate in bits per second for the whole stream (video and audio);
	// 0 means no limit.
	MaxBitrate int64 `json:"maxBitrate"`
	// Viewers taken into account, and how many of them lose packets or
	// build up delay. Viewers whose own connection is full (all the streams
	// they watch suffer) are left out.
	Viewers   int `json:"viewers"`
	Congested int `json:"congested"`
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
// "rate_limited", "sign_in_required", "not_allowed", "app_offline" or
// "invalid".
type RealtimeError struct {
	Type  string `json:"type" tstype:"'error'"`
	Room  string `json:"room,omitempty"`
	Ref   string `json:"ref,omitempty"`
	Code  string `json:"code"`
	Error string `json:"error"`
}
