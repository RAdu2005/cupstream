// Package view converts stored and live records into the API's JSON types,
// for both the HTTP API and the realtime WebSocket.
package view

import (
	"context"
	"time"

	"github.com/RAdu2005/cupstream/server/internal/api"
	"github.com/RAdu2005/cupstream/server/internal/gateway"
	"github.com/RAdu2005/cupstream/server/internal/store"
)

func User(u store.User) api.User {
	return api.User{ID: u.ID, Handle: u.Handle, DisplayName: u.DisplayName, AvatarURL: u.AvatarURL.String}
}

// Users loads users by ID, each at most once.
type Users struct {
	DB *store.DB
	m  map[string]api.User
}

func (c *Users) Get(ctx context.Context, id string) (api.User, error) {
	if u, ok := c.m[id]; ok {
		return u, nil
	}
	u, err := c.DB.GetUser(ctx, id)
	if err != nil {
		return api.User{}, err
	}
	if c.m == nil {
		c.m = map[string]api.User{}
	}
	c.m[id] = User(u)
	return c.m[id], nil
}

func Stream(ctx context.Context, users *Users, ls gateway.LiveStream) (api.Stream, error) {
	u, err := users.Get(ctx, ls.Stream.UserID)
	return api.Stream{
		ID: ls.Stream.ID, User: u, Source: ls.Stream.Source, State: string(ls.State),
		StartedAt: ls.Stream.StartedAt, Viewers: ls.Viewers,
		Bitrate: ls.Stats.Bitrate, LossRatio: ls.Stats.LossRatio,
		KeyFrameInterval: ls.Stats.KeyFrameInterval.Seconds(),
		ThumbnailAt:      unixMilli(ls.ThumbnailAt),
	}, err
}

// unixMilli is t in Unix milliseconds, or 0 for the zero time.
func unixMilli(t time.Time) int64 {
	if t.IsZero() {
		return 0
	}
	return t.UnixMilli()
}

func ChatMessage(m store.ChatMessage, u store.User) api.ChatMessage {
	return api.ChatMessage{ID: m.ID, User: User(u), Body: m.Body, CreatedAt: m.CreatedAt}
}
