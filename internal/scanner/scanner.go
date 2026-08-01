package scanner

import (
	"context"
	"errors"
	"io/fs"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/charlievieth/fastwalk"

	"goeverything/internal/db"
)

type Indexer interface {
	UpsertBatch(ctx context.Context, entries []db.Entry) error
}

type DirectorySizeUpdater interface {
	UpdateDirectorySizes(ctx context.Context, sizes map[string]int64) error
}

type Reconciler interface {
	BeginScan(ctx context.Context, roots []string) (int64, error)
	MarkSeenBatch(ctx context.Context, sessionID int64, entries []db.Entry) error
	MarkUnreadablePrefix(ctx context.Context, sessionID int64, path string) error
	FinishScan(ctx context.Context, sessionID int64, roots []string) error
	AbortScan(ctx context.Context, sessionID int64) error
}

type Runner struct {
	Indexer Indexer
	Workers int
	Batch   int
	Exclude []string
	Backend string

	Progress func(Progress)
	Warning  func(string)
}

type Metrics struct {
	Scanned        int64
	Indexed        int64
	Skipped        int64
	Elapsed        time.Duration
	FilesPerSecond float64
}

type Progress struct {
	Scanned        int64
	Indexed        int64
	Skipped        int64
	Elapsed        time.Duration
	FilesPerSecond float64
	CurrentPath    string
}

const (
	BackendAuto = "auto"
	BackendWalk = "walk"
	BackendNTFS = "ntfs"
)

type scanBackend interface {
	Scan(ctx context.Context, roots []string, emit func(db.Entry) error, progress scanProgress) error
}

type scanProgress struct {
	Scanned     *int64
	Skipped     *int64
	CurrentPath *atomic.Value
	Emit        func()
	Protect     func(string)
}

type directorySizeAccumulator struct {
	mu    sync.Mutex
	sizes map[string]int64
}

func newDirectorySizeAccumulator() *directorySizeAccumulator {
	return &directorySizeAccumulator{sizes: make(map[string]int64)}
}

func (a *directorySizeAccumulator) add(entry db.Entry) {
	if a == nil {
		return
	}

	root := filepath.Clean(strings.TrimSpace(entry.Root))
	path := filepath.Clean(strings.TrimSpace(entry.Path))
	if root == "." || path == "." || root == "" || path == "" {
		return
	}

	a.mu.Lock()
	defer a.mu.Unlock()
	if entry.IsDir {
		if _, ok := a.sizes[path]; !ok {
			a.sizes[path] = 0
		}
		return
	}

	for dir := filepath.Dir(path); ; {
		if !isWithinRoot(root, dir) {
			return
		}
		a.sizes[dir] += entry.Size
		if dir == root {
			return
		}
		next := filepath.Dir(dir)
		if next == dir {
			return
		}
		dir = next
	}
}

func (a *directorySizeAccumulator) snapshot() map[string]int64 {
	if a == nil {
		return nil
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	copySizes := make(map[string]int64, len(a.sizes))
	for path, size := range a.sizes {
		copySizes[path] = size
	}
	return copySizes
}

func isWithinRoot(root, path string) bool {
	root = filepath.Clean(root)
	path = filepath.Clean(path)
	if root == path {
		return true
	}
	rel, err := filepath.Rel(root, path)
	if err != nil || rel == ".." {
		return false
	}
	prefix := ".." + string(filepath.Separator)
	return !strings.HasPrefix(rel, prefix)
}

func DefaultWorkerCount() int {
	w := runtime.NumCPU() * 2
	if w < 4 {
		return 4
	}
	if w > 32 {
		return 32
	}
	return w
}

func (r Runner) Scan(ctx context.Context, roots []string) (Metrics, error) {
	start := time.Now()
	if r.Indexer == nil {
		return Metrics{}, errors.New("indexer is required")
	}
	if len(roots) == 0 {
		return Metrics{}, errors.New("at least one root is required")
	}
	roots = cleanRoots(roots)

	reconciler, reconcile := r.Indexer.(Reconciler)
	sessionID := int64(0)
	if reconcile {
		id, err := reconciler.BeginScan(ctx, roots)
		if err != nil {
			return Metrics{}, err
		}
		sessionID = id
		defer func() {
			if sessionID == 0 {
				return
			}
			cleanupCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			_ = reconciler.AbortScan(cleanupCtx, sessionID)
		}()
	}

	batchSize := r.Batch
	if batchSize <= 0 {
		batchSize = 2000
	}

	entriesCh := make(chan db.Entry, batchSize*2)
	errCh := make(chan error, 1)

	var (
		scanned int64
		indexed int64
		skipped int64
	)
	sizes := newDirectorySizeAccumulator()
	var currentPath atomic.Value
	protected := make(map[string]struct{})
	var protectedMu sync.Mutex

	emitProgress := func() {
		if r.Progress == nil {
			return
		}
		elapsed := time.Since(start)
		scannedNow := atomic.LoadInt64(&scanned)
		progress := Progress{
			Scanned:     scannedNow,
			Indexed:     atomic.LoadInt64(&indexed),
			Skipped:     atomic.LoadInt64(&skipped),
			Elapsed:     elapsed,
			CurrentPath: "",
		}
		if path, ok := currentPath.Load().(string); ok {
			progress.CurrentPath = path
		}
		if elapsed > 0 {
			progress.FilesPerSecond = float64(scannedNow) / elapsed.Seconds()
		}
		r.Progress(progress)
	}

	writerDone := make(chan struct{})
	go func() {
		defer close(writerDone)
		batch := make([]db.Entry, 0, batchSize)
		flush := func() error {
			if len(batch) == 0 {
				return nil
			}
			if err := r.Indexer.UpsertBatch(ctx, batch); err != nil {
				return err
			}
			if reconcile {
				if err := reconciler.MarkSeenBatch(ctx, sessionID, batch); err != nil {
					return err
				}
			}
			atomic.AddInt64(&indexed, int64(len(batch)))
			batch = batch[:0]
			emitProgress()
			return nil
		}

		for {
			select {
			case <-ctx.Done():
				return
			case entry, ok := <-entriesCh:
				if !ok {
					if err := flush(); err != nil {
						sendErr(errCh, err)
					}
					return
				}
				batch = append(batch, entry)
				if len(batch) >= batchSize {
					if err := flush(); err != nil {
						sendErr(errCh, err)
						return
					}
				}
			}
		}
	}()

	emitEntry := func(entry db.Entry) error {
		sizes.add(entry)
		select {
		case entriesCh <- entry:
			return nil
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	progress := scanProgress{
		Scanned:     &scanned,
		Skipped:     &skipped,
		CurrentPath: &currentPath,
		Emit:        emitProgress,
		Protect: func(path string) {
			path = strings.TrimSpace(path)
			if path == "" {
				return
			}
			protectedMu.Lock()
			protected[filepath.Clean(path)] = struct{}{}
			protectedMu.Unlock()
		},
	}
	if err := r.backend().Scan(ctx, roots, emitEntry, progress); err != nil {
		close(entriesCh)
		<-writerDone
		return Metrics{}, err
	}

	if ctx.Err() != nil {
		close(entriesCh)
		<-writerDone
		return Metrics{}, ctx.Err()
	}

	close(entriesCh)
	<-writerDone

	select {
	case err := <-errCh:
		return Metrics{}, err
	default:
	}
	if ctx.Err() != nil {
		return Metrics{}, ctx.Err()
	}
	elapsed := time.Since(start)
	metrics := Metrics{
		Scanned: atomic.LoadInt64(&scanned),
		Indexed: atomic.LoadInt64(&indexed),
		Skipped: atomic.LoadInt64(&skipped),
		Elapsed: elapsed,
	}
	if elapsed > 0 {
		metrics.FilesPerSecond = float64(metrics.Scanned) / elapsed.Seconds()
	}
	if reconcile {
		protectedMu.Lock()
		protectedPaths := make([]string, 0, len(protected))
		for path := range protected {
			protectedPaths = append(protectedPaths, path)
		}
		protectedMu.Unlock()

		for _, path := range protectedPaths {
			if err := reconciler.MarkUnreadablePrefix(ctx, sessionID, path); err != nil {
				return Metrics{}, err
			}
		}
		if err := reconciler.FinishScan(ctx, sessionID, roots); err != nil {
			return Metrics{}, err
		}
		sessionID = 0
	}

	if updater, ok := r.Indexer.(DirectorySizeUpdater); ok {
		if err := updater.UpdateDirectorySizes(ctx, sizes.snapshot()); err != nil {
			return Metrics{}, err
		}
	}

	emitProgress()

	return metrics, nil
}

func (r Runner) backend() scanBackend {
	backend := strings.ToLower(strings.TrimSpace(r.Backend))
	if backend == "" {
		backend = BackendAuto
	}
	if backend == BackendNTFS {
		return ntfsBackend{}
	}
	if backend == BackendAuto {
		return autoBackend{ntfs: ntfsBackend{}, walk: r.walkBackend(), warning: r.Warning}
	}
	return r.walkBackend()
}

func (r Runner) walkBackend() walkBackend {
	workers := r.Workers
	if workers <= 0 {
		workers = DefaultWorkerCount()
	}
	return walkBackend{workers: workers, exclude: r.Exclude}
}

func sendErr(errCh chan error, err error) {
	select {
	case errCh <- err:
	default:
	}
}

func cleanRoots(roots []string) []string {
	out := make([]string, 0, len(roots))
	for _, root := range roots {
		root = strings.TrimSpace(root)
		if root == "" {
			continue
		}
		out = append(out, filepath.Clean(root))
	}
	return out
}

type walkBackend struct {
	workers int
	exclude []string
}

func (b walkBackend) Scan(ctx context.Context, roots []string, emit func(db.Entry) error, progress scanProgress) error {
	mountFilter := newMountFilter(roots)
	for _, root := range roots {
		root := root
		exclude := newExcludeMatcher(root, b.exclude)
		err := fastwalk.Walk(&fastwalk.Config{Follow: false, NumWorkers: b.workers}, root, func(path string, d fs.DirEntry, err error) error {
			if err != nil {
				progress.CurrentPath.Store(path)
				atomic.AddInt64(progress.Skipped, 1)
				progress.Emit()
				if filepath.Clean(path) == filepath.Clean(root) {
					return err
				}
				if progress.Protect != nil {
					progress.Protect(path)
				}
				return nil
			}

			if d.IsDir() && mountFilter != nil && mountFilter(root, path) {
				return fastwalk.SkipDir
			}
			if exclude(path, d.IsDir()) {
				if d.IsDir() {
					return fastwalk.SkipDir
				}
				progress.CurrentPath.Store(path)
				atomic.AddInt64(progress.Skipped, 1)
				progress.Emit()
				return nil
			}
			select {
			case <-ctx.Done():
				return ctx.Err()
			default:
			}

			info, infoErr := d.Info()
			if infoErr != nil {
				atomic.AddInt64(progress.Skipped, 1)
				if progress.Protect != nil {
					progress.Protect(path)
				}
				return nil
			}

			progress.CurrentPath.Store(path)
			atomic.AddInt64(progress.Scanned, 1)
			progress.Emit()
			return emit(db.NewEntryFromPath(root, path, info.Size(), info.ModTime(), d.IsDir()))
		})
		if err != nil && !errors.Is(err, context.Canceled) {
			return err
		}
	}
	return nil
}

func newExcludeMatcher(root string, patterns []string) func(path string, isDir bool) bool {
	normalized := make([]string, 0, len(patterns))
	for _, pattern := range patterns {
		trimmed := strings.TrimSpace(pattern)
		if trimmed == "" {
			continue
		}
		normalized = append(normalized, filepath.Clean(trimmed))
	}

	root = filepath.Clean(root)

	return func(path string, _ bool) bool {
		path = filepath.Clean(path)
		rel, err := filepath.Rel(root, path)
		if err != nil {
			return false
		}
		rel = filepath.Clean(rel)

		for _, pattern := range normalized {
			if strings.Contains(pattern, string(filepath.Separator)) {
				if strings.HasSuffix(pattern, string(filepath.Separator)+"*") {
					prefix := strings.TrimSuffix(pattern, string(filepath.Separator)+"*")
					if rel == prefix || strings.HasPrefix(rel, prefix+string(filepath.Separator)) {
						return true
					}
				}
				if ok, _ := filepath.Match(pattern, rel); ok {
					return true
				}
				continue
			}

			if filepath.Base(path) == pattern {
				return true
			}
			if ok, _ := filepath.Match(pattern, filepath.Base(path)); ok {
				return true
			}
		}

		return false
	}
}
