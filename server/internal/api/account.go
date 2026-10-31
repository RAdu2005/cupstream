package api

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
