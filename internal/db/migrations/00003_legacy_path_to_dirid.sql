-- +goose Up
-- Legacy `entries(path)` to normalized `directories + dir_id` conversion is handled
-- by the pre-migration compatibility step in Go before the migration runner executes.
SELECT 1;

-- +goose Down
SELECT 1;
