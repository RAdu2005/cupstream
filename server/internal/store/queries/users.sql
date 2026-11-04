-- name: GetUser :one
SELECT * FROM users WHERE id = ?;

-- name: GetUserByHandle :one
SELECT * FROM users WHERE handle = ?;

-- name: HandleTaken :one
-- Whether handle belongs to someone other than user_id ('' for a new user):
-- their handle, a room's slug, or a handle they gave up since `since`, which
-- stays reserved to them.
SELECT EXISTS (
    SELECT 1 FROM users WHERE users.handle = sqlc.arg(handle) AND users.id != sqlc.arg(user_id)
    UNION ALL
    SELECT 1 FROM rooms
    WHERE rooms.slug = sqlc.arg(handle) AND (rooms.owner_id IS NULL OR rooms.owner_id != sqlc.arg(user_id))
    UNION ALL
    SELECT 1 FROM handle_changes
    WHERE handle_changes.old_handle = sqlc.arg(handle) AND handle_changes.user_id != sqlc.arg(user_id)
      AND handle_changes.changed_at > sqlc.arg(since) AND handle_changes.undone_at IS NULL
);

-- name: LastHandleChange :one
-- The user's latest handle change that was not undone.
SELECT * FROM handle_changes
WHERE user_id = ? AND undone_at IS NULL
ORDER BY changed_at DESC, id DESC
LIMIT 1;

-- name: CreateHandleChange :exec
INSERT INTO handle_changes (user_id, old_handle, new_handle, changed_at)
VALUES (?, ?, ?, ?);

-- name: UndoHandleChange :exec
UPDATE handle_changes SET undone_at = ? WHERE id = ?;

-- name: GetUserByIdentity :one
SELECT users.*
FROM identities
JOIN users ON users.id = identities.user_id
WHERE identities.provider = ? AND identities.subject = ?;

-- name: CreateUser :one
INSERT INTO users (id, handle, display_name, avatar_url, created_at)
VALUES (?, ?, ?, ?, ?)
RETURNING *;

-- name: UpdateProfile :one
UPDATE users SET handle = ?, display_name = ? WHERE id = ?
RETURNING *;

-- name: SetAvatarIfEmpty :exec
UPDATE users SET avatar_url = ? WHERE id = ? AND (avatar_url IS NULL OR avatar_url = '');

-- name: DeleteUser :exec
DELETE FROM users WHERE id = ?;

-- name: CreateIdentity :exec
INSERT INTO identities (provider, subject, user_id, username, created_at)
VALUES (?, ?, ?, ?, ?);

-- name: GetIdentity :one
SELECT * FROM identities WHERE provider = ? AND subject = ?;

-- name: ListIdentities :many
SELECT * FROM identities WHERE user_id = ? ORDER BY provider;

-- name: UpdateIdentityUsername :exec
UPDATE identities SET username = ? WHERE provider = ? AND subject = ?;

-- name: DeleteIdentity :execrows
DELETE FROM identities WHERE user_id = ? AND provider = ?;

-- name: MoveIdentity :exec
UPDATE identities SET user_id = sqlc.arg(to_user) WHERE provider = sqlc.arg(provider) AND subject = sqlc.arg(subject);

-- name: CreateUserSettings :exec
INSERT INTO user_settings (user_id) VALUES (?) ON CONFLICT DO NOTHING;

-- name: GetUserSettings :one
SELECT * FROM user_settings WHERE user_id = ?;

-- name: UpdateUserSettings :exec
UPDATE user_settings SET start_mode = ?, obs_fallback = ? WHERE user_id = ?;

-- name: SetUserBanned :exec
UPDATE users SET banned_at = ? WHERE id = ?;

-- name: ListBannedUsers :many
SELECT * FROM users WHERE banned_at IS NOT NULL ORDER BY banned_at DESC;
