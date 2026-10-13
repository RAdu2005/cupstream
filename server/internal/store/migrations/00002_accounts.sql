-- +goose Up

-- One identity per provider per user; linking a second Discord account
-- requires unlinking the first.
CREATE UNIQUE INDEX identities_user_provider ON identities (user_id, provider);

CREATE TABLE user_settings (
    user_id      TEXT PRIMARY KEY REFERENCES users (id) ON DELETE CASCADE,
    start_mode   TEXT NOT NULL DEFAULT 'discord_auto' CHECK (start_mode IN ('discord_auto', 'link')),
    obs_fallback TEXT NOT NULL DEFAULT 'link' CHECK (obs_fallback IN ('link', 'refuse'))
) STRICT;

INSERT INTO user_settings (user_id) SELECT id FROM users;

-- Discord refresh tokens, sealed with the server's secret key.
CREATE TABLE discord_tokens (
    user_id                 TEXT PRIMARY KEY REFERENCES users (id) ON DELETE CASCADE,
    encrypted_refresh_token BLOB NOT NULL,
    updated_at              INTEGER NOT NULL,
    guilds_synced_at        INTEGER NOT NULL DEFAULT 0
) STRICT;

-- A user's Discord servers, used for friend suggestions.
CREATE TABLE user_guilds (
    user_id             TEXT NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    guild_id            TEXT NOT NULL,
    name                TEXT NOT NULL,
    icon                TEXT NOT NULL DEFAULT '',
    approx_members      INTEGER NOT NULL DEFAULT 0,
    suggestions_enabled INTEGER NOT NULL DEFAULT 1,
    PRIMARY KEY (user_id, guild_id)
) STRICT;

CREATE INDEX user_guilds_guild_id ON user_guilds (guild_id);

-- OBS stream keys. Only the hash is stored; at most one active key per user.
CREATE TABLE stream_keys (
    id         TEXT PRIMARY KEY,
    user_id    TEXT NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    key_hash   BLOB NOT NULL UNIQUE,
    created_at INTEGER NOT NULL,
    revoked_at INTEGER
) STRICT;

CREATE UNIQUE INDEX stream_keys_active ON stream_keys (user_id) WHERE revoked_at IS NULL;

-- +goose Down
DROP TABLE stream_keys;
DROP TABLE user_guilds;
DROP TABLE discord_tokens;
DROP TABLE user_settings;
DROP INDEX identities_user_provider;
