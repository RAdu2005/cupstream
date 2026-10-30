-- +goose Up

-- Room chat. Messages are deleted 24 hours after they were sent.
CREATE TABLE chat_messages (
    id         TEXT PRIMARY KEY,       -- UUIDv7, so IDs sort by time
    room_id    TEXT NOT NULL REFERENCES rooms (id) ON DELETE CASCADE,
    user_id    TEXT NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    body       TEXT NOT NULL,
    created_at INTEGER NOT NULL
) STRICT;

CREATE INDEX chat_messages_room ON chat_messages (room_id, created_at);
CREATE INDEX chat_messages_created_at ON chat_messages (created_at);
CREATE INDEX chat_messages_user_id ON chat_messages (user_id);

-- +goose Down
DROP TABLE chat_messages;
