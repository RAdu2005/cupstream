package api

// DiscordChannel is a Discord room's voice channel, also served at
// GET /api/rooms/{slug}/discord to people who may not enter yet. The server
// and channel names are only given to members of the server.
type DiscordChannel struct {
	GuildName    string `json:"guildName,omitempty"`
	ChannelName  string `json:"channelName,omitempty"`
	GuildIconURL string `json:"guildIconUrl,omitempty"`
	// URL opens the voice channel in Discord.
	URL string `json:"url"`
}

// StreamGrace tells a streamer, on every tab, that their stream in a
// Discord room ends in EndsInMs milliseconds because they left the voice
// channel. 0 means they are back and it goes on.
type StreamGrace struct {
	Type     string `json:"type" tstype:"'streamGrace'"`
	RoomSlug string `json:"roomSlug"`
	StreamID string `json:"streamId"`
	EndsInMs int64  `json:"endsInMs"`
}

// StreamViewer is someone watching a stream, as listed to its streamer by
// GET /api/streams/{id}/viewers before moving it to their link room.
type StreamViewer struct {
	User User `json:"user"`
	// Allowed: they may already enter the streamer's link room.
	Allowed bool `json:"allowed"`
}

// MoveStreamRequest is the body of POST /api/streams/{id}/move, which moves
// the streamer's stream from a Discord room into their link room.
type MoveStreamRequest struct {
	// Visibility "everyone" opens the link room to everyone; empty keeps it.
	Visibility string `json:"visibility,omitempty" tstype:"'everyone' | ''"`
	// Viewers who keep watching: until the stream ends, or always (added to
	// the allowed list). The others are paused.
	Viewers []MoveViewer `json:"viewers"`
}

type MoveViewer struct {
	UserID string `json:"userId"`
	Always bool   `json:"always,omitempty"`
}

type MoveStreamResponse struct {
	RoomSlug string `json:"roomSlug"`
}

// StreamMoved tells a room's subscribers that a stream moved to another
// room, where the people its streamer let in can follow it.
type StreamMoved struct {
	Type     string `json:"type" tstype:"'streamMoved'"`
	Room     string `json:"room"`
	StreamID string `json:"streamId"`
	ToRoom   string `json:"toRoom"`
}
