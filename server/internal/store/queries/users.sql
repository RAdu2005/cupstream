-- name: GetUser :one
SELECT * FROM users WHERE id = ?;

-- name: GetUserByHandle :one
SELECT * FROM users WHERE handle = ?;

-- name: HandleTaken :one
SELECT EXISTS (SELECT 1 FROM users WHERE handle = ?);

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
