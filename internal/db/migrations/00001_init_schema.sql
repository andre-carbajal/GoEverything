-- +goose Up
CREATE TABLE IF NOT EXISTS directories (
    id INTEGER PRIMARY KEY,
    path TEXT NOT NULL UNIQUE
);

CREATE TABLE IF NOT EXISTS entries (
    id INTEGER PRIMARY KEY,
    name TEXT NOT NULL,
    dir_id INTEGER NOT NULL,
    ext TEXT NOT NULL,
    size INTEGER NOT NULL,
    mtime INTEGER NOT NULL,
    is_dir INTEGER NOT NULL,
    root TEXT NOT NULL,
    indexed_at INTEGER NOT NULL,
    UNIQUE(dir_id, name),
    FOREIGN KEY(dir_id) REFERENCES directories(id) ON DELETE CASCADE
);

CREATE INDEX IF NOT EXISTS idx_entries_root ON entries(root);
CREATE INDEX IF NOT EXISTS idx_entries_name ON entries(name);
CREATE INDEX IF NOT EXISTS idx_entries_ext ON entries(ext);
CREATE INDEX IF NOT EXISTS idx_entries_dir_id ON entries(dir_id);

-- +goose Down
DROP TABLE IF EXISTS entries;
DROP TABLE IF EXISTS directories;
