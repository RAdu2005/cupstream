// Package access decides who may watch, stream, chat in and moderate a room.
//
// It is pure logic: callers gather the facts (Room, Subject) from the
// database and, for Discord rooms, from the bot's voice state. Each room kind
// has exactly one source of truth: CupStream for link rooms, Discord for
// Discord rooms. Nobody is ever added to a room automatically.
package access

type Action int

const (
	Watch Action = iota
	Stream
	Chat
	ModerateStreams // end other people's streams, kick viewers
	ModerateChat    // delete chat messages
)

func (a Action) String() string {
	return [...]string{"watch", "stream", "chat", "moderate streams", "moderate chat"}[a]
}

type RoomKind string

const (
	Link    RoomKind = "link"
	Discord RoomKind = "discord"
)

type Visibility string

const (
	Everyone   Visibility = "everyone"
	SignedIn   Visibility = "signed_in"
	InviteOnly Visibility = "invite_only"
)

// StreamPolicy is a Discord server's rule for who may stream in its rooms.
type StreamPolicy string

const (
	VideoPermission StreamPolicy = "video_permission"
	AnyoneInChannel StreamPolicy = "anyone_in_channel"
)

type Room struct {
	Kind RoomKind

	// Link rooms.
	OwnerID    string
	Visibility Visibility

	// Discord rooms.
	Policy StreamPolicy
}

// Subject is what is known about the person asking.
type Subject struct {
	UserID string // empty for anonymous visitors

	// Link rooms.
	Banned          bool
	Allowed         bool // on the room's allowed list
	AllowedToStream bool // allowed list entry with streaming
	Pass            bool // valid temporary pass (invite link or moved stream)
	PassToStream    bool

	// Discord rooms, from the bot's voice state.
	DiscordLinked     bool
	InVoiceChannel    bool // in this room's voice channel, grace period included
	CanVideo          bool // Discord's Video (Go Live) permission in the channel
	CanMoveMembers    bool
	CanManageMessages bool
}

// Reason explains a refusal, so the website can tell the user what to do.
type Reason string

const (
	OK               Reason = ""
	SignInRequired   Reason = "sign_in_required"
	NotAllowed       Reason = "not_allowed"
	Banned           Reason = "banned"
	DiscordRequired  Reason = "discord_required"
	JoinVoiceChannel Reason = "join_voice_channel"
	NoVideoPerm      Reason = "no_video_permission"
)

// Check reports whether s may perform action in room, and why not.
func Check(room Room, s Subject, action Action) Reason {
	switch room.Kind {
	case Link:
		return checkLink(room, s, action)
	case Discord:
		return checkDiscord(room, s, action)
	}
	return NotAllowed
}

func checkLink(room Room, s Subject, action Action) Reason {
	signedIn := s.UserID != ""
	if signedIn && s.UserID == room.OwnerID {
		return OK
	}
	if s.Banned {
		return Banned
	}

	switch action {
	case ModerateStreams, ModerateChat:
		return NotAllowed
	case Stream:
		if !signedIn {
			return SignInRequired
		}
		if s.AllowedToStream || s.PassToStream {
			return OK
		}
		return NotAllowed
	case Chat:
		if !signedIn {
			return SignInRequired
		}
		return checkLink(room, s, Watch)
	}

	// Watch.
	switch room.Visibility {
	case Everyone:
		return OK
	case SignedIn:
		if !signedIn {
			return SignInRequired
		}
		return OK
	}
	if !signedIn {
		return SignInRequired
	}
	if s.Allowed || s.Pass {
		return OK
	}
	return NotAllowed
}

func checkDiscord(room Room, s Subject, action Action) Reason {
	switch {
	case s.UserID == "":
		return SignInRequired
	case !s.DiscordLinked:
		return DiscordRequired
	case !s.InVoiceChannel:
		return JoinVoiceChannel
	}
	switch action {
	case Stream:
		if room.Policy == AnyoneInChannel || s.CanVideo {
			return OK
		}
		return NoVideoPerm
	case ModerateStreams:
		if s.CanMoveMembers {
			return OK
		}
		return NotAllowed
	case ModerateChat:
		if s.CanManageMessages {
			return OK
		}
		return NotAllowed
	}
	return OK // Watch, Chat
}
