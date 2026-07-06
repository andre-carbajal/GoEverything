package db

import (
	"context"
	"database/sql"
	"fmt"
	"path/filepath"
	"strings"
	"time"
)

type sqlExecer interface {
	ExecContext(ctx context.Context, query string, args ...any) (sql.Result, error)
}

func (s *Store) BeginScan(ctx context.Context, _ []string) (int64, error) {
	now := time.Now().Unix()
	if _, err := s.db.ExecContext(ctx, `DELETE FROM scan_sessions WHERE started_at < ?`, now-86400); err != nil {
		return 0, err
	}

	res, err := s.db.ExecContext(ctx, `INSERT INTO scan_sessions(started_at) VALUES (?)`, now)
	if err != nil {
		return 0, err
	}
	return res.LastInsertId()
}

func (s *Store) MarkSeenBatch(ctx context.Context, sessionID int64, entries []Entry) error {
	if sessionID == 0 || len(entries) == 0 {
		return nil
	}

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()

	stmt, err := tx.PrepareContext(ctx, `
		INSERT INTO scan_seen_paths(session_id, path)
		VALUES (?, ?)
		ON CONFLICT(session_id, path) DO NOTHING`)
	if err != nil {
		return err
	}
	defer func() { _ = stmt.Close() }()

	for _, entry := range entries {
		path := strings.TrimSpace(entry.Path)
		if path == "" {
			continue
		}
		if _, err := stmt.ExecContext(ctx, sessionID, filepath.Clean(path)); err != nil {
			return err
		}
	}

	return tx.Commit()
}

func (s *Store) MarkUnreadablePrefix(ctx context.Context, sessionID int64, path string) error {
	path = strings.TrimSpace(path)
	if sessionID == 0 || path == "" {
		return nil
	}
	_, err := s.db.ExecContext(ctx, `
		INSERT INTO scan_protected_prefixes(session_id, path)
		VALUES (?, ?)
		ON CONFLICT(session_id, path) DO NOTHING`, sessionID, filepath.Clean(path))
	return err
}

func (s *Store) FinishScan(ctx context.Context, sessionID int64, roots []string) error {
	if sessionID == 0 {
		return nil
	}
	cleanRoots := cleanPathList(roots)
	if len(cleanRoots) == 0 {
		return s.AbortScan(ctx, sessionID)
	}

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()

	if err := deleteMissingFromScan(ctx, tx, sessionID, cleanRoots); err != nil {
		return err
	}
	if err := pruneEmptyDirectoriesWith(ctx, tx); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM scan_sessions WHERE id = ?`, sessionID); err != nil {
		return err
	}
	return tx.Commit()
}

func (s *Store) AbortScan(ctx context.Context, sessionID int64) error {
	if sessionID == 0 {
		return nil
	}
	_, err := s.db.ExecContext(ctx, `DELETE FROM scan_sessions WHERE id = ?`, sessionID)
	return err
}

func deleteEntriesByPrefixes(ctx context.Context, execer sqlExecer, prefixes []string) error {
	cleanPrefixes := cleanPathList(prefixes)
	if len(cleanPrefixes) == 0 {
		return nil
	}

	clauses := make([]string, 0, len(cleanPrefixes))
	args := []any{pathSeparator(), pathSeparator()}
	for _, prefix := range cleanPrefixes {
		prefixWithSep := pathPrefix(prefix)
		clauses = append(clauses, `(full_path = ? OR substr(full_path, 1, length(?)) = ?)`)
		args = append(args, prefix, prefixWithSep, prefixWithSep)
	}

	query := fullPathCTE() + `
		DELETE FROM entries
		WHERE id IN (
			SELECT id
			FROM indexed_entries
			WHERE ` + strings.Join(clauses, " OR ") + `
		)`
	_, err := execer.ExecContext(ctx, query, args...)
	return err
}

func deleteMissingFromScan(ctx context.Context, execer sqlExecer, sessionID int64, roots []string) error {
	clauses := make([]string, 0, len(roots))
	args := []any{pathSeparator(), pathSeparator()}
	for _, root := range roots {
		rootWithSep := pathPrefix(root)
		clauses = append(clauses, `(i.full_path = ? OR substr(i.full_path, 1, length(?)) = ?)`)
		args = append(args, root, rootWithSep, rootWithSep)
	}
	args = append(args, sessionID, sessionID, pathSeparator(), pathSeparator(), pathSeparator(), pathSeparator())

	query := fullPathCTE() + `
		DELETE FROM entries
		WHERE id IN (
			SELECT i.id
			FROM indexed_entries AS i
			WHERE (` + strings.Join(clauses, " OR ") + `)
				AND NOT EXISTS (
					SELECT 1
					FROM scan_seen_paths AS seen
					WHERE seen.session_id = ?
						AND seen.path = i.full_path
				)
				AND NOT EXISTS (
					SELECT 1
					FROM scan_protected_prefixes AS protected
					WHERE protected.session_id = ?
						AND (
							i.full_path = protected.path
							OR substr(i.full_path, 1, length(
								CASE
									WHEN substr(protected.path, length(protected.path), 1) = ? THEN protected.path
									ELSE protected.path || ?
								END
							)) = CASE
								WHEN substr(protected.path, length(protected.path), 1) = ? THEN protected.path
								ELSE protected.path || ?
							END
						)
				)
		)`
	_, err := execer.ExecContext(ctx, query, args...)
	return err
}

func pruneEmptyDirectoriesWith(ctx context.Context, execer sqlExecer) error {
	_, err := execer.ExecContext(ctx, `
		DELETE FROM directories
		WHERE id NOT IN (
			SELECT DISTINCT dir_id
			FROM entries
		)`)
	return err
}

func fullPathCTE() string {
	return `
		WITH indexed_entries AS (
			SELECT
				e.id,
				CASE
					WHEN substr(d.path, length(d.path), 1) = ? THEN d.path || e.name
					ELSE d.path || ? || e.name
				END AS full_path
			FROM entries AS e
			JOIN directories AS d ON d.id = e.dir_id
		)`
}

func cleanPathList(paths []string) []string {
	out := make([]string, 0, len(paths))
	seen := make(map[string]struct{}, len(paths))
	for _, path := range paths {
		path = strings.TrimSpace(path)
		if path == "" {
			continue
		}
		clean := filepath.Clean(path)
		if _, ok := seen[clean]; ok {
			continue
		}
		seen[clean] = struct{}{}
		out = append(out, clean)
	}
	return out
}

func pathSeparator() string {
	return string(filepath.Separator)
}

func pathPrefix(path string) string {
	sep := pathSeparator()
	clean := filepath.Clean(path)
	if strings.HasSuffix(clean, sep) {
		return clean
	}
	return fmt.Sprintf("%s%s", clean, sep)
}
