package app

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"text/tabwriter"

	"github.com/spf13/cobra"

	"goeverything/internal/config"
	"goeverything/internal/db"
	"goeverything/internal/scanner"
	"goeverything/internal/tui"
	"goeverything/internal/watcher"
)

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
		Use:   "ge",
		Short: "Fast local file index/search for macOS",
		RunE: func(cmd *cobra.Command, _ []string) error {
			return tui.Run(cmd.Context(), cfg)
		},
		PersistentPreRunE: func(_ *cobra.Command, _ []string) error {
			loaded, err := config.Load()
			if err != nil {
				cfgPath, _ := config.Path()
				return fmt.Errorf("cannot load config at %q: %w\nhint: ensure %s is writable", cfgPath, err, filepath.Dir(cfgPath))
			}
			cfg = loaded

			if opt.DBPath == "" {
				if cfg.DBPath != "" {
					opt.DBPath = cfg.DBPath
				} else {
					dbPath, dbErr := config.ExpandPath("~/.config/ge/goeverything.db")
					if dbErr != nil {
						return dbErr
					}
					opt.DBPath = dbPath
				}
			}
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
			if err := os.MkdirAll(filepath.Dir(opt.DBPath), 0o755); err != nil {
				return fmt.Errorf("cannot create db dir %q: %w\nhint: ensure ~/.config/ge is writable or pass --db", filepath.Dir(opt.DBPath), err)
			}
			cfg.DBPath = opt.DBPath
			cfg.Excludes = opt.Exclude
			return nil
		},
	}

	cmd.PersistentFlags().StringVar(&opt.DBPath, "db", "", "Path to SQLite database")

	cmd.AddCommand(newScanCommand(&opt, &cfg))
	cmd.AddCommand(newReindexCommand(&opt))
	cmd.AddCommand(newSearchCommand(&opt))
	cmd.AddCommand(newWatchCommand(&opt))
	cmd.AddCommand(newRootsCommand())

	return cmd
}

func newScanCommand(opt *options, cfg *config.Config) *cobra.Command {
	command := &cobra.Command{
		Use:   "scan",
		Short: "Scan filesystem roots and update index",
		RunE: func(cmd *cobra.Command, _ []string) error {
			store, err := db.Open(cmd.Context(), opt.DBPath)
			if err != nil {
				return err
			}
			defer store.Close()

			roots := []string{opt.Root}
			if opt.Roots {
				roots = scanner.DiscoverRoots()
			} else if strings.TrimSpace(opt.Root) == "" {
				if len(cfg.Roots) > 0 {
					roots = cfg.Roots
				} else {
					roots = []string{"~"}
				}
			}
			resolvedRoots, err := config.ResolveRoots(roots)
			if err != nil {
				return err
			}

			r := scanner.Runner{
				Indexer: store,
				Workers: opt.Workers,
				Batch:   opt.Batch,
				Exclude: opt.Exclude,
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
	command.Flags().StringVar(&opt.Root, "root", "", "Filesystem root to scan (default: roots from config)")
	command.Flags().BoolVar(&opt.Roots, "all-roots", false, "Scan default roots (/, /Volumes/*)")
	command.Flags().IntVar(&opt.Workers, "workers", scanner.DefaultWorkerCount(), "Concurrent index workers")
	command.Flags().IntVar(&opt.Batch, "batch", 2000, "Batch size for DB upserts")
	command.Flags().StringSliceVar(&opt.Exclude, "exclude", nil, "Exclude patterns (name or relative glob)")
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
			defer store.Close()

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
			defer store.Close()

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
	_ = command.MarkFlagRequired("query")
	return command
}

func newWatchCommand(opt *options) *cobra.Command {
	command := &cobra.Command{
		Use:   "watch",
		Short: "Watch indexed root for real-time updates",
		RunE: func(cmd *cobra.Command, _ []string) error {
			store, err := db.Open(cmd.Context(), opt.DBPath)
			if err != nil {
				return err
			}
			defer store.Close()

			root := opt.Root
			if root == "" {
				root = "/"
			}

			w := watcher.New(store)
			if err := w.Run(cmd.Context(), root); err != nil {
				return watcher.WithPermissionHint(err)
			}
			return nil
		},
	}
	command.Flags().StringVar(&opt.Root, "root", "/", "Filesystem root to watch")

	command.AddCommand(newWatchInstallCommand(opt))
	command.AddCommand(newWatchUninstallCommand())
	command.AddCommand(newWatchStartCommand())
	command.AddCommand(newWatchStopCommand())
	command.AddCommand(newWatchRestartCommand())
	command.AddCommand(newWatchStatusCommand())
	command.AddCommand(newWatchLogsCommand())

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
