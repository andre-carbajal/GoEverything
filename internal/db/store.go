package db

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"time"

	_ "modernc.org/sqlite"
)

type Entry struct {
	Name    string
	Path    string
	Ext     string
	Size    int64
	MTime   int64
	IsDir   bool
	Root    string
	Indexed time.Time
}

type SearchOptions struct {
	Query     string
	Limit     int
	Offset    int
	OnlyDirs  bool
	OnlyFiles bool
	Ext       string
	Root      string
}

type Store struct {
	db *sql.DB
}

func Open(ctx context.Context, dbPath string) (*Store, error) {
	if dbPath == "" {
		return nil, errors.New("db path is required")
	}

	dsn := fmt.Sprintf("file:%s?_pragma=busy_timeout(5000)", dbPath)
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, err
	}

	store := &Store{db: db}
	if err := store.setup(ctx); err != nil {
		_ = db.Close()
		return nil, err
	}

	return store, nil
}

func (s *Store) Close() error { return s.db.Close() }

func (s *Store) setup(ctx context.Context) error {
	pragmas := []string{
		"PRAGMA journal_mode=WAL;",
		"PRAGMA synchronous=NORMAL;",
		"PRAGMA temp_store=MEMORY;",
		"PRAGMA foreign_keys=ON;",
	}
	for _, stmt := range pragmas {
		if _, err := s.db.ExecContext(ctx, stmt); err != nil {
			return err
		}
	}

	schema := []string{
		`CREATE TABLE IF NOT EXISTS entries (
			id INTEGER PRIMARY KEY,
			name TEXT NOT NULL,
			path TEXT NOT NULL UNIQUE,
			ext TEXT NOT NULL,
			size INTEGER NOT NULL,
			mtime INTEGER NOT NULL,
			is_dir INTEGER NOT NULL,
			root TEXT NOT NULL,
			indexed_at INTEGER NOT NULL
		);`,
		`CREATE INDEX IF NOT EXISTS idx_entries_root ON entries(root);`,
		`CREATE INDEX IF NOT EXISTS idx_entries_name ON entries(name);`,
		`CREATE INDEX IF NOT EXISTS idx_entries_ext ON entries(ext);`,
		`CREATE VIRTUAL TABLE IF NOT EXISTS entries_fts USING fts5(
			name,
			path,
			content='entries',
			content_rowid='id',
			tokenize='unicode61 remove_diacritics 2'
		);`,
		`CREATE TRIGGER IF NOT EXISTS entries_ai AFTER INSERT ON entries BEGIN
			INSERT INTO entries_fts(rowid, name, path) VALUES (new.id, new.name, new.path);
		END;`,
		`CREATE TRIGGER IF NOT EXISTS entries_ad AFTER DELETE ON entries BEGIN
			INSERT INTO entries_fts(entries_fts, rowid, name, path) VALUES ('delete', old.id, old.name, old.path);
		END;`,
		`CREATE TRIGGER IF NOT EXISTS entries_au AFTER UPDATE ON entries BEGIN
			INSERT INTO entries_fts(entries_fts, rowid, name, path) VALUES ('delete', old.id, old.name, old.path);
			INSERT INTO entries_fts(rowid, name, path) VALUES (new.id, new.name, new.path);
		END;`,
	}

	for _, stmt := range schema {
		if _, err := s.db.ExecContext(ctx, stmt); err != nil {
			return err
		}
	}
	return nil
}

func (s *Store) UpsertBatch(ctx context.Context, entries []Entry) error {
	if len(entries) == 0 {
		return nil
	}

	var err error
	for attempt := 0; attempt < 6; attempt++ {
		err = s.upsertBatchOnce(ctx, entries)
		if err == nil {
			return nil
		}
		if !isBusyError(err) {
			return err
		}

		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(time.Duration(20*(1<<attempt)) * time.Millisecond):
		}
	}
	return err
}

func (s *Store) upsertBatchOnce(ctx context.Context, entries []Entry) error {
	var err error

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() {
		if err != nil {
			_ = tx.Rollback()
		}
	}()

	stmt, err := tx.PrepareContext(ctx, `
		INSERT INTO entries(name, path, ext, size, mtime, is_dir, root, indexed_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(path) DO UPDATE SET
			name=excluded.name,
			ext=excluded.ext,
			size=excluded.size,
			mtime=excluded.mtime,
			is_dir=excluded.is_dir,
			root=excluded.root,
			indexed_at=excluded.indexed_at
	`)
	if err != nil {
		return err
	}
	defer stmt.Close()

	now := time.Now().Unix()
	for _, entry := range entries {
		_, err = stmt.ExecContext(ctx, entry.Name, entry.Path, entry.Ext, entry.Size, entry.MTime, boolToInt(entry.IsDir), entry.Root, now)
		if err != nil {
			return err
		}
	}

	err = tx.Commit()
	return err
}

func isBusyError(err error) bool {
	if err == nil {
		return false
	}
	msg := strings.ToLower(err.Error())
	return strings.Contains(msg, "database is locked") || strings.Contains(msg, "sqlite_busy")
}

func (s *Store) DeleteByPath(ctx context.Context, path string) error {
	_, err := s.db.ExecContext(ctx, `DELETE FROM entries WHERE path = ?`, path)
	return err
}

func (s *Store) DeleteByPrefix(ctx context.Context, prefix string) error {
	if prefix == "" {
		return nil
	}
	clean := filepath.Clean(prefix)
	_, err := s.db.ExecContext(ctx, `DELETE FROM entries WHERE path = ? OR path LIKE ?`, clean, clean+"/%")
	return err
}

func (s *Store) Search(ctx context.Context, query string, limit, offset int) ([]Entry, error) {
	return s.SearchAdvanced(ctx, SearchOptions{Query: query, Limit: limit, Offset: offset})
}

func (s *Store) SearchAdvanced(ctx context.Context, opts SearchOptions) ([]Entry, error) {
	query := strings.TrimSpace(opts.Query)
	if query == "" {
		return nil, nil
	}
	if opts.Limit <= 0 {
		opts.Limit = 50
	}
	if opts.Offset < 0 {
		opts.Offset = 0
	}

	if strings.HasPrefix(query, "*") || strings.HasSuffix(query, "*") {
		return s.searchByLike(ctx, query, opts)
	}

	q := `
		SELECT e.name, e.path, e.ext, e.size, e.mtime, e.is_dir, e.root, e.indexed_at
		FROM entries_fts f
		JOIN entries e ON e.id = f.rowid
		WHERE entries_fts MATCH ?`
	args := []any{buildFTSQuery(query)}
	q, args = applySearchFilters(q, args, opts)
	q += `
		ORDER BY
			CASE
				WHEN e.name = ? THEN 0
				WHEN e.name LIKE ? THEN 1
				ELSE 2
			END,
			e.name ASC
		LIMIT ? OFFSET ?`
	args = append(args, query, query+"%", opts.Limit, opts.Offset)

	rows, err := s.db.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	return scanEntries(rows, opts.Limit)
}

func (s *Store) searchByLike(ctx context.Context, query string, opts SearchOptions) ([]Entry, error) {
	like := strings.ReplaceAll(query, "*", "%")
	if !strings.Contains(like, "%") {
		like = "%" + like + "%"
	}

	q := `
		SELECT e.name, e.path, e.ext, e.size, e.mtime, e.is_dir, e.root, e.indexed_at
		FROM entries e
		WHERE (e.name LIKE ? OR e.path LIKE ?)`
	args := []any{like, like}
	q, args = applySearchFilters(q, args, opts)
	q += ` ORDER BY e.name ASC LIMIT ? OFFSET ?`
	args = append(args, opts.Limit, opts.Offset)

	rows, err := s.db.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	return scanEntries(rows, opts.Limit)
}

func applySearchFilters(query string, args []any, opts SearchOptions) (string, []any) {
	if opts.OnlyDirs && !opts.OnlyFiles {
		query += ` AND e.is_dir = 1`
	}
	if opts.OnlyFiles && !opts.OnlyDirs {
		query += ` AND e.is_dir = 0`
	}
	if ext := strings.TrimPrefix(strings.ToLower(strings.TrimSpace(opts.Ext)), "."); ext != "" {
		query += ` AND e.ext = ?`
		args = append(args, ext)
	}
	if root := strings.TrimSpace(opts.Root); root != "" {
		query += ` AND e.root = ?`
		args = append(args, filepath.Clean(root))
	}
	return query, args
}

func scanEntries(rows *sql.Rows, capacity int) ([]Entry, error) {
	out := make([]Entry, 0, capacity)
	for rows.Next() {
		var (
			entry     Entry
			isDirInt  int
			indexedAt int64
		)
		if err := rows.Scan(&entry.Name, &entry.Path, &entry.Ext, &entry.Size, &entry.MTime, &isDirInt, &entry.Root, &indexedAt); err != nil {
			return nil, err
		}
		entry.IsDir = isDirInt == 1
		entry.Indexed = time.Unix(indexedAt, 0)
		out = append(out, entry)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return out, nil
}

func NewEntryFromPath(root, path string, size int64, mtime time.Time, isDir bool) Entry {
	base := filepath.Base(path)
	ext := ""
	if !isDir {
		ext = strings.ToLower(strings.TrimPrefix(filepath.Ext(base), "."))
	}
	return Entry{Name: base, Path: path, Ext: ext, Size: size, MTime: mtime.Unix(), IsDir: isDir, Root: root}
}

func buildFTSQuery(query string) string {
	parts := strings.Fields(strings.TrimSpace(query))
	if len(parts) == 0 {
		return ""
	}
	chunks := make([]string, 0, len(parts))
	for _, p := range parts {
		escaped := strings.ReplaceAll(p, `"`, "")
		chunks = append(chunks, fmt.Sprintf(`%q*`, escaped))
	}
	return strings.Join(chunks, " ")
}

func boolToInt(v bool) int {
	if v {
		return 1
	}
	return 0
}
