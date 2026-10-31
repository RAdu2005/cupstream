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
	if r, err := db.GetLinkRoomByOwner(ctx, sql.NullString{String: "u1", Valid: true}); err != nil || r.Visibility.String != "invite_only" || len(r.Slug) != 10 {
		t.Fatalf("link room: %+v, %v", r, err)
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
