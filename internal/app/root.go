package app

import (
	"fmt"
	"path/filepath"
	"strings"

	"github.com/spf13/cobra"

	"goeverything/internal/db"
	"goeverything/internal/scanner"
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
}

func NewRootCommand() *cobra.Command {
	opt := options{}

	cmd := &cobra.Command{
		Use:   "goeverything",
		Short: "Fast local file index/search for macOS",
		PersistentPreRunE: func(_ *cobra.Command, _ []string) error {
			if opt.DBPath == "" {
				opt.DBPath = filepath.Join(".", "goeverything.db")
			}
			if opt.Batch <= 0 {
				opt.Batch = 2000
			}
			if opt.Workers <= 0 {
				opt.Workers = scanner.DefaultWorkerCount()
			}
			if len(opt.Exclude) == 0 {
				opt.Exclude = scanner.DefaultExcludes()
			}
			return nil
		},
	}

	cmd.PersistentFlags().StringVar(&opt.DBPath, "db", "", "Path to SQLite database")

	cmd.AddCommand(newScanCommand(&opt))
	cmd.AddCommand(newSearchCommand(&opt))
	cmd.AddCommand(newWatchCommand(&opt))
	cmd.AddCommand(newRootsCommand())

	return cmd
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
			defer store.Close()

			roots := []string{opt.Root}
			if opt.Roots {
				roots = scanner.DiscoverRoots()
			} else if strings.TrimSpace(opt.Root) == "" {
				roots = []string{"/"}
			}

			r := scanner.Runner{
				Indexer: store,
				Workers: opt.Workers,
				Batch:   opt.Batch,
				Exclude: opt.Exclude,
			}
			metrics, err := r.Scan(cmd.Context(), roots)
			if err != nil {
				return err
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
	command.Flags().StringVar(&opt.Root, "root", "/", "Filesystem root to scan")
	command.Flags().BoolVar(&opt.Roots, "all-roots", false, "Scan default roots (/, /Volumes/*)")
	command.Flags().IntVar(&opt.Workers, "workers", scanner.DefaultWorkerCount(), "Concurrent index workers")
	command.Flags().IntVar(&opt.Batch, "batch", 2000, "Batch size for DB upserts")
	command.Flags().StringSliceVar(&opt.Exclude, "exclude", scanner.DefaultExcludes(), "Exclude patterns (name or relative glob)")
	return command
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

			results, err := store.Search(cmd.Context(), opt.Query, opt.Limit, opt.Offset)
			if err != nil {
				return err
			}

			for _, entry := range results {
				fmt.Printf("%s\t%s\t%d\n", entry.Name, entry.Path, entry.Size)
			}
			return nil
		},
	}

	command.Flags().StringVarP(&opt.Query, "query", "q", "", "Search query")
	command.Flags().IntVar(&opt.Limit, "limit", 50, "Maximum results")
	command.Flags().IntVar(&opt.Offset, "offset", 0, "Result offset")
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
			return w.Run(cmd.Context(), root)
		},
	}
	command.Flags().StringVar(&opt.Root, "root", "/", "Filesystem root to watch")
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
