package api

// AdminOverview is the response of GET /api/admin: every live stream, and
// the users who are banned. Bitrates are in bits per second.
type AdminOverview struct {
	Streams []AdminStream `json:"streams"`
	// Viewers receiving media, over all streams.
	Viewers int `json:"viewers"`
	// What the server receives from streamers and sends to viewers, from
	// MediaMTX's counters.
	Incoming   int64        `json:"incoming"`
	Outgoing   int64        `json:"outgoing"`
	Transcodes Transcodes   `json:"transcodes"`
	Banned     []BannedUser `json:"banned"`
}

// Transcodes is how many low-quality versions the server transcodes, and
// how many it may at once. Max starts at CUPSTREAM_TRANSCODES; an admin
// can change it with PUT /api/admin/transcodes until the server restarts.
type Transcodes struct {
	Running int `json:"running"`
	Max     int `json:"max"`
}

// SetTranscodesRequest is the body of PUT /api/admin/transcodes.
type SetTranscodesRequest struct {
	Max int `json:"max"`
}

// AdminStream is a live stream with the room it is in and what it costs.
type AdminStream struct {
	Stream   Stream `json:"stream"`
	RoomSlug string `json:"roomSlug"`
	// Outgoing is what the stream's viewers receive together.
	Outgoing int64 `json:"outgoing"`
}

// BannedUser is a user an admin banned. BannedAt is Unix seconds.
type BannedUser struct {
	User     User  `json:"user"`
	BannedAt int64 `json:"bannedAt"`
}
