package store

import (
	"context"
	"database/sql"
	"io/fs"
	"path/filepath"
	"testing"

	"github.com/pressly/goose/v3"
)

// A database from before accounts and rooms existed gains settings and a
// link room for each user.
func TestMigrateExistingUsers(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "old.db")

	raw, err := sql.Open("sqlite", "file:"+path)
	if err != nil {
		t.Fatal(err)
	}
	dir, _ := fs.Sub(migrations, "migrations")
	p, err := goose.NewProvider(goose.DialectSQLite3, raw, dir)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := p.UpTo(ctx, 1); err != nil {
		t.Fatal(err)
	}
	if _, err := raw.Exec(`INSERT INTO users (id, handle, display_name, created_at) VALUES ('u1', 'old', 'Old', 0)`); err != nil {
		t.Fatal(err)
	}
	raw.Close()

	db, err := Open(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if s, err := db.GetUserSettings(ctx, "u1"); err != nil || s.StartMode != "discord_auto" {
		t.Fatalf("settings: %+v, %v", s, err)
	}
	if r, err := db.GetLinkRoomByOwner(ctx, sql.NullString{String: "u1", Valid: true}); err != nil || r.Visibility.String != "invite_only" || len(r.Slug) != 10 {
		t.Fatalf("link room: %+v, %v", r, err)
	}
}
