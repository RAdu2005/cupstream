package api

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
	// ThumbnailAt is when the stream's latest thumbnail arrived (Unix
	// milliseconds), or 0 without one. The picture is at
	// GET /api/streams/{id}/thumbnail; app and web senders upload it.
	ThumbnailAt int64 `json:"thumbnailAt,omitempty"`
	// LowQuality says what a viewer gets if they pick the low-quality
	// version (POST /api/streams/{id}/whep?quality=low): none, one the
	// sender publishes, or one the server transcodes while someone watches.
	LowQuality string `json:"lowQuality" tstype:"'none' | 'sent' | 'transcoded'"`
	// LowBitrate is what the server receives of a sent low-quality
	// version, in bits per second, when known.
	LowBitrate int64 `json:"lowBitrate,omitempty"`
}

// PauseTracks is the optional body of POST /api/media/{stream}/whep/{secret}/pause
// and .../resume. Without it, video and audio are both selected.
type PauseTracks struct {
	Video *bool `json:"video,omitempty"`
	Audio *bool `json:"audio,omitempty"`
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
	Stream     Stream          `json:"stream"`
	RoomSlug   string          `json:"roomSlug"`
	WhipURL    string          `json:"whipUrl"`
	Token      string          `json:"token"`
	LowQuality LowQualityOffer `json:"lowQuality"`
}

// LowQualityOffer tells a sender whether to publish a low-quality version.
// When Publish is set, the server accepts one at WhipURL + "?quality=low",
// with the same token, once the stream itself is being published; it ends
// with the stream, and can end alone.
type LowQualityOffer struct {
	Publish bool `json:"publish"`
}
