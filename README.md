# GoEverything

Fast local file indexing and search for macOS (Everything-style), built with Go.

> **Platform status:** GoEverything is currently **macOS-only** (official support).

## What is GoEverything?

GoEverything (`ge`) helps you quickly find files and folders using a local SQLite + FTS5 index.

Use it when you want to:
- Find files by name or partial text
- Filter by extension or root path
- Keep results updated with a watcher
- Use an interactive TUI for day-to-day search

---

## Install

### Option 1: Install from release script (recommended)

**macOS**
```bash
curl -fsSL https://raw.githubusercontent.com/andre-carbajal/GoEverything/main/scripts/install.sh | bash
```

You can also install a specific version:
- macOS: `bash -s -- v0.1.0`

### Option 2: Build locally

```bash
go build -o ge ./cmd/ge
mv ./ge /usr/local/bin/ge
ge --help
```

### Option 3: Package manager (official)

**Homebrew**
```bash
brew tap andre-carbajal/tap
brew install goeverything
```

---

## Quick start (5 mins)

### 1) Open the TUI

```bash
ge
```

The TUI scans your configured location first, then opens the search screen.
If the startup scan fails or is canceled, it opens settings so you can adjust the scan location or excludes.

Useful shortcuts:
- `Space` show/hide startup scan progress
- `/` focus search input
- `Ctrl+S` open settings
- `Ctrl+G` scan now
- `Ctrl+X` stop scan
- `Ctrl+Q` quit

### 2) Check default roots

```bash
ge roots
```

### 3) Index your home folder

```bash
ge scan --root "$HOME"
```

### 4) Search from CLI

```bash
ge search -q safari
ge search -q "*report*" --format json
```

---

## Common workflows

### Index my home folder

```bash
ge scan --root "$HOME"
```

### Find files by name/ext

```bash
# By name/query
ge search -q invoice

# By extension
ge search -q report --ext pdf

# Only files or only directories
ge search -q docs --only-files
ge search -q src --only-dirs
```

### Keep index updated with watch

```bash
# One-shot watch on a root
ge watch --root "$HOME"

# install/start persistent launchd watcher
ge watch install --root "$HOME"
ge watch start
```

### Rebuild search index (without rescanning)

```bash
ge reindex
```

---

## Configuration

Default paths:
- Config: `~/.config/ge/config.json`
- Database: `~/.config/ge/goeverything.db`

Example `~/.config/ge/config.json`:

```json
{
  "default_scan_path": "~",
  "auto_scan_on_start": true,
  "theme": "tokyonight",
  "excludes": [".git", "node_modules", "Library/Caches/*"]
}
```

Notes:
- `default_scan_path`: default root when you do not pass `--root`
- `auto_scan_on_start`: legacy field; the TUI now always runs its startup scan before opening search
- `excludes`: ignore names or root-relative glob patterns

---

## Troubleshooting

### No results after scan
- Re-run scan on expected root: `ge scan --root "$HOME"`
- Search with broader query: `ge search -q a --limit 200`
- Confirm DB path if using custom `--db`

### Permission issues
- Some directories require extra macOS permissions.
- Start with folders you own (`$HOME`) and expand gradually.

### Scan feels slow
- Tune workers and batch size:

```bash
ge scan --root "$HOME" --workers 16 --batch 3000
```

- Add excludes for heavy folders (`node_modules`, caches, build outputs).

---

## Advanced options (short)

### Scan tuning
- `--workers`: concurrent index workers
- `--batch`: DB upsert batch size (default: 2000)
- `--exclude`: skip names or root-relative globs
- `--all-roots`: scan default roots (`/`, `/Volumes/*`)

Example:

```bash
ge scan \
  --all-roots \
  --workers 16 \
  --batch 3000 \
  --exclude .git \
  --exclude node_modules \
  --exclude "Library/Caches/*"
```

### Database migrations

```bash
ge db migrate
ge db status
ge db version
```

---

## Command reference

For full command details:

```bash
ge --help
ge <command> --help
```

Examples:

```bash
ge scan --help
ge search --help
ge watch --help
```
