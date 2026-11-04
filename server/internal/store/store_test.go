package store

import (
	"context"
	"database/sql"
	"io/fs"
	"path/filepath"
	"testing"
	"testing/fstest"

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
	if r, err := db.GetLinkRoomByOwner(ctx, sql.NullString{String: "u1", Valid: true}); err != nil || r.Visibility.String != "invite_only" || r.Slug != "old" {
		t.Fatalf("link room: %+v, %v", r, err)
	}
}

// Link rooms are renamed after their owner's handle, except when a room
// already has that slug.
func TestMigrateHandleSlugs(t *testing.T) {
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
	if _, err := p.UpTo(ctx, 10); err != nil {
		t.Fatal(err)
	}
	if _, err := raw.Exec(`
		INSERT INTO users (id, handle, display_name, created_at) VALUES ('u1', 'ana', 'Ana', 0), ('u2', '0a1b2c3d4e', 'Hex', 0);
		INSERT INTO rooms (id, kind, slug, owner_id, visibility, created_at) VALUES
			('r1', 'link', 'ffffffffff', 'u1', 'invite_only', 0),
			('r2', 'link', 'eeeeeeeeee', 'u2', 'invite_only', 0);
		INSERT INTO rooms (id, kind, slug, guild_id, channel_id, created_at) VALUES ('r3', 'discord', '0a1b2c3d4e', 'g', 'c', 0);
	`); err != nil {
		t.Fatal(err)
	}
	raw.Close()

	db, err := Open(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	for id, want := range map[string]string{"r1": "ana", "r2": "eeeeeeeeee", "r3": "0a1b2c3d4e"} {
		if r, err := db.GetRoom(ctx, id); err != nil || r.Slug != want {
			t.Errorf("room %s: slug %q, %v; want %q", id, r.Slug, err, want)
		}
	}
}

// A migration numbered below one that is already applied, from a pull request
// that merged later, is applied rather than refused.
func TestMigrateOutOfOrder(t *testing.T) {
	ctx := context.Background()
	db, err := sql.Open("sqlite", "file:"+filepath.Join(t.TempDir(), "order.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	sqlFile := func(table string) *fstest.MapFile {
		return &fstest.MapFile{Data: []byte("-- +goose Up\nCREATE TABLE " + table + " (id INTEGER);\n")}
	}
	dir := fstest.MapFS{
		"00001_a.sql":          sqlFile("a"),
		"20261031120000_c.sql": sqlFile("c"),
	}
	if err := migrateFS(ctx, db, dir); err != nil {
		t.Fatal(err)
	}
	dir["20261030120000_b.sql"] = sqlFile("b")
	if err := migrateFS(ctx, db, dir); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`INSERT INTO b (id) VALUES (1)`); err != nil {
		t.Fatalf("the late migration did not run: %v", err)
	}
}
