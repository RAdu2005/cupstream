package discord

import (
	"time"

	"github.com/bwmarrin/discordgo"
)

// Discord permission bits that CupStream reads.
const (
	permAdministrator   int64 = discordgo.PermissionAdministrator
	permManageGuild     int64 = discordgo.PermissionManageGuild
	permViewChannel     int64 = discordgo.PermissionViewChannel
	permReadHistory     int64 = discordgo.PermissionReadMessageHistory
	permManageMessages  int64 = discordgo.PermissionManageMessages
	permVideo           int64 = discordgo.PermissionVoiceStreamVideo // "Video": Go Live and camera
	permMoveMembers     int64 = discordgo.PermissionVoiceMoveMembers
	permAll                   = int64(^uint64(0) >> 1)
	permTimedOutAllowed       = permViewChannel | permReadHistory
)

// permInput is what a member's permissions in a channel depend on.
type permInput struct {
	guildID, ownerID string
	roles            map[string]int64 // permissions by role ID; @everyone has the guild's ID
	overwrites       []*discordgo.PermissionOverwrite
	userID           string
	memberRoles      []string
	timeoutUntil     time.Time
	now              time.Time
}

// channelPermissions computes a member's permissions in a channel the way
// Discord does: the server owner and administrators have every permission;
// otherwise @everyone's and the member's roles' permissions, then the
// channel's overwrites for @everyone, for the member's roles together and
// for the member. Without View Channel a member has none, and a member in
// timeout keeps only View Channel and Read Message History.
func channelPermissions(in permInput) int64 {
	if in.userID == in.ownerID {
		return permAll
	}
	perms := in.roles[in.guildID]
	for _, r := range in.memberRoles {
		perms |= in.roles[r]
	}
	if perms&permAdministrator != 0 {
		return permAll
	}

	var everyone, member *discordgo.PermissionOverwrite
	var allow, deny int64
	isRole := map[string]bool{}
	for _, r := range in.memberRoles {
		isRole[r] = true
	}
	for _, o := range in.overwrites {
		switch {
		case o.Type == discordgo.PermissionOverwriteTypeRole && o.ID == in.guildID:
			everyone = o
		case o.Type == discordgo.PermissionOverwriteTypeRole && isRole[o.ID]:
			allow |= o.Allow
			deny |= o.Deny
		case o.Type == discordgo.PermissionOverwriteTypeMember && o.ID == in.userID:
			member = o
		}
	}
	if everyone != nil {
		perms = perms&^everyone.Deny | everyone.Allow
	}
	perms = perms&^deny | allow
	if member != nil {
		perms = perms&^member.Deny | member.Allow
	}

	if perms&permViewChannel == 0 {
		return 0
	}
	if in.timeoutUntil.After(in.now) {
		perms &= permTimedOutAllowed
	}
	return perms
}
