// Package discord is the CupStream bot. It follows the voice channels of the
// Discord servers it was added to, which decide who may watch and stream in
// their Discord rooms.
//
// The bot runs inside the server with only the non-privileged GUILDS and
// GUILD_VOICE_STATES intents: a snapshot of each server when it connects,
// then updates, all kept in memory. Voice states of people who do not use
// CupStream are never kept. While the bot is disconnected its view is stale:
// grace periods stand still and nobody is paused, disconnected or ended
// until it sees the servers again (rooms.Service.Stale).
package discord

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"sync"
	"time"

	"github.com/bwmarrin/discordgo"

	"github.com/RAdu2005/cupstream/server/internal/access"
	"github.com/RAdu2005/cupstream/server/internal/rooms"
	"github.com/RAdu2005/cupstream/server/internal/store"
)

const (
	// Rooms are checked again this long after a grace period ends, so that
	// it has run out by then.
	graceMargin = 100 * time.Millisecond
	// How long looking up someone's voice state may take.
	lookupTimeout = 3 * time.Second
	// How often people who left voice are forgotten.
	forgetInterval = time.Minute
)

// restAPI is the part of Discord's HTTP API the bot calls.
type restAPI interface {
	// UserVoiceState returns a user's voice state in a server, or nil when
	// they are not in voice there.
	UserVoiceState(ctx context.Context, guildID, userID string) (*discordgo.VoiceState, error)
	GuildMember(ctx context.Context, guildID, userID string) (*discordgo.Member, error)
}

type Bot struct {
	db    *store.DB
	rooms *rooms.Service
	token string
	now   func() time.Time
	after func(time.Duration, func())
	api   restAPI

	// Reevaluate applies a change of who may be in a room to its media
	// sessions and realtime subscriptions. Set it before Run.
	Reevaluate func(ctx context.Context, room store.Room)

	ctx context.Context // Run's, for scheduled work

	mu        sync.Mutex
	connected bool // a gateway session is up
	readyOnce bool // the bot has connected at least once
	guilds    map[string]*guild
	settings  map[string]store.DiscordGuild // by guild ID
}

func New(db *store.DB, rs *rooms.Service, token string) *Bot {
	return &Bot{
		db: db, rooms: rs, token: token, now: time.Now,
		after:      func(d time.Duration, f func()) { time.AfterFunc(d, f) },
		Reevaluate: func(context.Context, store.Room) {},
		ctx:        context.Background(),
		guilds:     map[string]*guild{},
		settings:   map[string]store.DiscordGuild{},
	}
}

// Run connects to Discord and follows it until ctx is done; discordgo
// reconnects by itself.
func (b *Bot) Run(ctx context.Context) error {
	b.ctx = ctx
	if err := b.loadSettings(ctx); err != nil {
		return err
	}
	s, err := discordgo.New("Bot " + b.token)
	if err != nil {
		return err
	}
	s.Identify.Intents = discordgo.IntentsGuilds | discordgo.IntentsGuildVoiceStates
	// The bot keeps its own state, without anyone else's voice state.
	s.StateEnabled = false
	// Events in order: a voice state must not overtake the snapshot before it.
	s.SyncEvents = true
	s.AddHandler(func(_ *discordgo.Session, e any) { b.handle(e) })
	b.api = sessionAPI{s}
	if err := s.Open(); err != nil {
		return err
	}
	defer s.Close()

	t := time.NewTicker(forgetInterval)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return nil
		case <-t.C:
			b.forget()
		}
	}
}

func (b *Bot) loadSettings(ctx context.Context) error {
	rows, err := b.db.ListDiscordGuilds(ctx)
	if err != nil {
		return err
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	for _, r := range rows {
		b.settings[r.GuildID] = r
	}
	return nil
}

func (b *Bot) forget() {
	b.mu.Lock()
	defer b.mu.Unlock()
	for _, g := range b.guilds {
		g.forget(g.clock(b.now()))
	}
}

// Policy implements rooms.Voice.
func (b *Bot) Policy(guildID string) access.StreamPolicy {
	b.mu.Lock()
	defer b.mu.Unlock()
	if s, ok := b.settings[guildID]; ok {
		return access.StreamPolicy(s.StreamPolicy)
	}
	return access.VideoPermission
}

// Stale implements rooms.Voice.
func (b *Bot) Stale(guildID string) bool {
	b.mu.Lock()
	defer b.mu.Unlock()
	if g := b.guilds[guildID]; g != nil {
		return g.stale()
	}
	return !b.readyOnce
}

// Member implements rooms.Voice. Someone the bot has not seen since its
// last snapshot (they linked Discord while in voice) is looked up once.
func (b *Bot) Member(ctx context.Context, guildID, channelID, userID string) rooms.VoiceMember {
	b.mu.Lock()
	g := b.guilds[guildID]
	if g == nil {
		// Before the first connection nothing is known; after it, the bot is
		// not in this server.
		defer b.mu.Unlock()
		return rooms.VoiceMember{Stale: !b.readyOnce}
	}
	if g.members[userID] != nil || g.lookedUp[userID] || g.stale() || b.api == nil {
		defer b.mu.Unlock()
		return g.voiceMember(channelID, userID, b.now())
	}
	b.mu.Unlock()

	ctx, cancel := context.WithTimeout(ctx, lookupTimeout)
	defer cancel()
	vs, err := b.api.UserVoiceState(ctx, guildID, userID)
	if err == nil && vs != nil && vs.Member == nil {
		vs.Member, err = b.api.GuildMember(ctx, guildID, userID)
	}

	b.mu.Lock()
	defer b.mu.Unlock()
	if err != nil {
		slog.WarnContext(ctx, "looking up a discord voice state failed", "guild", guildID, "err", err)
		return rooms.VoiceMember{Stale: true}
	}
	if b.guilds[guildID] == g && g.members[userID] == nil && !g.lookedUp[userID] {
		g.lookedUp[userID] = true
		if vs != nil && vs.ChannelID != "" {
			g.setVoice(userID, vs.ChannelID, vs.Member, b.now())
		}
	}
	return g.voiceMember(channelID, userID, b.now())
}

// recheck is work to do after an event: rooms to check again, now and
// after each delay.
type recheck struct {
	guildID  string
	channels []string // nil: every room in the server
	delays   []time.Duration
}

var (
	afterLeaving    = []time.Duration{viewerDelay, streamerGrace}
	afterPermChange = []time.Duration{0, streamerGrace}
	afterResync     = []time.Duration{0, viewerDelay, streamerGrace}
)

// handle applies one gateway event.
func (b *Bot) handle(e any) {
	var todo []recheck
	switch e := e.(type) {
	case *discordgo.Ready:
		todo = b.onReady(e)
	case *discordgo.Resumed:
		todo = b.onResumed()
	case *discordgo.Disconnect:
		b.onDisconnect()
	case *discordgo.GuildCreate:
		todo = b.onGuildCreate(e.Guild)
	case *discordgo.GuildUpdate:
		todo = b.onGuildUpdate(e.Guild)
	case *discordgo.GuildDelete:
		todo = b.onGuildDelete(e.Guild)
	case *discordgo.GuildRoleCreate:
		todo = b.onRole(e.GuildID, e.Role.ID, e.Role.Permissions, false)
	case *discordgo.GuildRoleUpdate:
		todo = b.onRole(e.GuildID, e.Role.ID, e.Role.Permissions, false)
	case *discordgo.GuildRoleDelete:
		todo = b.onRole(e.GuildID, e.RoleID, 0, true)
	case *discordgo.ChannelCreate:
		todo = b.onChannel(e.Channel, false)
	case *discordgo.ChannelUpdate:
		todo = b.onChannel(e.Channel, false)
	case *discordgo.ChannelDelete:
		todo = b.onChannel(e.Channel, true)
	case *discordgo.VoiceStateUpdate:
		todo = b.onVoiceState(e.VoiceState)
	}
	for _, r := range todo {
		b.schedule(r)
	}
}

// updateStaleLocked marks g stale or current after the connection or the
// server's availability changed.
func (b *Bot) updateStaleLocked(g *guild) {
	stale := !b.connected || !g.synced || g.unavailable
	switch {
	case stale && g.staleSince.IsZero():
		g.staleSince = b.now()
	case !stale:
		g.staleSince = time.Time{}
	}
}

func (b *Bot) onReady(e *discordgo.Ready) []recheck {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.connected, b.readyOnce = true, true
	in := map[string]bool{}
	for _, eg := range e.Guilds {
		in[eg.ID] = true
		g := b.guilds[eg.ID]
		if g == nil {
			g = newGuild(eg.ID)
			b.guilds[eg.ID] = g
		}
		// A new session: every server is stale until its snapshot arrives.
		g.synced = false
		b.updateStaleLocked(g)
	}
	var todo []recheck
	for id := range b.guilds {
		if !in[id] {
			// Removed while the bot was away.
			delete(b.guilds, id)
			todo = append(todo, recheck{guildID: id, delays: []time.Duration{0}})
		}
	}
	slog.Info("discord bot connected", "servers", len(e.Guilds))
	return todo
}

func (b *Bot) onResumed() []recheck {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.connected = true
	var todo []recheck
	for _, g := range b.guilds {
		wasStale := g.stale()
		b.updateStaleLocked(g)
		if wasStale && !g.stale() {
			todo = append(todo, recheck{guildID: g.id, delays: afterResync})
		}
	}
	slog.Info("discord bot reconnected")
	return todo
}

func (b *Bot) onDisconnect() {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.connected = false
	for _, g := range b.guilds {
		b.updateStaleLocked(g)
	}
	slog.Warn("discord bot disconnected; holding access changes until it reconnects")
}

// linked reports whether a Discord user uses CupStream; the bot keeps no
// voice state of anyone else.
func (b *Bot) linked(userID string) bool {
	ok, err := b.db.IsDiscordLinked(b.ctx, userID)
	if err != nil {
		slog.Error("checking a discord identity failed", "err", err)
	}
	return ok
}

// onGuildCreate takes a server's snapshot: on connecting, when the bot is
// added, or after an outage.
func (b *Bot) onGuildCreate(dg *discordgo.Guild) []recheck {
	if dg.Unavailable {
		return nil
	}
	voice := map[string]*discordgo.VoiceState{}
	for _, vs := range dg.VoiceStates {
		if vs.ChannelID != "" && b.linked(vs.UserID) {
			voice[vs.UserID] = vs
		}
	}
	members := map[string]*discordgo.Member{}
	for _, m := range dg.Members {
		if m.User != nil && voice[m.User.ID] != nil {
			members[m.User.ID] = m
		}
	}
	if err := b.db.EnsureDiscordGuild(b.ctx, store.EnsureDiscordGuildParams{GuildID: dg.ID, CreatedAt: b.now().Unix()}); err != nil {
		slog.Error("saving a discord server failed", "guild", dg.ID, "err", err)
	}
	settings, err := b.db.GetDiscordGuild(b.ctx, dg.ID)
	if err != nil {
		slog.Error("loading a discord server's settings failed", "guild", dg.ID, "err", err)
	}

	b.mu.Lock()
	defer b.mu.Unlock()
	if err == nil {
		b.settings[dg.ID] = settings
	}
	now := b.now()
	old := b.guilds[dg.ID]
	g := newGuild(dg.ID)
	g.name, g.icon, g.ownerID = dg.Name, dg.Icon, dg.OwnerID
	for _, r := range dg.Roles {
		g.roles[r.ID] = r.Permissions
	}
	for _, c := range dg.Channels {
		g.channels[c.ID] = newChannel(c)
	}
	for id, vs := range voice {
		dm := vs.Member
		if dm == nil {
			dm = members[id]
		}
		g.setVoice(id, vs.ChannelID, dm, now)
	}
	if old != nil {
		// People who left voice while the bot was away get their grace
		// periods from now on.
		for id, m := range old.members {
			nm := g.members[id]
			switch {
			case m.channelID != "" && (nm == nil || nm.channelID != m.channelID):
				if nm == nil {
					nm = &member{roles: m.roles, timeoutUntil: m.timeoutUntil}
					g.members[id] = nm
				}
				nm.leftChannel, nm.leftAt = m.channelID, now
			case m.channelID == "" && nm == nil:
				g.members[id] = m
			}
		}
		g.staleSince = old.staleSince
		g.noteVideoLoss(old.canVideo(now), now)
	}
	g.synced = true
	b.guilds[dg.ID] = g
	b.updateStaleLocked(g)
	return []recheck{{guildID: dg.ID, delays: afterResync}}
}

func (b *Bot) onGuildUpdate(dg *discordgo.Guild) []recheck {
	b.mu.Lock()
	defer b.mu.Unlock()
	g := b.guilds[dg.ID]
	if g == nil {
		return nil
	}
	now := b.now()
	before := g.canVideo(now)
	g.name, g.icon, g.ownerID = dg.Name, dg.Icon, dg.OwnerID
	g.noteVideoLoss(before, now)
	return []recheck{{guildID: g.id, delays: afterPermChange}}
}

func (b *Bot) onGuildDelete(dg *discordgo.Guild) []recheck {
	b.mu.Lock()
	defer b.mu.Unlock()
	g := b.guilds[dg.ID]
	if g == nil {
		return nil
	}
	if dg.Unavailable {
		// An outage: the server comes back with a new snapshot.
		g.unavailable = true
		b.updateStaleLocked(g)
		return nil
	}
	delete(b.guilds, dg.ID)
	slog.Info("discord bot removed from a server", "guild", dg.ID)
	return []recheck{{guildID: dg.ID, delays: []time.Duration{0}}}
}

func (b *Bot) onRole(guildID, roleID string, perms int64, deleted bool) []recheck {
	b.mu.Lock()
	defer b.mu.Unlock()
	g := b.guilds[guildID]
	if g == nil {
		return nil
	}
	now := b.now()
	before := g.canVideo(now)
	if deleted {
		delete(g.roles, roleID)
	} else {
		g.roles[roleID] = perms
	}
	g.noteVideoLoss(before, now)
	return []recheck{{guildID: guildID, delays: afterPermChange}}
}

func (b *Bot) onChannel(c *discordgo.Channel, deleted bool) []recheck {
	b.mu.Lock()
	defer b.mu.Unlock()
	g := b.guilds[c.GuildID]
	if g == nil {
		return nil
	}
	now := b.now()
	before := g.canVideo(now)
	if deleted {
		delete(g.channels, c.ID)
	} else {
		g.channels[c.ID] = newChannel(c)
	}
	g.noteVideoLoss(before, now)
	return []recheck{{guildID: g.id, channels: []string{c.ID}, delays: afterPermChange}}
}

func (b *Bot) onVoiceState(vs *discordgo.VoiceState) []recheck {
	if vs == nil || vs.GuildID == "" {
		return nil
	}
	linked := b.linked(vs.UserID)

	b.mu.Lock()
	defer b.mu.Unlock()
	g := b.guilds[vs.GuildID]
	if g == nil {
		return nil
	}
	if !linked {
		// Not (or no longer) a CupStream user.
		delete(g.members, vs.UserID)
		return nil
	}
	now := b.now()
	before := g.canVideo(now)
	left, joined, permsChanged := g.setVoice(vs.UserID, vs.ChannelID, vs.Member, now)
	g.noteVideoLoss(before, now)

	var todo []recheck
	if left != "" {
		todo = append(todo, recheck{guildID: g.id, channels: []string{left}, delays: afterLeaving})
	}
	switch {
	case joined != "":
		todo = append(todo, recheck{guildID: g.id, channels: []string{joined}, delays: []time.Duration{0}})
	case permsChanged && vs.ChannelID != "":
		todo = append(todo, recheck{guildID: g.id, channels: []string{vs.ChannelID}, delays: afterPermChange})
	}
	return todo
}

// schedule checks the rooms of r again after each of its delays.
func (b *Bot) schedule(r recheck) {
	for _, d := range r.delays {
		if d > 0 {
			d += graceMargin
		}
		b.after(d, func() { b.reevaluate(r.guildID, r.channels) })
	}
}

func (b *Bot) reevaluate(guildID string, channels []string) {
	ctx := b.ctx
	var list []store.Room
	if channels == nil {
		rs, err := b.db.ListDiscordRooms(ctx, sql.NullString{String: guildID, Valid: true})
		if err != nil {
			slog.Error("listing discord rooms failed", "guild", guildID, "err", err)
			return
		}
		list = rs
	}
	for _, c := range channels {
		room, err := b.db.GetDiscordRoom(ctx, store.GetDiscordRoomParams{
			GuildID: sql.NullString{String: guildID, Valid: true}, ChannelID: sql.NullString{String: c, Valid: true},
		})
		switch {
		case err == nil:
			list = append(list, room)
		case !errors.Is(err, sql.ErrNoRows):
			slog.Error("loading a discord room failed", "guild", guildID, "err", err)
		}
	}
	for _, room := range list {
		b.Reevaluate(ctx, room)
	}
}

// sessionAPI is restAPI on a discordgo session.
type sessionAPI struct{ s *discordgo.Session }

func (a sessionAPI) UserVoiceState(ctx context.Context, guildID, userID string) (*discordgo.VoiceState, error) {
	endpoint := discordgo.EndpointGuild(guildID) + "/voice-states/"
	body, err := a.s.RequestWithBucketID(http.MethodGet, endpoint+userID, nil, endpoint, discordgo.WithContext(ctx))
	var re *discordgo.RESTError
	if errors.As(err, &re) && re.Response != nil && re.Response.StatusCode == http.StatusNotFound {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var vs discordgo.VoiceState
	return &vs, json.Unmarshal(body, &vs)
}

func (a sessionAPI) GuildMember(ctx context.Context, guildID, userID string) (*discordgo.Member, error) {
	return a.s.GuildMember(guildID, userID, discordgo.WithContext(ctx))
}
