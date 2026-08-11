package db

import (
	"context"
	"database/sql"
	"path/filepath"
	"testing"

	// Register the SQLite driver used by database/sql.
	_ "modernc.org/sqlite"
)

func TestMigrationRunnerAppliesAllMigrationsAndReportsStatus(t *testing.T) {
	ctx := context.Background()
	dbPath := filepath.Join(t.TempDir(), "migrations.db")

	store, err := Open(ctx, dbPath)
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	if err := store.Close(); err != nil {
		t.Fatalf("close store: %v", err)
	}

	version, err := SchemaVersion(ctx, dbPath)
	if err != nil {
		t.Fatalf("schema version: %v", err)
	}
	if version != 5 {
		t.Fatalf("expected schema version 5, got %d", version)
	}

	status, err := SchemaStatus(ctx, dbPath)
	if err != nil {
		t.Fatalf("schema status: %v", err)
	}
	if len(status) != 5 {
		t.Fatalf("expected five migrations, got %d", len(status))
	}
	for _, item := range status {
		if item.State != "up" || item.AppliedAt.IsZero() {
			t.Fatalf("migration %d is not applied: %#v", item.Version, item)
		}
	}
}

func TestMigrationRunnerResumesExistingGooseVersionTable(t *testing.T) {
	ctx := context.Background()
	dbPath := filepath.Join(t.TempDir(), "partial.db")

	status, err := preparePartialMigrations(ctx, dbPath, 3)
	if err != nil {
		t.Fatalf("prepare partial migrations: %v", err)
	}
	assertMigrationStates(t, status, 3)

	store, err := Open(ctx, dbPath)
	if err != nil {
		t.Fatalf("resume store: %v", err)
	}
	if err := store.Close(); err != nil {
		t.Fatalf("close resumed store: %v", err)
	}

	version, err := SchemaVersion(ctx, dbPath)
	if err != nil {
		t.Fatalf("schema version: %v", err)
	}
	if version != 5 {
		t.Fatalf("expected resumed schema version 5, got %d", version)
	}
}

func preparePartialMigrations(ctx context.Context, dbPath string, count int) ([]MigrationStatus, error) {
	sqlDB, err := sql.Open("sqlite", dbPath)
	if err != nil {
		return nil, err
	}
	defer func() { _ = sqlDB.Close() }()
	if err := ensureMigrationTable(ctx, sqlDB); err != nil {
		return nil, err
	}
	migrations, err := loadMigrations()
	if err != nil {
		return nil, err
	}
	for _, item := range migrations[:count] {
		if _, err := sqlDB.ExecContext(ctx, item.SQL); err != nil {
			return nil, err
		}
		if _, err := sqlDB.ExecContext(ctx, recordMigrationSQL, item.Version); err != nil {
			return nil, err
		}
	}
	return collectMigrationStatus(ctx, sqlDB)
}

func assertMigrationStates(t *testing.T, status []MigrationStatus, applied int) {
	t.Helper()
	for index, item := range status {
		want := "pending"
		if index < applied {
			want = "up"
		}
		if item.State != want {
			t.Fatalf("migration %d: expected state %q, got %#v", item.Version, want, item)
		}
	}
}
