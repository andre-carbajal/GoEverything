# GoEverything

Fast local file indexing and search for macOS (Everything-style), built with Go.

## Current Status

- Single binary mode:
  - `ge` (no args) opens interactive TUI
  - `ge <command>` runs CLI mode
- SQLite + FTS5 (prefix-enabled) index with WAL enabled
- Concurrent filesystem scanner using fastwalk
- macOS FSEvents watcher integration
- launchd watch service management commands

## Quick Start

```bash
go run ./cmd/ge
go run ./cmd/ge roots
go run ./cmd/ge scan --root /Users
go run ./cmd/ge reindex
go run ./cmd/ge search -q safari --format table
go run ./cmd/ge search -q "*report*" --format json
go run ./cmd/ge watch --root /Users
go run ./cmd/ge watch install --root /Users
go run ./cmd/ge watch start
```

## Scan Tuning

- `--workers`: concurrent index workers (default: auto)
- `--batch`: DB upsert batch size (default: 2000)
- `--exclude`: skip patterns by name or root-relative glob
- `--all-roots`: scan `/` and `/Volumes/*`

Example:

```bash
go run ./cmd/cli scan \
  --all-roots \
  --workers 16 \
  --batch 3000 \
  --exclude .git \
  --exclude node_modules \
  --exclude "Library/Caches/*" \
  --db "$HOME/.config/ge/goeverything.db"
```

## Search Filters

- `--ext go`
- `--root /Users`
- `--only-files` / `--only-dirs`
- `--format table|json`
