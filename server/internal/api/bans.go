package api

// BanEntry is someone banned from a link room. BannedAt is Unix seconds.
type BanEntry struct {
	User     User  `json:"user"`
	BannedAt int64 `json:"bannedAt"`
}
