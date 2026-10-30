package gateway

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"testing"

	"github.com/RAdu2005/cupstream/server/internal/access"
)

// costreams starts n streams in alice's room: hers and n-1 co-streamers'.
func (f *fixture) costreams(t *testing.T, n int) []Started {
	t.Helper()
	ctx := context.Background()
	room := f.room(t)
	out := []Started{f.publish(t)}
	for i := 1; i < n; i++ {
		u := f.signIn(t, fmt.Sprintf("costreamer%d", i))
		if err := f.gw.rooms.SetAllowed(ctx, room, u.ID, true); err != nil {
			t.Fatal(err)
		}
		st, err := f.gw.Start(ctx, StartRequest{User: u, Source: SourceWeb, RoomSlug: room.Slug})
		if err != nil {
			t.Fatal(err)
		}
		f.post(t, st, "Bearer "+st.Token, http.StatusCreated)
		out = append(out, st)
	}
	f.reconcile(t)
	return out
}

func TestRoomStreamLimit(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	f.costreams(t, MaxRoomStreams)

	extra := f.signIn(t, "extra")
	room := f.room(t)
	if err := f.gw.rooms.SetAllowed(ctx, room, extra.ID, true); err != nil {
		t.Fatal(err)
	}
	_, err := f.gw.Start(ctx, StartRequest{User: extra, Source: SourceWeb, RoomSlug: room.Slug})
	var dest *DestinationError
	if !errors.As(err, &dest) || dest.Code != CodeRoomFull {
		t.Fatalf("stream %d: err = %v, want %s", MaxRoomStreams+1, err, CodeRoomFull)
	}

	// Replacing one's own stream in a full room is not one more stream.
	if _, err := f.gw.Start(ctx, StartRequest{User: f.user, Source: SourceWeb, RoomSlug: room.Slug, Replace: true}); err != nil {
		t.Fatalf("replacing a stream in a full room: %v", err)
	}
}

func TestWatchingLimit(t *testing.T) {
	f := newFixture(t)
	f.gw.DetectFeatures(context.Background())
	streams := f.costreams(t, MaxWatching+1)
	f.setVisibility(t, access.Everyone)
	bob := f.signIn(t, "bob")

	var sessions []viewerSession
	for _, st := range streams[:MaxWatching] {
		sessions = append(sessions, f.play(t, st, bob.ID, "tab=tab-one-1", http.StatusCreated))
	}
	last := streams[MaxWatching]
	f.play(t, last, bob.ID, "tab=tab-two-2", http.StatusConflict) // the limit spans tabs

	// A pre-warmed session opens, but cannot resume while four play.
	v := f.play(t, last, bob.ID, "tab=tab-one-1&paused=true&preview=false", http.StatusCreated)
	if c := f.control(t, last, v, "resume", ""); c != http.StatusConflict {
		t.Fatalf("fifth resume = %d, want 409", c)
	}
	// Listening without video still counts as watching.
	if c := f.control(t, streams[0], sessions[0], "pause", `{"video":true}`); c != http.StatusNoContent {
		t.Fatalf("pause video = %d", c)
	}
	if c := f.control(t, last, v, "resume", ""); c != http.StatusConflict {
		t.Fatalf("resume next to an audio-only stream = %d, want 409", c)
	}
	// Pausing one entirely makes room.
	if c := f.control(t, streams[0], sessions[0], "pause", ""); c != http.StatusNoContent {
		t.Fatalf("pause = %d", c)
	}
	if c := f.control(t, last, v, "resume", ""); c != http.StatusNoContent {
		t.Fatalf("resume after pausing another = %d, want 204", c)
	}
	// Resuming tracks of a session that already plays is not one more.
	if c := f.control(t, last, v, "resume", `{"video":true}`); c != http.StatusNoContent {
		t.Fatalf("resume a playing session = %d, want 204", c)
	}

	// Anonymous visitors are counted per tab.
	for _, st := range streams[:MaxWatching] {
		f.play(t, st, "", "tab=anon-tab-1", http.StatusCreated)
	}
	f.play(t, last, "", "tab=anon-tab-1", http.StatusConflict)
	f.play(t, last, "", "tab=anon-tab-2", http.StatusCreated)
}
