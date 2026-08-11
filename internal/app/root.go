package app

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/spf13/cobra"

	"goeverything/internal/config"
	"goeverything/internal/db"
	"goeverything/internal/scanner"
	"goeverything/internal/tui"
	"goeverything/internal/watcher"
)

// Version identifies development builds. GoReleaser replaces it with the tag
// version so release builds report the exact version they came from.
var Version = "dev"

type options struct {
	DBPath  string
	Root    string
	Roots   bool
	Query   string
	Limit   int
	Offset  int
	Batch   int
	Workers int
	Exclude []string
	Backend string

	SearchFormat string
	SearchExt    string
	SearchRoot   string
	OnlyFiles    bool
	OnlyDirs     bool
}

func NewRootCommand() *cobra.Command {
	opt := options{}
	cfg := config.Config{}

	cmd := &cobra.Command{
		Use:     "ge",
		Short:   "Fast local file index/search",
		Version: Version,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return tui.Run(cmd.Context(), cfg)
		},
		PersistentPreRunE: func(_ *cobra.Command, _ []string) error {
			return prepareOptions(&opt, &cfg)
		},
	}

	cmd.PersistentFlags().StringVar(&opt.DBPath, "db", "", "Path to SQLite database")

	cmd.AddCommand(newScanCommand(&opt))
	cmd.AddCommand(newReindexCommand(&opt))
	cmd.AddCommand(newSearchCommand(&opt))
	cmd.AddCommand(newDBCommand(&opt))
	cmd.AddCommand(newWatchCommand(&opt))
	cmd.AddCommand(newRootsCommand())

	return cmd
}

func prepareOptions(opt *options, cfg *config.Config) error {
	if err := loadOptionsConfig(cfg); err != nil {
		return err
	}
	if err := resolveDBPath(opt, *cfg); err != nil {
		return err
	}
	applyOptionDefaults(opt, *cfg)
	return ensureDBDirectory(opt, cfg)
}

func loadOptionsConfig(cfg *config.Config) error {
	loaded, err := config.Load()
	if err == nil {
		*cfg = loaded
		return nil
	}
	cfgPath, _ := config.Path()
	return fmt.Errorf("cannot load config at %q: %w\nhint: ensure %s is writable", cfgPath, err, filepath.Dir(cfgPath))
}

func resolveDBPath(opt *options, cfg config.Config) error {
	if opt.DBPath != "" {
		return nil
	}
	if cfg.DBPath != "" {
		resolved, err := config.ExpandPath(cfg.DBPath)
		if err != nil {
			return err
		}
		opt.DBPath = resolved
		return nil
	}
	dbPath, err := config.Path()
	if err != nil {
		return err
	}
	opt.DBPath = filepath.Join(filepath.Dir(dbPath), "goeverything.db")
	return nil
}

func applyOptionDefaults(opt *options, cfg config.Config) {
	if opt.Batch <= 0 {
		opt.Batch = 2000
	}
	if opt.Workers <= 0 {
		opt.Workers = scanner.DefaultWorkerCount()
	}
	if len(opt.Exclude) == 0 {
		opt.Exclude = cfg.Excludes
		if len(opt.Exclude) == 0 {
			opt.Exclude = scanner.DefaultExcludes()
		}
	}
}

func ensureDBDirectory(opt *options, cfg *config.Config) error {
	if err := os.MkdirAll(filepath.Dir(opt.DBPath), 0o755); err != nil {
		return fmt.Errorf("cannot create db dir %q: %w\nhint: ensure the configured data directory is writable or pass --db", filepath.Dir(opt.DBPath), err)
	}
	cfg.DBPath, cfg.Excludes = opt.DBPath, opt.Exclude
	return nil
}

func newScanCommand(opt *options) *cobra.Command {
	command := &cobra.Command{
		Use:   "scan",
		Short: "Scan filesystem roots and update index",
		RunE: func(cmd *cobra.Command, _ []string) error {
			store, err := db.Open(cmd.Context(), opt.DBPath)
			if err != nil {
				return err
			}
			defer func() { _ = store.Close() }()

			roots := []string{opt.Root}
			if opt.Roots {
				roots = scanner.DiscoverRoots()
			} else if strings.TrimSpace(opt.Root) == "" {
				roots = []string{"~"}
			}
			resolvedRoots := make([]string, 0, len(roots))
			for _, root := range roots {
				resolved, resolveErr := config.ExpandPath(root)
				if resolveErr != nil {
					return resolveErr
				}
				resolvedRoots = append(resolvedRoots, resolved)
			}
			if opt.Roots {
				resolvedRoots = scanner.DeduplicateRoots(resolvedRoots)
			}

			r := scanner.Runner{
				Store:   store,
				Workers: opt.Workers,
				Batch:   opt.Batch,
				Exclude: opt.Exclude,
				Backend: opt.Backend,
				Warning: func(message string) {
					_, _ = fmt.Fprintf(os.Stderr, "warning: %s\n", message)
				},
			}
			metrics, err := r.Scan(cmd.Context(), resolvedRoots)
			if err != nil {
				return watcher.WithPermissionHint(err)
			}

			fmt.Printf("scanned=%d indexed=%d skipped=%d elapsed=%s files_per_sec=%.2f\n",
				metrics.Scanned,
				metrics.Indexed,
				metrics.Skipped,
				metrics.Elapsed,
				metrics.FilesPerSecond,
			)
			return nil
		},
	}
	command.Flags().StringVar(&opt.Root, "root", "", "Filesystem root to scan (default: home directory)")
	command.Flags().BoolVar(&opt.Roots, "all-roots", false, "Scan default platform roots")
	command.Flags().IntVar(&opt.Workers, "workers", scanner.DefaultWorkerCount(), "Concurrent index workers")
	command.Flags().IntVar(&opt.Batch, "batch", 2000, "Batch size for DB upserts")
	command.Flags().StringSliceVar(&opt.Exclude, "exclude", nil, "Exclude patterns (name or relative glob)")
	command.Flags().StringVar(&opt.Backend, "backend", scanner.BackendAuto, "Scan backend: auto|ntfs|walk")
	command.PreRunE = func(_ *cobra.Command, _ []string) error {
		return scanner.ValidateBackend(opt.Backend)
	}
	return command
}

func newReindexCommand(opt *options) *cobra.Command {
	return &cobra.Command{
		Use:   "reindex",
		Short: "Rebuild the FTS index without rescanning the filesystem",
		RunE: func(cmd *cobra.Command, _ []string) error {
			store, err := db.Open(cmd.Context(), opt.DBPath)
			if err != nil {
				return err
			}
			defer func() { _ = store.Close() }()

			if err := store.ReindexFTS(cmd.Context()); err != nil {
				return err
			}
			total, err := store.Count(cmd.Context())
			if err != nil {
				return err
			}
			fmt.Printf("fts_reindexed entries=%d\n", total)
			return nil
		},
	}
}

func newSearchCommand(opt *options) *cobra.Command {
	command := &cobra.Command{
		Use:   "search",
		Short: "Search indexed files",
		RunE: func(cmd *cobra.Command, _ []string) error {
			store, err := db.Open(cmd.Context(), opt.DBPath)
			if err != nil {
				return err
			}
			defer func() { _ = store.Close() }()

			searchOpts := db.SearchOptions{
				Query:     opt.Query,
				Limit:     opt.Limit,
				Offset:    opt.Offset,
				OnlyDirs:  opt.OnlyDirs,
				OnlyFiles: opt.OnlyFiles,
				Ext:       opt.SearchExt,
				Root:      opt.SearchRoot,
			}

			results, err := store.SearchAdvanced(cmd.Context(), searchOpts)
			if err != nil {
				return err
			}

			switch strings.ToLower(strings.TrimSpace(opt.SearchFormat)) {
			case "", "table":
				writeTable(os.Stdout, results)
				return nil
			case "json":
				return writeJSON(os.Stdout, results)
			default:
				return fmt.Errorf("unsupported format %q (allowed: table,json)", opt.SearchFormat)
			}
		},
	}

	command.Flags().StringVarP(&opt.Query, "query", "q", "", "Search query")
	command.Flags().IntVar(&opt.Limit, "limit", 50, "Maximum results")
	command.Flags().IntVar(&opt.Offset, "offset", 0, "Result offset")
	command.Flags().StringVar(&opt.SearchFormat, "format", "table", "Output format: table|json")
	command.Flags().StringVar(&opt.SearchExt, "ext", "", "Filter by extension (example: go or .go)")
	command.Flags().StringVar(&opt.SearchRoot, "root", "", "Filter results by indexed root")
	command.Flags().BoolVar(&opt.OnlyFiles, "only-files", false, "Return only files")
	command.Flags().BoolVar(&opt.OnlyDirs, "only-dirs", false, "Return only directories")
	command.MarkFlagsMutuallyExclusive("only-files", "only-dirs")
	command.PreRunE = func(_ *cobra.Command, _ []string) error {
		if strings.TrimSpace(opt.Query) == "" {
			return fmt.Errorf("at least one search term is required (--query)")
		}
		return nil
	}
	return command
}

func newWatchCommand(opt *options) *cobra.Command {
	command := &cobra.Command{
		Use:   "watch",
		Short: "Watch indexed root for real-time updates",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			store, err := db.Open(cmd.Context(), opt.DBPath)
			if err != nil {
				return err
			}
			defer func() { _ = store.Close() }()

			root := strings.TrimSpace(opt.Root)
			if root == "" {
				root = "~"
			}
			root, err = config.ExpandPath(root)
			if err != nil {
				return err
			}

			w := watcher.New(store, opt.Exclude...)
			if err := w.Run(cmd.Context(), root); err != nil {
				return watcher.WithPermissionHint(err)
			}
			return nil
		},
	}
	command.PersistentFlags().StringVar(&opt.Root, "root", "", "Filesystem root to watch (default: home directory)")

	return command
}

func newRootsCommand() *cobra.Command {
	command := &cobra.Command{
		Use:   "roots",
		Short: "List default scan roots",
		Run: func(_ *cobra.Command, _ []string) {
			for _, root := range scanner.DiscoverRoots() {
				fmt.Println(root)
			}
		},
	}
	return command
}

func newDBCommand(opt *options) *cobra.Command {
	command := &cobra.Command{
		Use:   "db",
		Short: "Database migration and schema commands",
	}

	command.AddCommand(&cobra.Command{
		Use:   "version",
		Short: "Show current DB migration version",
		RunE: func(cmd *cobra.Command, _ []string) error {
			version, err := db.SchemaVersion(cmd.Context(), opt.DBPath)
			if err != nil {
				return err
			}
			fmt.Printf("current_version=%d\n", version)
			return nil
		},
	})

	command.AddCommand(&cobra.Command{
		Use:   "status",
		Short: "Show migration status table",
		RunE: func(cmd *cobra.Command, _ []string) error {
			statuses, err := db.SchemaStatus(cmd.Context(), opt.DBPath)
			if err != nil {
				return err
			}
			w := tabwriter.NewWriter(os.Stdout, 0, 4, 2, ' ', 0)
			_, _ = fmt.Fprintln(w, "VERSION\tSTATE\tAPPLIED_AT\tFILE")
			for _, s := range statuses {
				appliedAt := "-"
				if !s.AppliedAt.IsZero() {
					appliedAt = s.AppliedAt.Format(time.RFC3339)
				}
				_, _ = fmt.Fprintf(w, "%d\t%s\t%s\t%s\n", s.Version, s.State, appliedAt, s.Name)
			}
			_ = w.Flush()
			return nil
		},
	})

	return command
}

func writeJSON(w io.Writer, entries []db.Entry) error {
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	return enc.Encode(entries)
}

func writeTable(dst io.Writer, entries []db.Entry) {
	w := tabwriter.NewWriter(dst, 0, 4, 2, ' ', 0)
	_, _ = fmt.Fprintln(w, "NAME\tPATH\tSIZE\tEXT\tTYPE")
	for _, entry := range entries {
		kind := "file"
		if entry.IsDir {
			kind = "dir"
		}
		_, _ = fmt.Fprintf(w, "%s\t%s\t%d\t%s\t%s\n", entry.Name, entry.Path, entry.Size, entry.Ext, kind)
	}
	_ = w.Flush()
}
