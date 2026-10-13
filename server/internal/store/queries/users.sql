-- name: GetUser :one
SELECT * FROM users WHERE id = ?;

-- name: GetUserByIdentity :one
SELECT users.*
FROM identities
JOIN users ON users.id = identities.user_id
WHERE identities.provider = ? AND identities.subject = ?;

-- name: CreateUser :one
INSERT INTO users (id, handle, display_name, avatar_url, created_at)
VALUES (?, ?, ?, ?, ?)
RETURNING *;

-- name: CreateIdentity :exec
INSERT INTO identities (provider, subject, user_id, username, created_at)
VALUES (?, ?, ?, ?, ?);
