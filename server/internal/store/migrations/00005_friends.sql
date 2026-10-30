-- +goose Up

-- Friend requests and friendships. Each pair is stored once, the lower user
-- ID first; requested_by is whoever asked.
CREATE TABLE friendships (
    user_a       TEXT NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    user_b       TEXT NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    status       TEXT NOT NULL CHECK (status IN ('pending', 'accepted')),
    requested_by TEXT NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    created_at   INTEGER NOT NULL,
    accepted_at  INTEGER,
    PRIMARY KEY (user_a, user_b),
    CHECK (user_a < user_b),
    CHECK (requested_by IN (user_a, user_b))
) STRICT;

CREATE INDEX friendships_user_b ON friendships (user_b);

-- People a user blocked: they cannot send that user friend requests or find
-- them by handle, and neither is suggested to the other.
CREATE TABLE user_blocks (
    user_id    TEXT NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    blocked_id TEXT NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    created_at INTEGER NOT NULL,
    PRIMARY KEY (user_id, blocked_id),
    CHECK (user_id <> blocked_id)
) STRICT;

CREATE INDEX user_blocks_blocked_id ON user_blocks (blocked_id);

-- +goose Down
DROP TABLE user_blocks;
DROP TABLE friendships;
