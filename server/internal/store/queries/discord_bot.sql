-- name: IsDiscordLinked :one
SELECT EXISTS (SELECT 1 FROM identities WHERE provider = 'discord' AND subject = ?);

-- name: GetDiscordIdentity :one
SELECT * FROM identities WHERE user_id = ? AND provider = 'discord';

-- name: EnsureDiscordGuild :exec
INSERT INTO discord_guilds (guild_id, created_at) VALUES (?, ?) ON CONFLICT DO NOTHING;

-- name: ListDiscordGuilds :many
SELECT * FROM discord_guilds;

-- name: GetDiscordGuild :one
SELECT * FROM discord_guilds WHERE guild_id = ?;

-- name: UpdateDiscordGuild :exec
UPDATE discord_guilds SET stream_policy = ?, announce = ?, tracked_channels = ? WHERE guild_id = ?;

-- name: CreateDiscordRoom :exec
INSERT INTO rooms (id, kind, slug, guild_id, channel_id, created_at)
VALUES (?, 'discord', ?, ?, ?, ?)
ON CONFLICT (guild_id, channel_id) WHERE kind = 'discord' DO NOTHING;

-- name: GetDiscordRoom :one
SELECT * FROM rooms WHERE kind = 'discord' AND guild_id = ? AND channel_id = ?;

-- name: ListDiscordRooms :many
SELECT * FROM rooms WHERE kind = 'discord' AND guild_id = ?;
