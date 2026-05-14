-- +goose Up
DROP TRIGGER IF EXISTS entries_ai;
DROP TRIGGER IF EXISTS entries_ad;
DROP TRIGGER IF EXISTS entries_au;
DROP TABLE IF EXISTS entries_fts;

CREATE VIRTUAL TABLE IF NOT EXISTS entries_fts USING fts5(
    name,
    content='entries',
    content_rowid='id',
    tokenize='unicode61 remove_diacritics 2',
    prefix='3'
);

INSERT INTO entries_fts(rowid, name)
SELECT id, name FROM entries;

-- +goose StatementBegin
CREATE TRIGGER IF NOT EXISTS entries_ai AFTER INSERT ON entries BEGIN
    INSERT INTO entries_fts(rowid, name) VALUES (new.id, new.name);
END;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE TRIGGER IF NOT EXISTS entries_ad AFTER DELETE ON entries BEGIN
    INSERT INTO entries_fts(entries_fts, rowid, name) VALUES ('delete', old.id, old.name);
END;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE TRIGGER IF NOT EXISTS entries_au AFTER UPDATE ON entries BEGIN
    INSERT INTO entries_fts(entries_fts, rowid, name) VALUES ('delete', old.id, old.name);
    INSERT INTO entries_fts(rowid, name) VALUES (new.id, new.name);
END;
-- +goose StatementEnd

-- +goose Down
DROP TRIGGER IF EXISTS entries_ai;
DROP TRIGGER IF EXISTS entries_ad;
DROP TRIGGER IF EXISTS entries_au;
DROP TABLE IF EXISTS entries_fts;
