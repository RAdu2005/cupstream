-- +goose Up

-- The bot's status message in each voice channel's text chat, listing the
-- live streams of the channel's room; deleted when the room goes idle.
CREATE TABLE discord_status_messages (
    channel_id TEXT PRIMARY KEY,
    guild_id   TEXT NOT NULL,
    message_id TEXT NOT NULL,
    created_at INTEGER NOT NULL
) STRICT;

-- +goose Down
DROP TABLE discord_status_messages;
