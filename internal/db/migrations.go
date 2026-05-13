package db

import (
	"context"
	"database/sql"
	"embed"
	"fmt"
	"io/fs"
	"path/filepath"
	"strings"
	"time"

	"github.com/pressly/goose/v3"
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

func applyMigrations(ctx context.Context, sqlDB *sql.DB) error {
	provider, err := gooseProvider(sqlDB)
	if err != nil {
		return err
	}
	_, err = provider.Up(ctx)
	return err
}

func currentSchemaVersion(ctx context.Context, sqlDB *sql.DB) (int64, error) {
	provider, err := gooseProvider(sqlDB)
	if err != nil {
		return 0, err
	}
	return provider.GetDBVersion(ctx)
}

func collectMigrationStatus(ctx context.Context, sqlDB *sql.DB) ([]MigrationStatus, error) {
	provider, err := gooseProvider(sqlDB)
	if err != nil {
		return nil, err
	}

	items, err := provider.Status(ctx)
	if err != nil {
		return nil, err
	}

	out := make([]MigrationStatus, 0, len(items))
	for _, item := range items {
		name := ""
		version := int64(0)
		if item.Source != nil {
			version = item.Source.Version
			name = filepath.Base(item.Source.Path)
		}
		out = append(out, MigrationStatus{
			Version:   version,
			Name:      name,
			State:     strings.ToLower(string(item.State)),
			AppliedAt: item.AppliedAt,
		})
	}
	return out, nil
}

func gooseProvider(sqlDB *sql.DB) (*goose.Provider, error) {
	migrationFS, err := fs.Sub(embeddedMigrations, "migrations")
	if err != nil {
		return nil, fmt.Errorf("cannot open embedded migrations: %w", err)
	}

	provider, err := goose.NewProvider(
		goose.DialectSQLite3,
		sqlDB,
		migrationFS,
		goose.WithTableName(migrationsTableName),
	)
	if err != nil {
		return nil, fmt.Errorf("cannot initialize goose provider: %w", err)
	}
	return provider, nil
}

func Migrate(ctx context.Context, dbPath string) (int64, error) {
	store, err := openStore(ctx, dbPath)
	if err != nil {
		return 0, err
	}
	defer store.Close()

	if err := store.setup(ctx); err != nil {
		return 0, err
	}

	return currentSchemaVersion(ctx, store.db)
}

func SchemaVersion(ctx context.Context, dbPath string) (int64, error) {
	store, err := openStore(ctx, dbPath)
	if err != nil {
		return 0, err
	}
	defer store.Close()

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
	defer store.Close()

	if err := store.applyPragmas(ctx); err != nil {
		return nil, err
	}
	return collectMigrationStatus(ctx, store.db)
}
