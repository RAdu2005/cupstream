-- name: CreateLinkRoom :one
INSERT INTO rooms (id, kind, slug, owner_id, visibility, created_at)
VALUES (?, 'link', ?, ?, 'invite_only', ?)
RETURNING *;

-- name: GetRoom :one
SELECT * FROM rooms WHERE id = ?;

-- name: GetRoomBySlug :one
SELECT * FROM rooms WHERE slug = ?;

-- name: GetLinkRoomByOwner :one
SELECT * FROM rooms WHERE kind = 'link' AND owner_id = ?;

-- name: SetRoomVisibility :exec
UPDATE rooms SET visibility = ? WHERE id = ? AND kind = 'link';

-- name: GetAllowed :one
SELECT * FROM room_allowed WHERE room_id = ? AND user_id = ?;

-- name: ListAllowed :many
SELECT sqlc.embed(users), room_allowed.can_stream
FROM room_allowed
JOIN users ON users.id = room_allowed.user_id
WHERE room_allowed.room_id = ?
ORDER BY users.handle;

-- name: UpsertAllowed :exec
INSERT INTO room_allowed (room_id, user_id, can_stream, created_at)
VALUES (?, ?, ?, ?)
ON CONFLICT (room_id, user_id) DO UPDATE SET can_stream = excluded.can_stream;

-- name: DeleteAllowed :execrows
DELETE FROM room_allowed WHERE room_id = ? AND user_id = ?;

-- Valid passes only: not expired and not from a revoked invite.
-- name: GetActivePass :one
SELECT room_passes.*
FROM room_passes
LEFT JOIN invites ON invites.id = room_passes.invite_id
WHERE room_passes.room_id = sqlc.arg(room_id) AND room_passes.user_id = sqlc.arg(user_id)
  AND (room_passes.expires_at IS NULL OR room_passes.expires_at > CAST(sqlc.arg(now) AS INTEGER))
  AND (invites.id IS NULL OR invites.revoked_at IS NULL);

-- name: IsBanned :one
SELECT EXISTS (SELECT 1 FROM room_bans WHERE room_id = ? AND user_id = ?);

-- Merging accounts: carry the absorbed user's room memberships over.
-- name: MoveAllowed :exec
INSERT OR IGNORE INTO room_allowed (room_id, user_id, can_stream, created_at)
SELECT room_id, sqlc.arg(to_user), can_stream, created_at FROM room_allowed WHERE room_allowed.user_id = sqlc.arg(from_user);
