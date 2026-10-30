package discord

import (
	"time"

	"github.com/bwmarrin/discordgo"

	"github.com/RAdu2005/cupstream/server/internal/rooms"
)

const (
	// A viewer who leaves voice keeps their media this long: it absorbs quick
	// leave-and-rejoin flaps.
	viewerDelay = 1500 * time.Millisecond
	// A streamer who leaves voice or loses the Video permission keeps
	// streaming this long.
	streamerGrace = 12 * time.Second
	// People who left voice are forgotten once no grace period needs them.
	forgetAfter = time.Minute
)

// guild is what the bot knows about a Discord server.
type guild struct {
	id, name, icon, ownerID string
	roles                   map[string]int64 // permissions by role ID; @everyone has the guild's ID
	channels                map[string]*channel
	// CupStream users who are in voice or left recently, by Discord user ID.
	members map[string]*member
	// Users whose voice state was looked up since the last snapshot.
	lookedUp map[string]bool

	synced      bool      // a snapshot arrived in the current gateway session
	unavailable bool      // Discord reported an outage of the server
	staleSince  time.Time // when the bot's view went out of date; zero while it is current
}

type channel struct {
	id, name   string
	voice      bool // a voice or stage channel
	overwrites []*discordgo.PermissionOverwrite
}

// member is a CupStream user in a server's voice channels.
type member struct {
	channelID    string // "" when not in voice
	roles        []string
	timeoutUntil time.Time

	// The channel they left last, and when, for the grace periods.
	leftChannel string
	leftAt      time.Time
	// When they lost the Video permission in their channel, for the
	// streamers' grace period.
	videoLostAt time.Time
}

func newGuild(id string) *guild {
	return &guild{
		id:       id,
		roles:    map[string]int64{},
		channels: map[string]*channel{},
		members:  map[string]*member{},
		lookedUp: map[string]bool{},
	}
}

func newChannel(c *discordgo.Channel) *channel {
	return &channel{
		id: c.ID, name: c.Name, overwrites: c.PermissionOverwrites,
		voice: c.Type == discordgo.ChannelTypeGuildVoice || c.Type == discordgo.ChannelTypeGuildStageVoice,
	}
}

func (g *guild) stale() bool { return !g.staleSince.IsZero() }

// clock is the time grace periods are measured against. While the view is
// stale it stands still, so that no grace period runs out until the bot
// sees again who is in voice.
func (g *guild) clock(now time.Time) time.Time {
	if g.stale() {
		return g.staleSince
	}
	return now
}

func (g *guild) perms(ch *channel, userID string, m *member, now time.Time) int64 {
	return channelPermissions(permInput{
		guildID: g.id, ownerID: g.ownerID, roles: g.roles, overwrites: ch.overwrites,
		userID: userID, memberRoles: m.roles, timeoutUntil: m.timeoutUntil, now: now,
	})
}

// canVideo lists who has the Video permission in their voice channel now.
func (g *guild) canVideo(now time.Time) map[string]string {
	out := map[string]string{} // user ID -> channel ID
	for id, m := range g.members {
		if ch := g.channels[m.channelID]; ch != nil && g.perms(ch, id, m, now)&permVideo != 0 {
			out[id] = m.channelID
		}
	}
	return out
}

// noteVideoLoss starts the grace period of members who had the Video
// permission in their channel before a change (before, from canVideo) and
// no longer have it.
func (g *guild) noteVideoLoss(before map[string]string, now time.Time) {
	after := g.canVideo(now)
	for id, m := range g.members {
		switch {
		case after[id] != "":
			m.videoLostAt = time.Time{}
		case before[id] != "" && before[id] == m.channelID:
			m.videoLostAt = now
		}
	}
}

// setVoice applies a voice state of a CupStream user. It reports the channel
// they left and the one they joined ("" for none), and whether their
// permissions may have changed.
func (g *guild) setVoice(userID, channelID string, dm *discordgo.Member, now time.Time) (left, joined string, permsChanged bool) {
	m := g.members[userID]
	if m == nil {
		m = &member{}
		g.members[userID] = m
	}
	if dm != nil {
		var until time.Time
		if dm.CommunicationDisabledUntil != nil {
			until = *dm.CommunicationDisabledUntil
		}
		permsChanged = !equalStrings(m.roles, dm.Roles) || !until.Equal(m.timeoutUntil)
		m.roles, m.timeoutUntil = dm.Roles, until
	}
	if m.channelID != channelID {
		if m.channelID != "" {
			left = m.channelID
			m.leftChannel, m.leftAt = m.channelID, now
		}
		m.channelID, joined = channelID, channelID
		m.videoLostAt = time.Time{}
	}
	return left, joined, permsChanged
}

// voiceMember answers rooms.Voice.Member from what the bot saw.
func (g *guild) voiceMember(channelID, userID string, now time.Time) rooms.VoiceMember {
	now = g.clock(now)
	vm := rooms.VoiceMember{Stale: g.stale()}
	m := g.members[userID]
	if m == nil {
		return vm
	}
	in := m.channelID == channelID
	left := m.leftChannel == channelID && !m.leftAt.IsZero()
	vm.InChannel = in || left && now.Sub(m.leftAt) < viewerDelay
	vm.InChannelToStream = in || left && now.Sub(m.leftAt) < streamerGrace
	ch := g.channels[channelID]
	if ch == nil {
		return vm
	}
	p := g.perms(ch, userID, m, now)
	vm.CanVideo = p&permVideo != 0 || in && !m.videoLostAt.IsZero() && now.Sub(m.videoLostAt) < streamerGrace
	vm.CanMoveMembers = p&permMoveMembers != 0
	vm.CanManageMessages = p&permManageMessages != 0
	return vm
}

// forget drops people who left voice long enough ago.
func (g *guild) forget(now time.Time) {
	for id, m := range g.members {
		if m.channelID == "" && now.Sub(m.leftAt) > forgetAfter {
			delete(g.members, id)
		}
	}
}

func equalStrings(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	seen := map[string]bool{}
	for _, s := range a {
		seen[s] = true
	}
	for _, s := range b {
		if !seen[s] {
			return false
		}
	}
	return true
}
