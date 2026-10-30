-- name: InsertBan :exec
INSERT OR IGNORE INTO room_bans (room_id, user_id, created_at) VALUES (?, ?, ?);

-- name: DeleteBan :execrows
DELETE FROM room_bans WHERE room_id = ? AND user_id = ?;

-- name: ListBans :many
SELECT sqlc.embed(users), room_bans.created_at AS banned_at
FROM room_bans
JOIN users ON users.id = room_bans.user_id
WHERE room_bans.room_id = ?
ORDER BY users.handle;

-- name: DeletePass :exec
DELETE FROM room_passes WHERE room_id = ? AND user_id = ?;
