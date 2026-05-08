# GoEverything

Fast local file indexing and search for macOS (Everything-style), built with Go.

## Current Status

- CLI scan/search/watch implemented
- SQLite + FTS5 index with WAL enabled
- Concurrent filesystem scanner using fastwalk
- macOS FSEvents watcher integration

## Quick Start

```bash
go run ./cmd/cli roots
go run ./cmd/cli scan --root /Users --db ./goeverything.db
go run ./cmd/cli search -q safari --db ./goeverything.db
go run ./cmd/cli search -q "*report*" --db ./goeverything.db
go run ./cmd/cli watch --root /Users --db ./goeverything.db
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
  --db ./goeverything.db
```
