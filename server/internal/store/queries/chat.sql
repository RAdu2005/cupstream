-- name: CreateChatMessage :exec
INSERT INTO chat_messages (id, room_id, user_id, body, created_at) VALUES (?, ?, ?, ?, ?);

-- The latest messages of a room sent after a time, newest first.
-- name: ListChatMessages :many
SELECT sqlc.embed(chat_messages), sqlc.embed(users)
FROM chat_messages
JOIN users ON users.id = chat_messages.user_id
WHERE chat_messages.room_id = sqlc.arg(room_id) AND chat_messages.created_at > sqlc.arg(since)
ORDER BY chat_messages.created_at DESC, chat_messages.id DESC
LIMIT sqlc.arg(max);

-- name: GetChatMessage :one
SELECT * FROM chat_messages WHERE id = ?;

-- name: DeleteChatMessage :execrows
DELETE FROM chat_messages WHERE id = ? AND room_id = ?;

-- name: DeleteChatMessagesBefore :execrows
DELETE FROM chat_messages WHERE created_at <= ?;
