package realtime

import (
	"context"
	"database/sql"
	"testing"
	"time"

	"github.com/RAdu2005/cupstream/server/internal/auth"
	"github.com/RAdu2005/cupstream/server/internal/oauth"
	"github.com/RAdu2005/cupstream/server/internal/store"
)

func TestLimiter(t *testing.T) {
	l := newLimiter(3, time.Second)
	now := time.Unix(1000, 0)
	for i := range 3 {
		if !l.allow("bob", now) {
			t.Fatalf("message %d refused", i+1)
		}
	}
	if l.allow("bob", now) {
		t.Fatal("fourth message at once allowed")
	}
	if !l.allow("alice", now) {
		t.Fatal("limits are per user")
	}
	if l.allow("bob", now.Add(900*time.Millisecond)) {
		t.Fatal("allowed before a refill")
	}
	if !l.allow("bob", now.Add(1900*time.Millisecond)) || l.allow("bob", now.Add(1900*time.Millisecond)) {
		t.Fatal("one message per refill")
	}

	// Idle users are forgotten once their bucket is full again.
	l.prune(now.Add(2 * time.Second))
	if _, ok := l.buckets["bob"]; !ok || len(l.buckets) != 1 {
		t.Fatalf("buckets %v: only bob's is still not full", l.buckets)
	}
	l.prune(now.Add(time.Minute))
	if len(l.buckets) != 0 {
		t.Fatalf("%d buckets after a minute", len(l.buckets))
	}
}

func TestCleanChatBody(t *testing.T) {
	long := ""
	for range maxChatBody {
		long += "é"
	}
	for in, want := range map[string]string{
		"  hello  ":              "hello",
		"line\r\nbreak\x07":      "line\nbreak",
		"tab\there​":             "tab\there​",
		long:                     long,
		"\x00\x1b[31mred\x1b[0m": "[31mred[0m",
	} {
		if got, err := cleanChatBody(in); err != nil || got != want {
			t.Errorf("cleanChatBody(%q) = %q, %v; want %q", in, got, err, want)
		}
	}
	for _, in := range []string{"", " \n\t ", "\x00", long + "a", "bad \xff utf-8"} {
		if got, err := cleanChatBody(in); err == nil {
			t.Errorf("cleanChatBody(%q) = %q, want an error", in, got)
		}
	}
}

// TestChatExpiry: messages are no longer sent after 24 hours, and deleted
// by the cleanup.
func TestChatExpiry(t *testing.T) {
	ctx := context.Background()
	db, err := store.Open(ctx, ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	u, _, err := auth.NewAccounts(db, nil).SignIn(ctx, oauth.Identity{Provider: auth.ProviderDev, Subject: "alice", Username: "alice"})
	if err != nil {
		t.Fatal(err)
	}
	room, err := db.GetLinkRoomByOwner(ctx, nullString(u.ID))
	if err != nil {
		t.Fatal(err)
	}

	start := time.Unix(1_800_000_000, 0)
	now := start
	h := &Hub{db: db, now: func() time.Time { return now }}
	for i, body := range []string{"first", "second"} {
		err := db.CreateChatMessage(ctx, store.CreateChatMessageParams{
			ID: body, RoomID: room.ID, UserID: u.ID, Body: body, CreatedAt: start.Add(time.Duration(i) * time.Hour).Unix(),
		})
		if err != nil {
			t.Fatal(err)
		}
	}
	bodies := func() []string {
		msgs, err := h.chatHistory(ctx, room.ID)
		if err != nil {
			t.Fatal(err)
		}
		var out []string
		for _, m := range msgs {
			out = append(out, m.Body)
		}
		return out
	}

	now = start.Add(23 * time.Hour)
	h.deleteExpiredChat(ctx)
	if got := bodies(); len(got) != 2 || got[0] != "first" {
		t.Fatalf("after 23 hours: %v", got)
	}
	now = start.Add(24 * time.Hour)
	if got := bodies(); len(got) != 1 || got[0] != "second" {
		t.Fatalf("after 24 hours: %v", got)
	}
	h.deleteExpiredChat(ctx)
	if _, err := db.GetChatMessage(ctx, "first"); err == nil {
		t.Fatal("the expired message is still stored")
	}
	if _, err := db.GetChatMessage(ctx, "second"); err != nil {
		t.Fatalf("the newer message: %v", err)
	}
}

func nullString(s string) sql.NullString { return sql.NullString{String: s, Valid: true} }
