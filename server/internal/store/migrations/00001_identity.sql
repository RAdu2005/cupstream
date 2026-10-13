-- +goose Up
-- Times are Unix seconds (UTC).

CREATE TABLE users (
    id           TEXT PRIMARY KEY,
    handle       TEXT NOT NULL UNIQUE COLLATE NOCASE,
    display_name TEXT NOT NULL,
    avatar_url   TEXT,
    created_at   INTEGER NOT NULL
) STRICT;

-- Provider identities (google, discord, and dev for the development login).
CREATE TABLE identities (
    provider   TEXT NOT NULL,
    subject    TEXT NOT NULL,
    user_id    TEXT NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    username   TEXT NOT NULL,
    created_at INTEGER NOT NULL,
    PRIMARY KEY (provider, subject)
) STRICT;

CREATE INDEX identities_user_id ON identities (user_id);

-- Website and desktop-app sessions. Only the SHA-256 hash of the token is stored.
CREATE TABLE sessions (
    id           TEXT PRIMARY KEY,
    token_hash   BLOB NOT NULL UNIQUE,
    user_id      TEXT NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    kind         TEXT NOT NULL CHECK (kind IN ('web', 'app')),
    device_name  TEXT NOT NULL DEFAULT '',
    created_at   INTEGER NOT NULL,
    expires_at   INTEGER NOT NULL,
    last_seen_at INTEGER NOT NULL
) STRICT;

CREATE INDEX sessions_user_id ON sessions (user_id);
CREATE INDEX sessions_expires_at ON sessions (expires_at);

-- +goose Down
DROP TABLE sessions;
DROP TABLE identities;
DROP TABLE users;
