-- +goose Up
-- Platform bans by an admin (overview 9.1). A banned user cannot sign in,
-- stream or keep a session; NULL is not banned. Additive: the previous
-- release ignores the column.
ALTER TABLE users ADD COLUMN banned_at INTEGER;

-- +goose Down
ALTER TABLE users DROP COLUMN banned_at;
