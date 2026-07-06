-- +goose Up
CREATE TABLE IF NOT EXISTS scan_sessions (
    id INTEGER PRIMARY KEY,
    started_at INTEGER NOT NULL
);

CREATE TABLE IF NOT EXISTS scan_seen_paths (
    session_id INTEGER NOT NULL,
    path TEXT NOT NULL,
    PRIMARY KEY (session_id, path),
    FOREIGN KEY (session_id) REFERENCES scan_sessions(id) ON DELETE CASCADE
);

CREATE TABLE IF NOT EXISTS scan_protected_prefixes (
    session_id INTEGER NOT NULL,
    path TEXT NOT NULL,
    PRIMARY KEY (session_id, path),
    FOREIGN KEY (session_id) REFERENCES scan_sessions(id) ON DELETE CASCADE
);

-- +goose Down
DROP TABLE IF EXISTS scan_protected_prefixes;
DROP TABLE IF EXISTS scan_seen_paths;
DROP TABLE IF EXISTS scan_sessions;
