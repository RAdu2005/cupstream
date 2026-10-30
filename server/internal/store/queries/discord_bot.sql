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

-- Discord rooms with a live stream.
-- name: ListLiveDiscordRooms :many
SELECT * FROM rooms
WHERE kind = 'discord' AND id IN (SELECT room_id FROM streams WHERE ended_at IS NULL);

-- The Discord accounts of people streaming in a Discord room.
-- name: ListLiveDiscordStreamers :many
SELECT rooms.guild_id, identities.subject AS discord_id
FROM streams
JOIN rooms ON rooms.id = streams.room_id
JOIN identities ON identities.user_id = streams.user_id AND identities.provider = 'discord'
WHERE streams.ended_at IS NULL AND rooms.kind = 'discord';

-- name: IsGuildMember :one
SELECT EXISTS (SELECT 1 FROM user_guilds WHERE user_id = ? AND guild_id = ?);

-- A pass until a moved stream ends. It replaces a pass that is no longer
-- valid, but not one that is.
-- name: CreateStreamPass :exec
INSERT INTO room_passes (room_id, user_id, stream_id, created_at)
VALUES (sqlc.arg(room_id), sqlc.arg(user_id), sqlc.arg(stream_id), sqlc.arg(now))
ON CONFLICT (room_id, user_id) DO UPDATE SET
    stream_id = excluded.stream_id, invite_id = NULL, can_stream = 0, expires_at = NULL, created_at = excluded.created_at
WHERE (room_passes.expires_at IS NOT NULL AND room_passes.expires_at <= excluded.created_at)
   OR room_passes.invite_id IN (SELECT id FROM invites WHERE revoked_at IS NOT NULL)
   OR room_passes.stream_id IN (SELECT id FROM streams WHERE ended_at IS NOT NULL);

-- name: MoveStream :exec
UPDATE streams SET room_id = ? WHERE id = ? AND ended_at IS NULL;

-- name: ListStatusMessages :many
SELECT * FROM discord_status_messages;

-- name: UpsertStatusMessage :exec
INSERT INTO discord_status_messages (channel_id, guild_id, message_id, created_at)
VALUES (?, ?, ?, ?)
ON CONFLICT (channel_id) DO UPDATE SET message_id = excluded.message_id, created_at = excluded.created_at;

-- name: DeleteStatusMessage :exec
DELETE FROM discord_status_messages WHERE channel_id = ?;
