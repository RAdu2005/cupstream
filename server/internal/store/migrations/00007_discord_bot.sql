-- +goose Up

-- Settings of each Discord server the bot was added to. Who is in voice and
-- what Discord allows them is never stored: the bot keeps it in memory.
CREATE TABLE discord_guilds (
    guild_id         TEXT PRIMARY KEY,
    -- Who may stream in the server's rooms.
    stream_policy    TEXT NOT NULL DEFAULT 'video_permission' CHECK (stream_policy IN ('video_permission', 'anyone_in_channel')),
    -- Whether the bot posts status messages in voice channels' text chat.
    announce         INTEGER NOT NULL DEFAULT 1,
    -- JSON array of the voice channel IDs that get rooms; NULL: all of them.
    tracked_channels TEXT,
    created_at       INTEGER NOT NULL
) STRICT;

-- +goose Down
DROP TABLE discord_guilds;
