package api

// HomeRoom is a room on the home page, with its active streams.
type HomeRoom struct {
	Slug  string `json:"slug"`
	Kind  string `json:"kind" tstype:"'link' | 'discord'"`
	Owner *User  `json:"owner,omitempty"` // link rooms
	// Access says why the user may enter: their own room, the allowed list,
	// or a pass (an invite link). Empty for rooms reached through a friend.
	Access    string   `json:"access,omitempty" tstype:"'own' | 'allowed' | 'pass'"`
	CanStream bool     `json:"canStream"`
	Streams   []Stream `json:"streams"`
	// Discord rooms: the voice channel.
	Discord *DiscordChannel `json:"discord,omitempty"`
}

// Home is the response of GET /api/home.
type Home struct {
	// Voice is the room of the Discord voice channel the user is in, when
	// the CupStream bot is in that server.
	Voice *HomeRoom `json:"voice,omitempty"`
	// The user's link room first, then the rooms they may enter, live ones
	// first.
	Rooms []HomeRoom `json:"rooms"`
	// FriendsLive are other rooms where friends are live now and the user
	// may watch (friends only see each other live where they are allowed).
	FriendsLive []HomeRoom `json:"friendsLive"`
}
