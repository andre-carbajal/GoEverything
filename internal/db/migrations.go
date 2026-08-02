package db

import (
	"context"
	"database/sql"
	"embed"
	"fmt"
	"io/fs"
	"sort"
	"strconv"
	"strings"
	"time"
)

const migrationsTableName = "goose_db_version"

//go:embed migrations/*.sql
var embeddedMigrations embed.FS

type MigrationStatus struct {
	Version   int64
	Name      string
	State     string
	AppliedAt time.Time
}

type migration struct {
	Version int64
	Name    string
	SQL     string
}

type migrationState struct {
	appliedAt time.Time
	applied   bool
}

func applyMigrations(ctx context.Context, sqlDB *sql.DB) error {
	if err := ensureMigrationTable(ctx, sqlDB); err != nil {
		return err
	}

	migrations, err := loadMigrations()
	if err != nil {
		return err
	}
	states, err := migrationStates(ctx, sqlDB)
	if err != nil {
		return err
	}

	for _, item := range migrations {
		if state, ok := states[item.Version]; ok && state.applied {
			continue
		}

		tx, err := sqlDB.BeginTx(ctx, nil)
		if err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, item.SQL); err != nil {
			_ = tx.Rollback()
			return fmt.Errorf("apply migration %s: %w", item.Name, err)
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO `+migrationsTableName+`(version_id, is_applied)
			VALUES (?, 1)`, item.Version); err != nil {
			_ = tx.Rollback()
			return fmt.Errorf("record migration %s: %w", item.Name, err)
		}
		if err := tx.Commit(); err != nil {
			return fmt.Errorf("commit migration %s: %w", item.Name, err)
		}
	}
	return nil
}

func currentSchemaVersion(ctx context.Context, sqlDB *sql.DB) (int64, error) {
	if err := ensureMigrationTable(ctx, sqlDB); err != nil {
		return 0, err
	}
	states, err := migrationStates(ctx, sqlDB)
	if err != nil {
		return 0, err
	}

	version := int64(0)
	for candidate, state := range states {
		if state.applied && candidate > version {
			version = candidate
		}
	}
	return version, nil
}

func collectMigrationStatus(ctx context.Context, sqlDB *sql.DB) ([]MigrationStatus, error) {
	if err := ensureMigrationTable(ctx, sqlDB); err != nil {
		return nil, err
	}
	migrations, err := loadMigrations()
	if err != nil {
		return nil, err
	}
	states, err := migrationStates(ctx, sqlDB)
	if err != nil {
		return nil, err
	}

	out := make([]MigrationStatus, 0, len(migrations))
	for _, item := range migrations {
		state := migrationStatus{State: "pending"}
		if applied, ok := states[item.Version]; ok {
			if applied.applied {
				state = migrationStatus{State: "up", AppliedAt: applied.appliedAt}
			}
		}
		out = append(out, MigrationStatus{
			Version:   item.Version,
			Name:      item.Name,
			State:     state.State,
			AppliedAt: state.AppliedAt,
		})
	}
	return out, nil
}

type migrationStatus struct {
	State     string
	AppliedAt time.Time
}

func ensureMigrationTable(ctx context.Context, sqlDB *sql.DB) error {
	_, err := sqlDB.ExecContext(ctx, `CREATE TABLE IF NOT EXISTS `+migrationsTableName+` (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			version_id INTEGER NOT NULL,
			is_applied INTEGER NOT NULL,
			tstamp TIMESTAMP DEFAULT (datetime('now'))
		)`)
	return err
}

func migrationStates(ctx context.Context, sqlDB *sql.DB) (map[int64]migrationState, error) {
	rows, err := sqlDB.QueryContext(ctx, `SELECT version_id, is_applied, tstamp
		FROM `+migrationsTableName+`
		ORDER BY id ASC`)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()

	states := make(map[int64]migrationState)
	for rows.Next() {
		var (
			version   int64
			isApplied int
			tstamp    any
		)
		if err := rows.Scan(&version, &isApplied, &tstamp); err != nil {
			return nil, err
		}
		states[version] = migrationState{
			applied:   isApplied != 0,
			appliedAt: parseMigrationTime(tstamp),
		}
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return states, nil
}

func loadMigrations() ([]migration, error) {
	entries, err := fs.ReadDir(embeddedMigrations, "migrations")
	if err != nil {
		return nil, fmt.Errorf("read embedded migrations: %w", err)
	}

	migrations := make([]migration, 0, len(entries))
	seen := make(map[int64]struct{}, len(entries))
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".sql") {
			continue
		}
		version, err := migrationVersion(entry.Name())
		if err != nil {
			return nil, err
		}
		if _, exists := seen[version]; exists {
			return nil, fmt.Errorf("duplicate migration version %d", version)
		}
		seen[version] = struct{}{}

		data, err := fs.ReadFile(embeddedMigrations, "migrations/"+entry.Name())
		if err != nil {
			return nil, fmt.Errorf("read migration %s: %w", entry.Name(), err)
		}
		migrations = append(migrations, migration{
			Version: version,
			Name:    entry.Name(),
			SQL:     migrationUpSQL(string(data)),
		})
	}

	sort.Slice(migrations, func(i, j int) bool {
		return migrations[i].Version < migrations[j].Version
	})
	return migrations, nil
}

func migrationVersion(name string) (int64, error) {
	prefix, _, ok := strings.Cut(name, "_")
	if !ok || prefix == "" {
		return 0, fmt.Errorf("invalid migration filename %q", name)
	}
	version, err := strconv.ParseInt(prefix, 10, 64)
	if err != nil || version <= 0 {
		return 0, fmt.Errorf("invalid migration version in %q", name)
	}
	return version, nil
}

func migrationUpSQL(contents string) string {
	lines := strings.Split(contents, "\n")
	up := make([]string, 0, len(lines))
	inUp := false
	for _, line := range lines {
		marker := strings.TrimSpace(line)
		switch marker {
		case "-- +goose Up":
			inUp = true
			continue
		case "-- +goose Down":
			return strings.Join(up, "\n")
		}
		if inUp && marker != "-- +goose StatementBegin" && marker != "-- +goose StatementEnd" {
			up = append(up, line)
		}
	}
	return strings.Join(up, "\n")
}

func parseMigrationTime(value any) time.Time {
	var text string
	switch typed := value.(type) {
	case time.Time:
		return typed
	case string:
		text = typed
	case []byte:
		text = string(typed)
	default:
		return time.Time{}
	}
	for _, layout := range []string{
		time.RFC3339Nano,
		"2006-01-02 15:04:05.999999999-07:00",
		"2006-01-02 15:04:05.999999999",
		"2006-01-02 15:04:05",
	} {
		if parsed, err := time.Parse(layout, text); err == nil {
			return parsed
		}
	}
	return time.Time{}
}

func SchemaVersion(ctx context.Context, dbPath string) (int64, error) {
	store, err := openStore(ctx, dbPath)
	if err != nil {
		return 0, err
	}
	defer func() { _ = store.Close() }()

	if err := store.applyPragmas(ctx); err != nil {
		return 0, err
	}
	return currentSchemaVersion(ctx, store.db)
}

func SchemaStatus(ctx context.Context, dbPath string) ([]MigrationStatus, error) {
	store, err := openStore(ctx, dbPath)
	if err != nil {
		return nil, err
	}
	defer func() { _ = store.Close() }()

	if err := store.applyPragmas(ctx); err != nil {
		return nil, err
	}
	return collectMigrationStatus(ctx, store.db)
}
