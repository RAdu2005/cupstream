-- Link rooms whose allowed list has the user.
-- name: ListAllowedRooms :many
SELECT sqlc.embed(rooms)
FROM room_allowed
JOIN rooms ON rooms.id = room_allowed.room_id
WHERE room_allowed.user_id = ?;

-- Rooms where the user holds a valid pass (an invite link, a moved stream).
-- name: ListPassRooms :many
SELECT sqlc.embed(rooms)
FROM room_passes
JOIN rooms ON rooms.id = room_passes.room_id
LEFT JOIN invites ON invites.id = room_passes.invite_id
WHERE room_passes.user_id = sqlc.arg(user_id)
  AND (room_passes.expires_at IS NULL OR room_passes.expires_at > CAST(sqlc.arg(now) AS INTEGER))
  AND (invites.id IS NULL OR invites.revoked_at IS NULL);
