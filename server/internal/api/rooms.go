package api

// Permissions says what the viewer may do in a room.
type Permissions struct {
	Watch           bool `json:"watch"`
	Stream          bool `json:"stream"`
	Chat            bool `json:"chat"`
	ModerateStreams bool `json:"moderateStreams"`
	ModerateChat    bool `json:"moderateChat"`
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
	// Discord rooms: the voice channel.
	Discord *DiscordChannel `json:"discord,omitempty"`
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
