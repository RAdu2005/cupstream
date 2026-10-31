package api

// Invite is an invite link to a link room. The link itself is only shown
// once, when it is created. Times are Unix seconds.
type Invite struct {
	ID        string `json:"id"`
	CanStream bool   `json:"canStream"`
	// ExpiresAt is when the link stops working; links without it work until
	// revoked, or until the room's streams end (UntilStreamEnd).
	ExpiresAt      int64 `json:"expiresAt,omitempty"`
	UntilStreamEnd bool  `json:"untilStreamEnd"`
	MaxUses        int64 `json:"maxUses,omitempty"` // 0: no limit
	Uses           int64 `json:"uses"`
	CreatedAt      int64 `json:"createdAt"`
}

// CreateInviteRequest is the body of POST /api/rooms/{slug}/invites.
// "stream" lasts until the room has no live stream left.
type CreateInviteRequest struct {
	Expiry    string `json:"expiry" tstype:"'1h' | '24h' | 'stream' | 'never'"`
	MaxUses   int64  `json:"maxUses,omitempty"`
	CanStream bool   `json:"canStream"`
}

type NewInvite struct {
	Invite Invite `json:"invite"`
	URL    string `json:"url"`
}

// InviteToken is the body of POST /api/invites/preview and .../redeem: the
// token from an invite link (/i/<token>).
type InviteToken struct {
	Token string `json:"token"`
}

// InvitePreview is the response of POST /api/invites/preview: where a valid
// link leads, so the page can ask the visitor to sign in first.
type InvitePreview struct {
	RoomSlug  string `json:"roomSlug"`
	Owner     User   `json:"owner"`
	CanStream bool   `json:"canStream"`
}

// RedeemedInvite is the response of POST /api/invites/redeem.
type RedeemedInvite struct {
	RoomSlug string `json:"roomSlug"`
}
