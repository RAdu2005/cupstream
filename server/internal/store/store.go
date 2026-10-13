// Package store owns the SQLite database: opening it, running migrations and
// the sqlc-generated queries (*.gen.go, regenerate with `sqlc generate`).
package store

import (
	"context"
	"database/sql"
	"embed"
	"errors"
	"fmt"
	"io/fs"
	"net/url"
	"os"
	"path/filepath"

	"github.com/pressly/goose/v3"
	"modernc.org/sqlite"
	sqlite3 "modernc.org/sqlite/lib"
)

//go:embed migrations/*.sql
var migrations embed.FS

// DB wraps the connection pool and the generated queries.
type DB struct {
	*Queries
	SQL *sql.DB
}

// Open opens (creating if needed) the SQLite database at path and applies all
// pending migrations. Use ":memory:" for a throwaway database in tests.
func Open(ctx context.Context, path string) (*DB, error) {
	if path != ":memory:" {
		if err := os.MkdirAll(filepath.Dir(path), 0o750); err != nil {
			return nil, fmt.Errorf("create database directory: %w", err)
		}
	}

	q := url.Values{}
	for _, p := range []string{
		"journal_mode(WAL)",
		"synchronous(NORMAL)",
		"foreign_keys(ON)",
		"busy_timeout(5000)",
	} {
		q.Add("_pragma", p)
	}
	// Write transactions take the lock up front, so concurrent writers wait on
	// busy_timeout instead of failing with SQLITE_BUSY halfway through.
	q.Set("_txlock", "immediate")

	db, err := sql.Open("sqlite", "file:"+path+"?"+q.Encode())
	if err != nil {
		return nil, err
	}
	if path == ":memory:" {
		// Every connection to :memory: is a separate database.
		db.SetMaxOpenConns(1)
	}
	if err := db.PingContext(ctx); err != nil {
		db.Close()
		return nil, fmt.Errorf("open database: %w", err)
	}
	if err := migrate(ctx, db); err != nil {
		db.Close()
		return nil, err
	}
	return &DB{Queries: New(db), SQL: db}, nil
}

func migrate(ctx context.Context, db *sql.DB) error {
	dir, err := fs.Sub(migrations, "migrations")
	if err != nil {
		return err
	}
	p, err := goose.NewProvider(goose.DialectSQLite3, db, dir)
	if err != nil {
		return fmt.Errorf("migrations: %w", err)
	}
	if _, err := p.Up(ctx); err != nil {
		return fmt.Errorf("migrate: %w", err)
	}
	return nil
}

// Tx runs fn inside a transaction, committing if it returns nil.
func (d *DB) Tx(ctx context.Context, fn func(q *Queries) error) error {
	tx, err := d.SQL.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback() //nolint:errcheck // no-op after commit
	if err := fn(d.WithTx(tx)); err != nil {
		return err
	}
	return tx.Commit()
}

func (d *DB) Close() error { return d.SQL.Close() }

// IsUniqueViolation reports whether err is a UNIQUE or PRIMARY KEY constraint failure.
func IsUniqueViolation(err error) bool {
	var e *sqlite.Error
	if !errors.As(err, &e) {
		return false
	}
	return e.Code() == sqlite3.SQLITE_CONSTRAINT_UNIQUE || e.Code() == sqlite3.SQLITE_CONSTRAINT_PRIMARYKEY
}

// UserSettings returns the user's settings, creating the defaults if the
// user has none yet.
func (d *DB) UserSettings(ctx context.Context, userID string) (UserSetting, error) {
	if err := d.CreateUserSettings(ctx, userID); err != nil {
		return UserSetting{}, err
	}
	return d.GetUserSettings(ctx, userID)
}
