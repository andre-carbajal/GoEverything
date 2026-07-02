package db

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"time"

	"github.com/uptrace/bun"
	"github.com/uptrace/bun/dialect/sqlitedialect"
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
	db  *sql.DB
	bun *bun.DB
}

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

	bunDB := bun.NewDB(sqlDB, sqlitedialect.New())
	return &Store{db: sqlDB, bun: bunDB}, nil
}

func (s *Store) Close() error {
	if s.bun != nil {
		_ = s.bun.Close()
	}
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

	//noinspection SqlResolve,SqlRedundantOrderingDirection
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

	//noinspection SqlResolve
	insertEntry, err := tx.PrepareContext(ctx, `
		INSERT INTO entries_v2(id, name, dir_id, ext, size, mtime, is_dir, root, indexed_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`)
	if err != nil {
		return err
	}
	defer func() { _ = insertEntry.Close() }()

	for rows.Next() {
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

		if _, err := insertEntry.ExecContext(ctx, id, baseName, dirID, ext, size, mtime, isDirInt, root, indexedAt); err != nil {
			return err
		}
	}
	if err := rows.Err(); err != nil {
		return err
	}

	//noinspection SqlResolve
	swap := []string{
		`DROP TABLE entries;`,
		`ALTER TABLE entries_v2 RENAME TO entries;`,
	}
	for _, stmt := range swap {
		if _, err := tx.ExecContext(ctx, stmt); err != nil {
			return err
		}
	}

	if err := tx.Commit(); err != nil {
		return err
	}
	if _, err := s.db.ExecContext(ctx, `VACUUM;`); err != nil {
		return err
	}
	return s.ReindexFTS(ctx)
}

func (s *Store) tableExists(ctx context.Context, table string) (bool, error) {
	var count int
	err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM sqlite_master WHERE type='table' AND name=?`, table).Scan(&count)
	return count > 0, err
}

func (s *Store) tableHasColumn(ctx context.Context, table, column string) (bool, error) {
	rows, err := s.db.QueryContext(ctx, fmt.Sprintf(`PRAGMA table_info(%s)`, table))
	if err != nil {
		return false, err
	}
	defer func() { _ = rows.Close() }()

	for rows.Next() {
		var (
			cid       int
			name      string
			typ       string
			notNull   int
			dfltValue any
			pk        int
		)
		if err := rows.Scan(&cid, &name, &typ, &notNull, &dfltValue, &pk); err != nil {
			return false, err
		}
		if name == column {
			return true, nil
		}
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
	return s.bun.RunInTx(ctx, nil, func(ctx context.Context, tx bun.Tx) error {
		now := time.Now().Unix()

		dirSet := make(map[string]struct{}, len(entries))
		for _, entry := range entries {
			dirPath, _ := splitPath(entry.Path)
			dirSet[dirPath] = struct{}{}
		}

		dirs := make([]*DirectoryModel, 0, len(dirSet))
		paths := make([]string, 0, len(dirSet))
		for path := range dirSet {
			dirs = append(dirs, &DirectoryModel{Path: path})
			paths = append(paths, path)
		}

		if len(dirs) > 0 {
			if _, err := tx.NewInsert().
				Model(&dirs).
				On("CONFLICT (path) DO NOTHING").
				Exec(ctx); err != nil {
				return err
			}
		}

		var directoryRows []DirectoryModel
		if len(paths) > 0 {
			if err := tx.NewSelect().
				Model(&directoryRows).
				Where("path IN (?)", bun.List(paths)).
				Scan(ctx); err != nil {
				return err
			}
		}

		dirIDByPath := make(map[string]int64, len(directoryRows))
		for _, row := range directoryRows {
			dirIDByPath[row.Path] = row.ID
		}

		models := make([]*EntryModel, 0, len(entries))
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
			models = append(models, &EntryModel{
				Name:      name,
				DirID:     dirID,
				Ext:       entry.Ext,
				Size:      entry.Size,
				MTime:     entry.MTime,
				IsDir:     entry.IsDir,
				Root:      entry.Root,
				IndexedAt: now,
			})
		}

		if len(models) == 0 {
			return nil
		}

		_, err := tx.NewInsert().
			Model(&models).
			On("CONFLICT (dir_id, name) DO UPDATE").
			Set("ext = EXCLUDED.ext").
			Set("size = EXCLUDED.size").
			Set("mtime = EXCLUDED.mtime").
			Set("is_dir = EXCLUDED.is_dir").
			Set("root = EXCLUDED.root").
			Set("indexed_at = EXCLUDED.indexed_at").
			Exec(ctx)
		return err
	})
}

func isBusyError(err error) bool {
	if err == nil {
		return false
	}
	msg := strings.ToLower(err.Error())
	return strings.Contains(msg, "database is locked") || strings.Contains(msg, "sqlite_busy")
}

func (s *Store) DeleteByPath(ctx context.Context, path string) error {
	if strings.TrimSpace(path) == "" {
		return nil
	}
	dirPath, baseName := splitPath(path)

	_, err := s.bun.NewDelete().
		Model((*EntryModel)(nil)).
		Where("name = ?", baseName).
		Where("dir_id = (SELECT id FROM directories WHERE path = ?)", dirPath).
		Exec(ctx)
	if err != nil {
		return err
	}
	return s.pruneEmptyDirectories(ctx)
}

func (s *Store) DeleteByPrefix(ctx context.Context, prefix string) error {
	if strings.TrimSpace(prefix) == "" {
		return nil
	}
	clean := filepath.Clean(prefix)

	subQuery := s.bun.NewSelect().
		TableExpr("entries AS e").
		Column("e.id").
		Join("JOIN directories AS d ON d.id = e.dir_id").
		Where("d.path = ?", clean).
		WhereOr("d.path LIKE ?", clean+"/%").
		WhereOr("(CASE WHEN d.path = '/' THEN '/' || e.name ELSE d.path || '/' || e.name END) = ?", clean).
		WhereOr("(CASE WHEN d.path = '/' THEN '/' || e.name ELSE d.path || '/' || e.name END) LIKE ?", clean+"/%")

	_, err := s.bun.NewDelete().
		Model((*EntryModel)(nil)).
		Where("id IN (?)", subQuery).
		Exec(ctx)
	if err != nil {
		return err
	}
	return s.pruneEmptyDirectories(ctx)
}

func (s *Store) pruneEmptyDirectories(ctx context.Context) error {
	subQuery := s.bun.NewSelect().
		TableExpr("entries").
		ColumnExpr("DISTINCT dir_id")

	_, err := s.bun.NewDelete().
		Model((*DirectoryModel)(nil)).
		Where("id NOT IN (?)", subQuery).
		Exec(ctx)
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
	err := s.bun.NewSelect().Model((*EntryModel)(nil)).ColumnExpr("COUNT(*)").Scan(ctx, &total)
	return total, err
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
