package api

// Friend is someone who accepted a friend request, or whose request the
// user accepted. Since is Unix seconds.
type Friend struct {
	User  User  `json:"user"`
	Since int64 `json:"since"`
}

// FriendRequest is a pending request, sent or received. CreatedAt is Unix
// seconds.
type FriendRequest struct {
	User      User  `json:"user"`
	CreatedAt int64 `json:"createdAt"`
}

// Friends is the response of GET /api/friends.
type Friends struct {
	Friends  []Friend        `json:"friends"`
	Incoming []FriendRequest `json:"incoming"` // requests to the user
	Outgoing []FriendRequest `json:"outgoing"` // requests the user sent
}

// UserRelation is someone and how the user stands with them: friends, a
// request either way ("outgoing" was sent by the user), blocked by the user,
// or none. It answers GET /api/friends/lookup?handle= and the friend actions.
type UserRelation struct {
	User     User   `json:"user"`
	Relation string `json:"relation" tstype:"'none' | 'friends' | 'outgoing' | 'incoming' | 'blocked'"`
}

// SendFriendRequest is the body of POST /api/friends/requests. If the other
// person already asked, it accepts their request instead.
type SendFriendRequest struct {
	UserID string `json:"userId"`
}

// Realtime, app-wide: these messages concern the signed-in user rather than
// a room, so they have no "room".

// Notification is something to tell the user about at once, wherever they
// are on the site: User sent them a friend request or accepted theirs.
type Notification struct {
	Type string `json:"type" tstype:"'notification'"`
	Kind string `json:"kind" tstype:"'friendRequest' | 'friendAccepted'"`
	User User   `json:"user"`
}

// FriendsChanged means the user's friends or requests changed; the client
// fetches them again.
type FriendsChanged struct {
	Type string `json:"type" tstype:"'friendsChanged'"`
}

// FriendSuggestion is someone the user may know: a CupStream user with
// Discord linked who shares Discord servers with them. GET
// /api/friends/suggestions lists them, those sharing the most servers first.
type FriendSuggestion struct {
	User   User          `json:"user"`
	Guilds []SharedGuild `json:"guilds"`
}

type SharedGuild struct {
	ID      string `json:"id"`
	Name    string `json:"name"`
	IconURL string `json:"iconUrl,omitempty"`
}
