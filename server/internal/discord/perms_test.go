package discord

import (
	"testing"
	"time"

	"github.com/bwmarrin/discordgo"
)

func TestChannelPermissions(t *testing.T) {
	const (
		guild = "g"
		mods  = "mods"
		muted = "muted"
		admin = "admin"
	)
	base := permViewChannel | permVideo
	roles := map[string]int64{guild: base, mods: permMoveMembers | permManageMessages, muted: 0, admin: permAdministrator}
	role := func(id string, allow, deny int64) *discordgo.PermissionOverwrite {
		return &discordgo.PermissionOverwrite{ID: id, Type: discordgo.PermissionOverwriteTypeRole, Allow: allow, Deny: deny}
	}
	user := func(id string, allow, deny int64) *discordgo.PermissionOverwrite {
		return &discordgo.PermissionOverwrite{ID: id, Type: discordgo.PermissionOverwriteTypeMember, Allow: allow, Deny: deny}
	}
	now := time.Unix(1_800_000_000, 0)

	cases := []struct {
		name       string
		user       string
		roles      []string
		overwrites []*discordgo.PermissionOverwrite
		timeout    time.Time
		want       int64 // bits that must be set
		wantNot    int64 // bits that must not be set
	}{
		{"@everyone's permissions", "u", nil, nil, time.Time{}, base, permMoveMembers},
		{"roles add up", "u", []string{mods}, nil, time.Time{}, base | permMoveMembers | permManageMessages, 0},
		{"owner has everything", "owner", nil, []*discordgo.PermissionOverwrite{role(guild, 0, permViewChannel)}, time.Time{}, permVideo | permMoveMembers, 0},
		{"administrator has everything", "u", []string{admin}, []*discordgo.PermissionOverwrite{user("u", 0, permVideo)}, time.Time{}, permVideo | permManageGuild, 0},
		{"channel denies video to @everyone", "u", nil, []*discordgo.PermissionOverwrite{role(guild, 0, permVideo)}, time.Time{}, permViewChannel, permVideo},
		{"role overwrite allows it back", "u", []string{mods}, []*discordgo.PermissionOverwrite{role(guild, 0, permVideo), role(mods, permVideo, 0)}, time.Time{}, permVideo, 0},
		{"role allow beats role deny", "u", []string{mods, muted}, []*discordgo.PermissionOverwrite{role(muted, 0, permVideo), role(mods, permVideo, 0)}, time.Time{}, permVideo, 0},
		{"member overwrite beats roles", "u", []string{mods}, []*discordgo.PermissionOverwrite{role(mods, permVideo, 0), user("u", 0, permVideo)}, time.Time{}, permViewChannel, permVideo},
		{"someone else's member overwrite", "u", nil, []*discordgo.PermissionOverwrite{user("v", 0, permVideo)}, time.Time{}, permVideo, 0},
		{"no view channel, nothing", "u", []string{mods}, []*discordgo.PermissionOverwrite{role(guild, 0, permViewChannel)}, time.Time{}, 0, permVideo | permMoveMembers},
		{"timeout keeps viewing only", "u", []string{mods}, nil, now.Add(time.Hour), permViewChannel, permVideo | permMoveMembers},
		{"expired timeout", "u", nil, nil, now.Add(-time.Hour), permVideo, 0},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := channelPermissions(permInput{
				guildID: guild, ownerID: "owner", roles: roles, overwrites: tc.overwrites,
				userID: tc.user, memberRoles: tc.roles, timeoutUntil: tc.timeout, now: now,
			})
			if got&tc.want != tc.want {
				t.Errorf("permissions %b lack %b", got, tc.want)
			}
			if got&tc.wantNot != 0 {
				t.Errorf("permissions %b include %b", got, got&tc.wantNot)
			}
		})
	}
}
