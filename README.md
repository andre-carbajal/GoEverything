# GoEverything

Fast local file indexing and search for macOS and Windows (Everything-style), built with Go.

> **Platform status:** GoEverything supports macOS and Windows. Windows scans use an NTFS metadata backend when
> available and automatically fall back to a portable filesystem walk.

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

**Windows (PowerShell)**

```powershell
iwr https://raw.githubusercontent.com/andre-carbajal/GoEverything/main/scripts/install.ps1 -UseB | iex
```

You can also install a specific version:

- macOS: `bash -s -- v0.1.0`
- Windows: `.\install.ps1 -Version v0.1.0`

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

- `↑` / `↓` move through search results
- `Enter` open selected result
- `Ctrl+D` / `Delete` remove selected result
- Click select, double-click open, right-click delete
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

Windows PowerShell:

```powershell
ge scan --root $env:USERPROFILE
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

Windows PowerShell:

```powershell
ge scan --root $env:USERPROFILE
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

Windows supports foreground watch:

```powershell
ge watch --root $env:USERPROFILE
```

Persistent `watch install/start/stop/status/logs` commands are currently macOS-only.

### Rebuild search index (without rescanning)

```bash
ge reindex
```

---

## Configuration

Default paths:

- macOS/Linux config: `~/.config/ge/config.json`
- macOS/Linux database: `~/.config/ge/goeverything.db`
- Windows config: `%LOCALAPPDATA%\ge\config.json`
- Windows database: `%LOCALAPPDATA%\ge\goeverything.db`

Example `~/.config/ge/config.json`:

```json
{
  "db_path": "~/.config/ge/goeverything.db",
  "default_scan_path": "~",
  "theme": "tokyonight",
  "delete_mode": "trash",
  "excludes": [
    ".git",
    "node_modules",
    "Library/Caches/*"
  ]
}
```

Notes:

- `db_path`: SQLite database location
- `default_scan_path`: default root when you do not pass `--root`
- `delete_mode`: `trash` or `permanent` for deletes from the TUI
- `excludes`: ignore names or root-relative glob patterns

---

## Troubleshooting

### No results after scan

- Re-run scan on expected root: `ge scan --root "$HOME"`
- Search with broader query: `ge search -q a --limit 200`
- Confirm DB path if using custom `--db`

### Permission issues

- Some directories require extra platform permissions.
- Start with folders you own (`$HOME`) and expand gradually.
- On Windows, the NTFS backend may require an elevated terminal for full-volume scans. In `--backend auto` mode,
  GoEverything falls back to the portable walker if NTFS metadata access is unavailable.

### Scan feels slow

- Tune workers and batch size:

```bash
ge scan --root "$HOME" --workers 16 --batch 3000
```

- Add excludes for heavy folders (`node_modules`, caches, build outputs).
- On Windows, use `--backend ntfs` to require NTFS metadata scanning, `--backend walk` to force the portable scanner, or
  the default `--backend auto` to try NTFS first and fall back automatically.

---

## Advanced options (short)

### Scan tuning

- `--workers`: concurrent index workers
- `--batch`: DB upsert batch size (default: 2000)
- `--exclude`: skip names or root-relative globs
- `--all-roots`: scan default platform roots (`/`, `/Volumes/*`, or Windows drive roots like `C:\`)

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
