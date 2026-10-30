-- name: CreateInvite :one
INSERT INTO invites (id, token_hash, room_id, created_by, can_stream, expires_at, until_stream_end, max_uses, created_at)
VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)
RETURNING *;

-- Links whose passes still work: not revoked and not expired. Used-up links
-- are listed too, so the owner can revoke them to remove their people.
-- name: ListActiveInvites :many
SELECT * FROM invites
WHERE room_id = sqlc.arg(room_id) AND revoked_at IS NULL
  AND (expires_at IS NULL OR expires_at > CAST(sqlc.arg(now) AS INTEGER))
ORDER BY created_at DESC, id;

-- name: GetInviteByToken :one
SELECT * FROM invites WHERE token_hash = ?;

-- name: RevokeInvite :execrows
UPDATE invites SET revoked_at = sqlc.arg(now)
WHERE id = sqlc.arg(id) AND room_id = sqlc.arg(room_id) AND revoked_at IS NULL;

-- Counts one use of a link, if it still works.
-- name: UseInvite :execrows
UPDATE invites SET uses = uses + 1
WHERE id = sqlc.arg(id) AND revoked_at IS NULL
  AND (expires_at IS NULL OR expires_at > CAST(sqlc.arg(now) AS INTEGER))
  AND (max_uses IS NULL OR uses < max_uses);

-- Links that last until the stream ends, once the room has no stream left.
-- name: RevokeStreamEndInvites :many
UPDATE invites SET revoked_at = sqlc.arg(now)
WHERE room_id = sqlc.arg(room_id) AND until_stream_end = 1 AND revoked_at IS NULL
RETURNING id;

-- name: UpsertPass :exec
INSERT INTO room_passes (room_id, user_id, invite_id, can_stream, expires_at, created_at)
VALUES (?, ?, ?, ?, ?, ?)
ON CONFLICT (room_id, user_id) DO UPDATE SET
    invite_id = excluded.invite_id, can_stream = excluded.can_stream,
    expires_at = excluded.expires_at, created_at = excluded.created_at;

-- name: DeleteInvitePasses :exec
DELETE FROM room_passes WHERE invite_id = ?;
