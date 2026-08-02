package db

import (
	"context"
	"database/sql"
	"path/filepath"
	"testing"

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

	sqlDB, err := sql.Open("sqlite", dbPath)
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	if err := ensureMigrationTable(ctx, sqlDB); err != nil {
		t.Fatalf("create goose table: %v", err)
	}
	migrations, err := loadMigrations()
	if err != nil {
		t.Fatalf("load migrations: %v", err)
	}
	for _, item := range migrations[:3] {
		if _, err := sqlDB.ExecContext(ctx, item.SQL); err != nil {
			t.Fatalf("apply legacy migration %s: %v", item.Name, err)
		}
		if _, err := sqlDB.ExecContext(ctx, `
			INSERT INTO goose_db_version(version_id, is_applied)
			VALUES (?, 1)`, item.Version); err != nil {
			t.Fatalf("record legacy migration %s: %v", item.Name, err)
		}
	}
	status, err := collectMigrationStatus(ctx, sqlDB)
	if err != nil {
		t.Fatalf("collect partial status: %v", err)
	}
	for index, item := range status {
		want := "pending"
		if index < 3 {
			want = "up"
		}
		if item.State != want {
			t.Fatalf("migration %d: expected state %q, got %#v", item.Version, want, item)
		}
	}
	if err := sqlDB.Close(); err != nil {
		t.Fatalf("close sqlite: %v", err)
	}

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
