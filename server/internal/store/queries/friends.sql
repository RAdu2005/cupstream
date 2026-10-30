-- Pairs are stored with user_a < user_b; callers order them.

-- name: GetFriendship :one
SELECT * FROM friendships WHERE user_a = ? AND user_b = ?;

-- name: CreateFriendRequest :exec
INSERT INTO friendships (user_a, user_b, status, requested_by, created_at)
VALUES (?, ?, 'pending', ?, ?);

-- name: AcceptFriendRequest :exec
UPDATE friendships SET status = 'accepted', accepted_at = ?
WHERE user_a = ? AND user_b = ? AND status = 'pending';

-- name: DeleteFriendship :exec
DELETE FROM friendships WHERE user_a = ? AND user_b = ?;

-- name: UpsertFriendship :exec
INSERT INTO friendships (user_a, user_b, status, requested_by, created_at, accepted_at)
VALUES (?, ?, ?, ?, ?, ?)
ON CONFLICT (user_a, user_b) DO UPDATE SET
    status = excluded.status,
    requested_by = excluded.requested_by,
    created_at = excluded.created_at,
    accepted_at = excluded.accepted_at;

-- name: ListUserFriendships :many
SELECT * FROM friendships WHERE user_a = sqlc.arg(user_id) OR user_b = sqlc.arg(user_id);

-- A user's friends and requests, with the other person.
-- name: ListFriendships :many
SELECT sqlc.embed(users), friendships.status, friendships.requested_by, friendships.created_at, friendships.accepted_at
FROM friendships
JOIN users ON users.id = CASE WHEN friendships.user_a = sqlc.arg(user_id) THEN friendships.user_b ELSE friendships.user_a END
WHERE friendships.user_a = sqlc.arg(user_id) OR friendships.user_b = sqlc.arg(user_id)
ORDER BY users.display_name COLLATE NOCASE, users.handle;

-- name: ListFriendIDs :many
SELECT CAST(CASE WHEN user_a = sqlc.arg(user_id) THEN user_b ELSE user_a END AS TEXT) AS friend_id
FROM friendships
WHERE (user_a = sqlc.arg(user_id) OR user_b = sqlc.arg(user_id)) AND status = 'accepted';

-- name: CountFriendships :one
SELECT
    CAST(COALESCE(SUM(status = 'accepted'), 0) AS INTEGER) AS friends,
    CAST(COALESCE(SUM(status = 'pending' AND requested_by = sqlc.arg(user_id)), 0) AS INTEGER) AS outgoing,
    CAST(COALESCE(SUM(status = 'pending' AND requested_by <> sqlc.arg(user_id)), 0) AS INTEGER) AS incoming
FROM friendships
WHERE user_a = sqlc.arg(user_id) OR user_b = sqlc.arg(user_id);

-- name: BlockUser :exec
INSERT INTO user_blocks (user_id, blocked_id, created_at) VALUES (?, ?, ?)
ON CONFLICT DO NOTHING;

-- name: UnblockUser :execrows
DELETE FROM user_blocks WHERE user_id = ? AND blocked_id = ?;

-- name: IsBlocked :one
SELECT EXISTS (SELECT 1 FROM user_blocks WHERE user_id = ? AND blocked_id = ?);

-- name: ListBlocked :many
SELECT users.*
FROM user_blocks
JOIN users ON users.id = user_blocks.blocked_id
WHERE user_blocks.user_id = ?
ORDER BY users.handle;

-- Merging accounts: the absorbed user's blocks, in both directions.
-- name: MoveBlocksBy :exec
INSERT OR IGNORE INTO user_blocks (user_id, blocked_id, created_at)
SELECT sqlc.arg(to_user), blocked_id, created_at FROM user_blocks
WHERE user_blocks.user_id = sqlc.arg(from_user) AND blocked_id <> sqlc.arg(to_user);

-- name: MoveBlocksOf :exec
INSERT OR IGNORE INTO user_blocks (user_id, blocked_id, created_at)
SELECT user_id, sqlc.arg(to_user), created_at FROM user_blocks
WHERE user_blocks.blocked_id = sqlc.arg(from_user) AND user_id <> sqlc.arg(to_user);

-- Friendships of user_id with someone either of them blocked.
-- name: DeleteBlockedFriendships :exec
DELETE FROM friendships
WHERE (user_a = sqlc.arg(user_id) OR user_b = sqlc.arg(user_id))
  AND EXISTS (
      SELECT 1 FROM user_blocks
      WHERE (user_blocks.user_id = friendships.user_a AND user_blocks.blocked_id = friendships.user_b)
         OR (user_blocks.user_id = friendships.user_b AND user_blocks.blocked_id = friendships.user_a)
  );
