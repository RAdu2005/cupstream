-- name: UpsertDiscordToken :exec
INSERT INTO discord_tokens (user_id, encrypted_refresh_token, updated_at)
VALUES (?, ?, ?)
ON CONFLICT (user_id) DO UPDATE SET
    encrypted_refresh_token = excluded.encrypted_refresh_token,
    updated_at = excluded.updated_at;

-- name: GetDiscordToken :one
SELECT * FROM discord_tokens WHERE user_id = ?;

-- name: MarkGuildsSynced :exec
UPDATE discord_tokens SET guilds_synced_at = ? WHERE user_id = ?;

-- name: DeleteDiscordToken :exec
DELETE FROM discord_tokens WHERE user_id = ?;

-- Users whose server list is older than sync_before and who were active
-- since active_since.
-- name: ListGuildSyncDue :many
SELECT discord_tokens.user_id
FROM discord_tokens
WHERE discord_tokens.guilds_synced_at < sqlc.arg(sync_before)
  AND EXISTS (
      SELECT 1 FROM sessions
      WHERE sessions.user_id = discord_tokens.user_id AND sessions.last_seen_at >= sqlc.arg(active_since)
  )
LIMIT 100;

-- name: UpsertUserGuild :exec
INSERT INTO user_guilds (user_id, guild_id, name, icon, approx_members, suggestions_enabled)
VALUES (?, ?, ?, ?, ?, ?)
ON CONFLICT (user_id, guild_id) DO UPDATE SET
    name = excluded.name,
    icon = excluded.icon,
    approx_members = excluded.approx_members;

-- name: ListUserGuilds :many
SELECT * FROM user_guilds WHERE user_id = ? ORDER BY name COLLATE NOCASE;

-- name: DeleteUserGuild :exec
DELETE FROM user_guilds WHERE user_id = ? AND guild_id = ?;

-- name: DeleteUserGuilds :exec
DELETE FROM user_guilds WHERE user_id = ?;

-- name: SetGuildSuggestions :execrows
UPDATE user_guilds SET suggestions_enabled = ? WHERE user_id = ? AND guild_id = ?;
