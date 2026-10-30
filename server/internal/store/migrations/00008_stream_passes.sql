-- +goose Up

-- A pass from a stream moved into a link room lasts until that stream ends.
ALTER TABLE room_passes ADD COLUMN stream_id TEXT REFERENCES streams (id) ON DELETE CASCADE;

-- +goose Down
ALTER TABLE room_passes DROP COLUMN stream_id;
