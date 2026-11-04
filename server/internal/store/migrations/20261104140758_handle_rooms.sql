-- +goose Up
-- A user's handle is the slug of their link room (/r/<handle>). Handle
-- changes are kept for the renaming rules: one change a week, and the
-- handle given up stays reserved to its owner for a day, who may switch
-- back to it (undone_at) without using up the week's change. Additive: the
-- previous release keeps working with handles as slugs.
CREATE TABLE handle_changes (
    id         INTEGER PRIMARY KEY,
    user_id    TEXT NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    old_handle TEXT NOT NULL COLLATE NOCASE,
    new_handle TEXT NOT NULL COLLATE NOCASE,
    changed_at INTEGER NOT NULL,
    undone_at  INTEGER
) STRICT;

CREATE INDEX handle_changes_user ON handle_changes (user_id, changed_at);
CREATE INDEX handle_changes_old_handle ON handle_changes (old_handle, changed_at);

-- Link rooms move from their random slug to their owner's handle, unless
-- another room already has that slug (its owner keeps the random one until
-- they change their handle).
UPDATE rooms
SET slug = (SELECT handle FROM users WHERE users.id = rooms.owner_id)
WHERE kind = 'link' AND NOT EXISTS (
    SELECT 1 FROM rooms AS other
    WHERE other.slug = (SELECT handle FROM users WHERE users.id = rooms.owner_id)
);

-- +goose Down
DROP TABLE handle_changes;
