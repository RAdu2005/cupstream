-- +goose Up

-- Friend suggestions a user dismissed, or people whose request they
-- declined: they are not suggested to that user again.
CREATE TABLE friend_dismissals (
    user_id    TEXT NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    other_id   TEXT NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    created_at INTEGER NOT NULL,
    PRIMARY KEY (user_id, other_id)
) STRICT;

-- +goose Down
DROP TABLE friend_dismissals;
