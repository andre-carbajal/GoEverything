# GoEverything

Fast local file indexing and search for macOS, Windows, and Linux (Everything-style), built with Go.

> **Platform status:** GoEverything officially supports macOS, Windows, and Linux. Windows scans use an NTFS metadata
> backend when available and automatically fall back to a portable filesystem walk. Linux uses a portable filesystem
> walk and discovers accessible mounts while excluding virtual filesystems.

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

**Linux**

```bash
curl -fsSL https://raw.githubusercontent.com/andre-carbajal/GoEverything/main/scripts/install.sh | bash
```

Linux packages (`.deb` and `.rpm`) are also published with each release. The optional desktop actions use `xdg-open`
or `gio`; moving files to the trash uses `gio` or `trash-put` from `trash-cli`.

**Windows (PowerShell)**

```powershell
iwr https://raw.githubusercontent.com/andre-carbajal/GoEverything/main/scripts/install.ps1 -UseB | iex
```

You can also install a specific version:

- macOS: `bash -s -- v0.1.0`
- Windows: `.\install.ps1 -Version v0.1.0`

### Option 2: Build locally

```bash
mkdir -p ./bin
go build -o ./bin/ge ./cmd/ge
mv ./bin/ge /usr/local/bin/ge
ge --help
```

### Option 3: Package manager (official)

**Homebrew**

```bash
brew tap andre-carbajal/tap
brew install goeverything
```

**Scoop**

```powershell
scoop bucket add andre-carbajal https://github.com/andre-carbajal/scoop-bucket
scoop install andre-carbajal/goeverything
```

---

## Quick start (5 mins)

### 1) Open the TUI

```bash
ge
```

The TUI opens a location picker first. The input is focused immediately: choose Home, System, a mounted volume/drive, or
type a folder path. Suggestions are completed one directory level at a time. After confirming, the selected location is
scanned and the progress panel is shown before search opens. The selected location is not persisted. If the scan fails
or is canceled, the picker is shown again.

Useful shortcuts:

- `↑` / `↓` move through search results
- In the location picker, `↑` / `↓` select suggestions and `Tab` / `→` accept completion
- In the location picker, `Enter` scans the selected root/path and `Esc` cancels
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
```

Windows supports foreground watch:

```powershell
ge watch --root $env:USERPROFILE
```

The watcher stays in the foreground. If a persistent watcher is needed, manage it with the native service tooling for
the target operating system.

### Rebuild search index (without rescanning)

```bash
ge reindex
```

---

## Configuration

Portable data paths:

- Config and database are stored below `os.UserConfigDir()/ge` for the current platform.
- Existing legacy files under `~/.config/ge` (and the previous Windows locations) are migrated on load.

Example `~/.config/ge/config.json`:

```json
{
  "db_path": "~/.config/ge/goeverything.db",
  "theme": "tokyonight",
  "delete_mode": "trash",
  "last_search": "report",
  "excludes": [
    ".git",
    "node_modules",
    "Library/Caches/*"
  ]
}
```

Notes:

- `db_path`: SQLite database location
- Scan location: selected per TUI execution and never persisted as a default
- `last_search`: last non-empty TUI query, displayed as the next search suggestion
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
- On Linux, check Unix permissions, ACLs, and mount options when a directory is inaccessible. Virtual filesystems are
  excluded automatically when scanning the system root.
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
- `--all-roots`: scan default platform roots (`/`, Linux data mounts, `/Volumes/*`, or Windows drive roots like `C:\`)

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
ge db status
ge db version
```

Migrations run automatically when the database is opened. Existing databases using the compatible
`goose_db_version` table are resumed without destructive resets; rollback is not supported.

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
