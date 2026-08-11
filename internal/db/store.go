package db

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"time"

	// Register the SQLite driver used by database/sql.
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

const (
	maxTransactionAttempts       = 6
	transactionRetryDelay        = 20 * time.Millisecond
	recalculateDirectorySizesSQL = `
		WITH indexed_entries AS (
			SELECT
				e.id,
				CASE
					WHEN substr(d.path, length(d.path), 1) = ? THEN d.path || e.name
					ELSE d.path || ? || e.name
				END AS full_path
			FROM entries AS e
			JOIN directories AS d ON d.id = e.dir_id
		)
		SELECT target.path, e.size
		FROM directory_size_targets AS target
		JOIN indexed_entries AS i
			ON i.full_path = target.path
			OR substr(i.full_path, 1, length(target.prefix)) = target.prefix
		JOIN entries AS e ON e.id = i.id
		WHERE e.is_dir = 0`
)

func Open(ctx context.Context, dbPath string) (*Store, error) {
	store, err := openStore(ctx, dbPath)
	if err != nil {
		return nil, err
	}
	if err := store.setup(ctx); err != nil {
		_ = store.Close()
		return nil, err
	}
	return store, nil
}

func openStore(_ context.Context, dbPath string) (*Store, error) {
	if dbPath == "" {
		return nil, errors.New("db path is required")
	}

	dsn := fmt.Sprintf("file:%s?_pragma=busy_timeout(5000)", dbPath)
	sqlDB, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, err
	}

	return &Store{db: sqlDB}, nil
}

func (s *Store) Close() error {
	if s.db != nil {
		return s.db.Close()
	}
	return nil
}

func (s *Store) setup(ctx context.Context) error {
	if err := s.applyPragmas(ctx); err != nil {
		return err
	}
	if err := s.migrateLegacyPathSchema(ctx); err != nil {
		return err
	}
	if err := applyMigrations(ctx, s.db); err != nil {
		return err
	}
	return nil
}

func (s *Store) applyPragmas(ctx context.Context) error {
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
	return nil
}

func (s *Store) migrateLegacyPathSchema(ctx context.Context) error {
	hasEntries, err := s.tableExists(ctx, "entries")
	if err != nil {
		return err
	}
	if !hasEntries {
		return nil
	}

	hasPath, err := s.tableHasColumn(ctx, "entries", "path")
	if err != nil {
		return err
	}
	hasDirID, err := s.tableHasColumn(ctx, "entries", "dir_id")
	if err != nil {
		return err
	}

	if !hasPath || hasDirID {
		return nil
	}

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()

	if err := prepareLegacyPathSchema(ctx, tx); err != nil {
		return err
	}
	if err := copyLegacyEntries(ctx, tx); err != nil {
		return err
	}
	if err := swapLegacyPathSchema(ctx, tx); err != nil {
		return err
	}

	if err := tx.Commit(); err != nil {
		return err
	}
	if _, err := s.db.ExecContext(ctx, `VACUUM;`); err != nil {
		return err
	}
	return s.ReindexFTS(ctx)
}

func prepareLegacyPathSchema(ctx context.Context, tx *sql.Tx) error {
	setup := []string{
		`DROP TRIGGER IF EXISTS entries_ai;`,
		`DROP TRIGGER IF EXISTS entries_ad;`,
		`DROP TRIGGER IF EXISTS entries_au;`,
		`DROP TABLE IF EXISTS entries_fts;`,
		`CREATE TABLE IF NOT EXISTS directories (
			id INTEGER PRIMARY KEY,
			path TEXT NOT NULL UNIQUE
		);`,
		`CREATE TABLE IF NOT EXISTS entries_v2 (
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
		);`,
	}
	for _, stmt := range setup {
		if _, err := tx.ExecContext(ctx, stmt); err != nil {
			return err
		}
	}
	return nil
}

func copyLegacyEntries(ctx context.Context, tx *sql.Tx) error {
	rows, err := tx.QueryContext(ctx, `
		SELECT id, name, path, ext, size, mtime, is_dir, root, indexed_at
		FROM entries
		ORDER BY id ASC`)
	if err != nil {
		return err
	}
	defer func() { _ = rows.Close() }()

	insertDir, err := tx.PrepareContext(ctx, `INSERT INTO directories(path) VALUES (?) ON CONFLICT(path) DO NOTHING`)
	if err != nil {
		return err
	}
	defer func() { _ = insertDir.Close() }()
	selectDirID, err := tx.PrepareContext(ctx, `SELECT id FROM directories WHERE path = ?`)
	if err != nil {
		return err
	}
	defer func() { _ = selectDirID.Close() }()
	insertEntry, err := tx.PrepareContext(ctx, `
		INSERT INTO entries_v2(id, name, dir_id, ext, size, mtime, is_dir, root, indexed_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`)
	if err != nil {
		return err
	}
	defer func() { _ = insertEntry.Close() }()

	for rows.Next() {
		if err := copyLegacyEntry(ctx, rows, insertDir, selectDirID, insertEntry); err != nil {
			return err
		}
	}
	return rows.Err()
}

func copyLegacyEntry(ctx context.Context, rows *sql.Rows, insertDir, selectDirID, insertEntry *sql.Stmt) error {
	var (
		id        int64
		name      string
		fullPath  string
		ext       string
		size      int64
		mtime     int64
		isDirInt  int
		root      string
		indexedAt int64
	)
	if err := rows.Scan(&id, &name, &fullPath, &ext, &size, &mtime, &isDirInt, &root, &indexedAt); err != nil {
		return err
	}
	dirPath, baseName := splitPath(fullPath)
	if baseName == "" || baseName == "." {
		baseName = name
	}
	if _, err := insertDir.ExecContext(ctx, dirPath); err != nil {
		return err
	}
	var dirID int64
	if err := selectDirID.QueryRowContext(ctx, dirPath).Scan(&dirID); err != nil {
		return err
	}
	_, err := insertEntry.ExecContext(ctx, id, baseName, dirID, ext, size, mtime, isDirInt, root, indexedAt)
	return err
}

func swapLegacyPathSchema(ctx context.Context, tx *sql.Tx) error {
	for _, stmt := range []string{`DROP TABLE entries;`, `ALTER TABLE entries_v2 RENAME TO entries;`} {
		if _, err := tx.ExecContext(ctx, stmt); err != nil {
			return err
		}
	}
	return nil
}

func (s *Store) tableExists(ctx context.Context, table string) (bool, error) {
	var count int
	err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM sqlite_master WHERE type='table' AND name=?`, table).Scan(&count)
	return count > 0, err
}

func (s *Store) tableHasColumn(ctx context.Context, table, column string) (bool, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT name FROM pragma_table_info(?) WHERE name = ?`, table, column)
	if err != nil {
		return false, err
	}
	defer func() { _ = rows.Close() }()

	if rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			return false, err
		}
		return name == column, nil
	}
	if err := rows.Err(); err != nil {
		return false, err
	}
	return false, nil
}

func (s *Store) UpsertBatch(ctx context.Context, entries []Entry) error {
	if len(entries) == 0 {
		return nil
	}
	return s.upsertBatchOnce(ctx, entries)
}

func (s *Store) upsertBatchOnce(ctx context.Context, entries []Entry) error {
	return s.withTx(ctx, func(tx *sql.Tx) error {
		return upsertBatchTx(ctx, tx, entries)
	})
}

func (s *Store) withTx(ctx context.Context, fn func(*sql.Tx) error) error {
	var err error
	for attempt := 0; attempt < maxTransactionAttempts; attempt++ {
		err = s.withTxOnce(ctx, fn)
		if err == nil {
			return nil
		}
		if !isBusyError(err) {
			return err
		}

		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(transactionRetryDelay * (1 << attempt)):
		}
	}
	return err
}

func (s *Store) withTxOnce(ctx context.Context, fn func(*sql.Tx) error) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	if err := fn(tx); err != nil {
		return err
	}
	return tx.Commit()
}

func upsertBatchTx(ctx context.Context, tx *sql.Tx, entries []Entry) error {
	now := time.Now().Unix()

	dirPaths := make(map[string]struct{}, len(entries))
	for _, entry := range entries {
		dirPath, _ := splitPath(entry.Path)
		dirPaths[dirPath] = struct{}{}
	}
	dirIDByPath, err := ensureDirectoryIDs(ctx, tx, dirPaths)
	if err != nil {
		return err
	}
	return insertBatchEntries(ctx, tx, entries, dirIDByPath, now)
}

func ensureDirectoryIDs(ctx context.Context, tx *sql.Tx, paths map[string]struct{}) (map[string]int64, error) {
	insertDir, err := tx.PrepareContext(ctx, `INSERT INTO directories(path) VALUES (?) ON CONFLICT(path) DO NOTHING`)
	if err != nil {
		return nil, err
	}
	defer func() { _ = insertDir.Close() }()
	selectDirID, err := tx.PrepareContext(ctx, `SELECT id FROM directories WHERE path = ?`)
	if err != nil {
		return nil, err
	}
	defer func() { _ = selectDirID.Close() }()
	for path := range paths {
		if _, err := insertDir.ExecContext(ctx, path); err != nil {
			return nil, err
		}
	}

	dirIDByPath := make(map[string]int64, len(paths))
	for path := range paths {
		var id int64
		if err := selectDirID.QueryRowContext(ctx, path).Scan(&id); err != nil {
			return nil, err
		}
		dirIDByPath[path] = id
	}
	return dirIDByPath, nil
}

func insertBatchEntries(ctx context.Context, tx *sql.Tx, entries []Entry, dirIDByPath map[string]int64, now int64) error {
	entryStmt, err := tx.PrepareContext(ctx, `
		INSERT INTO entries(name, dir_id, ext, size, mtime, is_dir, root, indexed_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(dir_id, name) DO UPDATE SET
			ext = excluded.ext,
			size = excluded.size,
			mtime = excluded.mtime,
			is_dir = excluded.is_dir,
			root = excluded.root,
			indexed_at = excluded.indexed_at`)
	if err != nil {
		return err
	}
	defer func() { _ = entryStmt.Close() }()

	for _, entry := range entries {
		dirPath, baseName := splitPath(entry.Path)
		dirID, ok := dirIDByPath[dirPath]
		if !ok {
			return fmt.Errorf("directory id not found for path %q", dirPath)
		}

		name := strings.TrimSpace(entry.Name)
		if name == "" {
			name = baseName
		}
		isDir := 0
		if entry.IsDir {
			isDir = 1
		}
		if _, err := entryStmt.ExecContext(ctx, name, dirID, entry.Ext, entry.Size, entry.MTime, isDir, entry.Root, now); err != nil {
			return err
		}
	}
	return nil
}

func (s *Store) UpdateDirectorySizes(ctx context.Context, sizes map[string]int64) error {
	cleanSizes := cleanDirectorySizes(sizes)
	if len(cleanSizes) == 0 {
		return nil
	}
	return s.withTx(ctx, func(tx *sql.Tx) error {
		return updateDirectoryValuesTx(ctx, tx, cleanSizes, false)
	})
}

func (s *Store) UpsertBatchWithDirectorySizes(ctx context.Context, entries []Entry) error {
	entries = dedupeEntries(entries)
	if len(entries) == 0 {
		return nil
	}

	return s.withTx(ctx, func(tx *sql.Tx) error {
		deltas := make(map[string]int64)
		for _, entry := range entries {
			oldSize, oldIsDir, exists, err := lookupEntryStateTx(ctx, tx, entry.Path)
			if err != nil {
				return err
			}
			if exists && !oldIsDir {
				addAncestorDelta(deltas, entry.Path, -oldSize)
			}
			if !entry.IsDir {
				addAncestorDelta(deltas, entry.Path, entry.Size)
			}
		}
		if err := upsertBatchTx(ctx, tx, entries); err != nil {
			return err
		}
		return updateDirectoryValuesTx(ctx, tx, deltas, true)
	})
}

func (s *Store) DeleteByPathWithDirectorySize(ctx context.Context, path string) error {
	return s.DeleteByPrefixWithDirectorySize(ctx, path)
}

func (s *Store) DeleteByPrefixWithDirectorySize(ctx context.Context, prefix string) error {
	prefix = strings.TrimSpace(prefix)
	if prefix == "" {
		return nil
	}
	prefix = filepath.Clean(prefix)

	return s.withTx(ctx, func(tx *sql.Tx) error {
		total, err := sumFileSizesByPrefixTx(ctx, tx, prefix)
		if err != nil {
			return err
		}
		if err := deleteEntriesByPrefixes(ctx, tx, []string{prefix}); err != nil {
			return err
		}
		if total != 0 {
			deltas := make(map[string]int64)
			addAncestorDeltaForDir(deltas, filepath.Dir(prefix), -total)
			if err := updateDirectoryValuesTx(ctx, tx, deltas, true); err != nil {
				return err
			}
		}
		if err := pruneEmptyDirectoriesWith(ctx, tx); err != nil {
			return err
		}
		return nil
	})
}

func (s *Store) RecalculateDirectorySizes(ctx context.Context, roots []string) error {
	roots = cleanPathList(roots)
	if len(roots) == 0 {
		return nil
	}

	return s.withTx(ctx, func(tx *sql.Tx) error {
		directories, err := collectDirectorySizes(ctx, tx, roots)
		if err != nil {
			return err
		}
		if len(directories) == 0 {
			return nil
		}

		if err := createDirectorySizeTargets(ctx, tx, directories); err != nil {
			return err
		}
		defer func() { _, _ = tx.ExecContext(context.Background(), `DROP TABLE IF EXISTS directory_size_targets`) }()
		if err := collectDirectorySizeTotals(ctx, tx, directories); err != nil {
			return err
		}
		return updateDirectoryValuesTx(ctx, tx, directories, false)
	})
}

func collectDirectorySizeTotals(ctx context.Context, tx *sql.Tx, directories map[string]int64) error {
	rows, err := tx.QueryContext(ctx, recalculateDirectorySizesSQL, pathSeparator(), pathSeparator())
	if err != nil {
		return err
	}
	defer func() { _ = rows.Close() }()
	for rows.Next() {
		var root string
		var size int64
		if err := rows.Scan(&root, &size); err != nil {
			return err
		}
		directories[root] += size
	}
	return rows.Err()
}

func collectDirectorySizes(ctx context.Context, tx *sql.Tx, roots []string) (map[string]int64, error) {
	directories := make(map[string]int64)
	for _, root := range roots {
		for dir := filepath.Clean(root); ; {
			exists, err := directoryEntryExistsTx(ctx, tx, dir)
			if err != nil {
				return nil, err
			}
			if !exists {
				break
			}
			directories[dir] = 0
			parent := filepath.Dir(dir)
			if parent == dir {
				break
			}
			dir = parent
		}
	}
	return directories, nil
}

func createDirectorySizeTargets(ctx context.Context, tx *sql.Tx, directories map[string]int64) error {
	if _, err := tx.ExecContext(ctx, `CREATE TEMP TABLE directory_size_targets (path TEXT PRIMARY KEY, prefix TEXT NOT NULL)`); err != nil {
		return err
	}
	stmt, err := tx.PrepareContext(ctx, `INSERT INTO directory_size_targets(path, prefix) VALUES (?, ?)`)
	if err != nil {
		return err
	}
	defer func() { _ = stmt.Close() }()
	for path := range directories {
		if _, err := stmt.ExecContext(ctx, path, pathPrefix(path)); err != nil {
			return err
		}
	}
	return nil
}

type txExecutor interface {
	ExecContext(ctx context.Context, query string, args ...any) (sql.Result, error)
	PrepareContext(ctx context.Context, query string) (*sql.Stmt, error)
	QueryContext(ctx context.Context, query string, args ...any) (*sql.Rows, error)
	QueryRowContext(ctx context.Context, query string, args ...any) *sql.Row
}

func updateDirectoryValuesTx(ctx context.Context, tx txExecutor, values map[string]int64, delta bool) error {
	if len(values) == 0 {
		return nil
	}
	if _, err := tx.ExecContext(ctx, `CREATE TEMP TABLE directory_size_values (path TEXT PRIMARY KEY, value INTEGER NOT NULL)`); err != nil {
		return err
	}
	defer func() { _, _ = tx.ExecContext(context.Background(), `DROP TABLE IF EXISTS directory_size_values`) }()

	stmt, err := tx.PrepareContext(ctx, `INSERT INTO directory_size_values(path, value) VALUES (?, ?) ON CONFLICT(path) DO UPDATE SET value = excluded.value`)
	if err != nil {
		return err
	}
	for path, value := range values {
		path = filepath.Clean(strings.TrimSpace(path))
		if path == "" || path == "." {
			continue
		}
		if _, err := stmt.ExecContext(ctx, path, value); err != nil {
			_ = stmt.Close()
			return err
		}
	}
	if err := stmt.Close(); err != nil {
		return err
	}

	query := fullPathCTE() + `
		UPDATE entries
		SET size = (SELECT CASE WHEN ? THEN
				CASE WHEN entries.size + value < 0 THEN 0 ELSE entries.size + value END
			ELSE value END
			FROM directory_size_values
			WHERE directory_size_values.path = (
				SELECT indexed_entries.full_path
				FROM indexed_entries
				WHERE indexed_entries.id = entries.id
			))
		WHERE is_dir = 1
		  AND id IN (
			SELECT indexed_entries.id
			FROM indexed_entries
			JOIN directory_size_values ON directory_size_values.path = indexed_entries.full_path
		)`
	_, err = tx.ExecContext(ctx, query, pathSeparator(), pathSeparator(), delta)
	return err
}

func cleanDirectorySizes(sizes map[string]int64) map[string]int64 {
	clean := make(map[string]int64, len(sizes))
	for path, size := range sizes {
		path = filepath.Clean(strings.TrimSpace(path))
		if path == "" || path == "." {
			continue
		}
		if size < 0 {
			size = 0
		}
		clean[path] = size
	}
	return clean
}

func dedupeEntries(entries []Entry) []Entry {
	byPath := make(map[string]Entry, len(entries))
	order := make([]string, 0, len(entries))
	for _, entry := range entries {
		path := filepath.Clean(entry.Path)
		if _, ok := byPath[path]; !ok {
			order = append(order, path)
		}
		entry.Path = path
		byPath[path] = entry
	}
	out := make([]Entry, 0, len(order))
	for _, path := range order {
		out = append(out, byPath[path])
	}
	return out
}

func lookupEntryStateTx(ctx context.Context, tx txExecutor, path string) (size int64, isDir bool, exists bool, err error) {
	dirPath, baseName := splitPath(path)
	err = tx.QueryRowContext(ctx, `
		SELECT e.size, e.is_dir
		FROM entries AS e
		JOIN directories AS d ON d.id = e.dir_id
		WHERE d.path = ? AND e.name = ?`, dirPath, baseName).Scan(&size, &isDir)
	if errors.Is(err, sql.ErrNoRows) {
		return 0, false, false, nil
	}
	return size, isDir, err == nil, err
}

func sumFileSizesByPrefixTx(ctx context.Context, tx txExecutor, prefix string) (int64, error) {
	pathPrefixValue := pathPrefix(prefix)
	query := fullPathCTE() + `
		SELECT COALESCE(SUM(e.size), 0)
		FROM indexed_entries AS i
		JOIN entries AS e ON e.id = i.id
		WHERE e.is_dir = 0
		  AND (i.full_path = ? OR substr(i.full_path, 1, length(?)) = ?)`
	var total int64
	err := tx.QueryRowContext(ctx, query, pathSeparator(), pathSeparator(), prefix, pathPrefixValue, pathPrefixValue).Scan(&total)
	return total, err
}

func directoryEntryExistsTx(ctx context.Context, tx txExecutor, path string) (bool, error) {
	query := fullPathCTE() + `
		SELECT EXISTS(
			SELECT 1 FROM indexed_entries AS i
			JOIN entries AS e ON e.id = i.id
			WHERE e.is_dir = 1 AND i.full_path = ?
		)`
	var exists bool
	err := tx.QueryRowContext(ctx, query, pathSeparator(), pathSeparator(), filepath.Clean(path)).Scan(&exists)
	return exists, err
}

func addAncestorDelta(deltas map[string]int64, path string, delta int64) {
	if delta == 0 {
		return
	}
	addAncestorDeltaForDir(deltas, filepath.Dir(filepath.Clean(path)), delta)
}

func addAncestorDeltaForDir(deltas map[string]int64, dir string, delta int64) {
	for dir = filepath.Clean(dir); ; {
		deltas[dir] += delta
		parent := filepath.Dir(dir)
		if parent == dir {
			return
		}
		dir = parent
	}
}

func isWithinPath(root, path string) bool {
	root = filepath.Clean(root)
	path = filepath.Clean(path)
	if root == path {
		return true
	}
	rel, err := filepath.Rel(root, path)
	if err != nil || rel == ".." {
		return false
	}
	return !strings.HasPrefix(rel, ".."+string(filepath.Separator))
}

func isBusyError(err error) bool {
	if err == nil {
		return false
	}
	msg := strings.ToLower(err.Error())
	return strings.Contains(msg, "database is locked") ||
		strings.Contains(msg, "sqlite_busy") ||
		strings.Contains(msg, "sqlite_busy_snapshot")
}

func (s *Store) DeleteByPath(ctx context.Context, path string) error {
	if strings.TrimSpace(path) == "" {
		return nil
	}
	dirPath, baseName := splitPath(path)

	_, err := s.db.ExecContext(ctx, `
		DELETE FROM entries
		WHERE name = ?
		  AND dir_id = (SELECT id FROM directories WHERE path = ?)`, baseName, dirPath)
	if err != nil {
		return err
	}
	return s.pruneEmptyDirectories(ctx)
}

func (s *Store) DeleteByPrefix(ctx context.Context, prefix string) error {
	if strings.TrimSpace(prefix) == "" {
		return nil
	}

	if err := deleteEntriesByPrefixes(ctx, s.db, []string{prefix}); err != nil {
		return err
	}
	return s.pruneEmptyDirectories(ctx)
}

func (s *Store) pruneEmptyDirectories(ctx context.Context) error {
	return pruneEmptyDirectoriesWith(ctx, s.db)
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

	//noinspection SqlResolve,SqlDialectInspection,SqlNoDataSourceInspection
	q := `
		SELECT e.name, d.path, e.ext, e.size, e.mtime, e.is_dir, e.root, e.indexed_at
		FROM entries_fts f
		JOIN entries e ON e.id = f.rowid
		JOIN directories d ON d.id = e.dir_id
		WHERE entries_fts MATCH ?`
	args := []any{buildFTSQuery(query)}
	q, args = applySearchFilters(q, args, opts)
	q += "\n\t\t" + "ORDER BY" + `
			CASE
				WHEN e.name = ? THEN 0
				WHEN e.name LIKE ? THEN 1
				ELSE 2
			END,
			e.name ASC,
			d.path ASC
		LIMIT ? OFFSET ?`
	args = append(args, query, query+"%", opts.Limit, opts.Offset)

	rows, err := s.db.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()

	return scanEntries(rows, opts.Limit)
}

func (s *Store) searchByLike(ctx context.Context, query string, opts SearchOptions) ([]Entry, error) {
	nameLike := wildcardToLike(query)
	if nameLike == "" {
		nameLike = "%"
	}

	q := `
		SELECT e.name, d.path, e.ext, e.size, e.mtime, e.is_dir, e.root, e.indexed_at
		FROM entries e
		JOIN directories d ON d.id = e.dir_id
		WHERE e.name LIKE ?`
	args := []any{nameLike}

	q, args = applySearchFilters(q, args, opts)
	q += " " + "ORDER BY" + ` e.name ASC, d.path ASC LIMIT ? OFFSET ?`
	args = append(args, opts.Limit, opts.Offset)

	rows, err := s.db.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()

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
			dirPath   string
			isDirInt  int
			indexedAt int64
		)
		if err := rows.Scan(&entry.Name, &dirPath, &entry.Ext, &entry.Size, &entry.MTime, &isDirInt, &entry.Root, &indexedAt); err != nil {
			return nil, err
		}
		entry.Path = joinPath(dirPath, entry.Name)
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
	return Entry{Name: base, Path: filepath.Clean(path), Ext: ext, Size: size, MTime: mtime.Unix(), IsDir: isDir, Root: root}
}

func (s *Store) ReindexFTS(ctx context.Context) error {
	stmts := []string{
		`DROP TRIGGER IF EXISTS entries_ai;`,
		`DROP TRIGGER IF EXISTS entries_ad;`,
		`DROP TRIGGER IF EXISTS entries_au;`,
		`DROP TABLE IF EXISTS entries_fts;`,
		`CREATE VIRTUAL TABLE IF NOT EXISTS entries_fts USING fts5(
			name,
			content='entries',
			content_rowid='id',
			tokenize='unicode61 remove_diacritics 2',
			prefix='3'
		);`,
		`INSERT INTO entries_fts(rowid, name) SELECT id, name FROM entries;`,
		`CREATE TRIGGER IF NOT EXISTS entries_ai AFTER INSERT ON entries BEGIN
			INSERT INTO entries_fts(rowid, name) VALUES (new.id, new.name);
		END;`,
		`CREATE TRIGGER IF NOT EXISTS entries_ad AFTER DELETE ON entries BEGIN
			INSERT INTO entries_fts(entries_fts, rowid, name) VALUES ('delete', old.id, old.name);
		END;`,
		`CREATE TRIGGER IF NOT EXISTS entries_au AFTER UPDATE ON entries BEGIN
			INSERT INTO entries_fts(entries_fts, rowid, name) VALUES ('delete', old.id, old.name);
			INSERT INTO entries_fts(rowid, name) VALUES (new.id, new.name);
		END;`,
	}
	for _, stmt := range stmts {
		if _, err := s.db.ExecContext(ctx, stmt); err != nil {
			return err
		}
	}
	return nil
}

func (s *Store) Count(ctx context.Context) (int64, error) {
	var total int64
	err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM entries`).Scan(&total)
	return total, err
}

// TopEntries returns the aggregate size of root and its largest direct children.
func (s *Store) TopEntries(ctx context.Context, root string) (int64, []Entry, error) {
	root = filepath.Clean(strings.TrimSpace(root))
	if root == "" || root == "." {
		return 0, nil, errors.New("root is required")
	}

	dirPath, baseName := splitPath(root)
	var total int64
	err := s.db.QueryRowContext(ctx, `
		SELECT COALESCE(e.size, 0)
		FROM entries AS e
		JOIN directories AS d ON d.id = e.dir_id
		WHERE d.path = ? AND e.name = ? AND e.is_dir = 1`, dirPath, baseName).Scan(&total)
	if errors.Is(err, sql.ErrNoRows) {
		total = 0
	} else if err != nil {
		return 0, nil, err
	}

	rows, err := s.db.QueryContext(ctx, `
		SELECT e.name, d.path, e.ext, e.size, e.mtime, e.is_dir, e.root, e.indexed_at
		FROM entries AS e
		JOIN directories AS d ON d.id = e.dir_id
		WHERE d.path = ?
		ORDER BY e.size DESC, e.name ASC`, root)
	if err != nil {
		return 0, nil, err
	}
	defer func() { _ = rows.Close() }()

	entries, err := scanEntries(rows, 0)
	if err != nil {
		return 0, nil, err
	}
	return total, entries, nil
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

func wildcardToLike(query string) string {
	like := strings.TrimSpace(strings.ReplaceAll(query, "*", "%"))
	if like == "" {
		return ""
	}
	if !strings.Contains(like, "%") {
		like = "%" + like + "%"
	}
	return like
}

func splitPath(path string) (dirPath, baseName string) {
	clean := filepath.Clean(path)
	return filepath.Dir(clean), filepath.Base(clean)
}

func joinPath(dirPath, baseName string) string {
	if dirPath == "/" {
		return filepath.Clean("/" + baseName)
	}
	return filepath.Clean(filepath.Join(dirPath, baseName))
}
