-- name: CreateStream :one
INSERT INTO streams (id, room_id, user_id, source, publish_token_hash, started_at)
VALUES (?, ?, ?, ?, ?, ?)
RETURNING *;

-- name: GetStream :one
SELECT * FROM streams WHERE id = ?;

-- name: ListActiveStreams :many
SELECT * FROM streams WHERE ended_at IS NULL;

-- name: EndStream :exec
UPDATE streams
SET ended_at = sqlc.arg(ended_at), end_reason = sqlc.arg(end_reason), peak_viewers = max(peak_viewers, CAST(sqlc.arg(peak) AS INTEGER))
WHERE id = sqlc.arg(id) AND ended_at IS NULL;

-- name: UpdatePeakViewers :exec
UPDATE streams SET peak_viewers = sqlc.arg(peak) WHERE id = sqlc.arg(id) AND peak_viewers < sqlc.arg(peak);

-- name: GetActiveStreamKey :one
SELECT * FROM stream_keys WHERE user_id = ? AND revoked_at IS NULL;

-- name: GetUserByStreamKey :one
SELECT users.*
FROM stream_keys
JOIN users ON users.id = stream_keys.user_id
WHERE stream_keys.key_hash = ? AND stream_keys.revoked_at IS NULL;

-- name: RevokeStreamKeys :exec
UPDATE stream_keys SET revoked_at = ? WHERE user_id = ? AND revoked_at IS NULL;

-- name: CreateStreamKey :exec
INSERT INTO stream_keys (id, user_id, key_hash, created_at) VALUES (?, ?, ?, ?);
