-- +goose Up

-- Link rooms belong to a user (exactly one each); Discord rooms belong to a
-- voice channel. Columns of the other kind are NULL.
CREATE TABLE rooms (
    id         TEXT PRIMARY KEY,
    kind       TEXT NOT NULL CHECK (kind IN ('link', 'discord')),
    slug       TEXT NOT NULL UNIQUE,
    owner_id   TEXT REFERENCES users (id) ON DELETE CASCADE,
    visibility TEXT CHECK (visibility IN ('everyone', 'signed_in', 'invite_only')),
    guild_id   TEXT,
    channel_id TEXT,
    created_at INTEGER NOT NULL,
    CHECK (
        (kind = 'link' AND owner_id IS NOT NULL AND visibility IS NOT NULL AND guild_id IS NULL AND channel_id IS NULL)
        OR (kind = 'discord' AND owner_id IS NULL AND visibility IS NULL AND guild_id IS NOT NULL AND channel_id IS NOT NULL)
    )
) STRICT;

CREATE UNIQUE INDEX rooms_link_owner ON rooms (owner_id) WHERE kind = 'link';
CREATE UNIQUE INDEX rooms_discord_channel ON rooms (guild_id, channel_id) WHERE kind = 'discord';

-- People the owner allowed into a link room.
CREATE TABLE room_allowed (
    room_id    TEXT NOT NULL REFERENCES rooms (id) ON DELETE CASCADE,
    user_id    TEXT NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    can_stream INTEGER NOT NULL DEFAULT 0,
    created_at INTEGER NOT NULL,
    PRIMARY KEY (room_id, user_id)
) STRICT;

CREATE INDEX room_allowed_user_id ON room_allowed (user_id);

CREATE TABLE invites (
    id               TEXT PRIMARY KEY,
    token_hash       BLOB NOT NULL UNIQUE,
    room_id          TEXT NOT NULL REFERENCES rooms (id) ON DELETE CASCADE,
    created_by       TEXT NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    can_stream       INTEGER NOT NULL DEFAULT 0,
    expires_at       INTEGER,          -- NULL: never, or until the stream ends
    until_stream_end INTEGER NOT NULL DEFAULT 0,
    max_uses         INTEGER,
    uses             INTEGER NOT NULL DEFAULT 0,
    created_at       INTEGER NOT NULL,
    revoked_at       INTEGER
) STRICT;

-- Temporary access from invite links and moved streams.
CREATE TABLE room_passes (
    room_id    TEXT NOT NULL REFERENCES rooms (id) ON DELETE CASCADE,
    user_id    TEXT NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    invite_id  TEXT REFERENCES invites (id) ON DELETE CASCADE,
    can_stream INTEGER NOT NULL DEFAULT 0,
    expires_at INTEGER,                -- NULL: until revoked
    created_at INTEGER NOT NULL,
    PRIMARY KEY (room_id, user_id)
) STRICT;

CREATE TABLE room_bans (
    room_id    TEXT NOT NULL REFERENCES rooms (id) ON DELETE CASCADE,
    user_id    TEXT NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    created_at INTEGER NOT NULL,
    PRIMARY KEY (room_id, user_id)
) STRICT;

-- Stream history. A stream with ended_at NULL is live or reconnecting.
CREATE TABLE streams (
    id                 TEXT PRIMARY KEY,
    room_id            TEXT NOT NULL REFERENCES rooms (id) ON DELETE CASCADE,
    user_id            TEXT NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    source             TEXT NOT NULL CHECK (source IN ('app', 'web', 'obs')),
    publish_token_hash BLOB,           -- app and web senders; OBS uses its stream key
    started_at         INTEGER NOT NULL,
    ended_at           INTEGER,
    end_reason         TEXT,
    peak_viewers       INTEGER NOT NULL DEFAULT 0
) STRICT;

CREATE INDEX streams_active ON streams (user_id) WHERE ended_at IS NULL;
CREATE INDEX streams_room ON streams (room_id, started_at);

-- Every existing user gets a link room.
INSERT INTO rooms (id, kind, slug, owner_id, visibility, created_at)
SELECT lower(hex(randomblob(16))), 'link', lower(hex(randomblob(5))), id, 'invite_only', unixepoch()
FROM users;

-- +goose Down
DROP TABLE streams;
DROP TABLE room_bans;
DROP TABLE room_passes;
DROP TABLE invites;
DROP TABLE room_allowed;
DROP TABLE rooms;
