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

type Runner struct {
	Store   *db.Store
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

type scanner interface {
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
	if r.Store == nil {
		return Metrics{}, errors.New("store is required")
	}
	if len(roots) == 0 {
		return Metrics{}, errors.New("at least one root is required")
	}
	roots = cleanRoots(roots)

	sessionID, err := r.Store.BeginScan(ctx, roots)
	if err != nil {
		return Metrics{}, err
	}
	defer func() {
		if sessionID == 0 {
			return
		}
		cleanupCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = r.Store.AbortScan(cleanupCtx, sessionID)
	}()

	batchSize := r.Batch
	if batchSize <= 0 {
		batchSize = 2000
	}
	result, err := r.runScanBackend(ctx, roots, sessionID, batchSize, start)
	if err != nil {
		return Metrics{}, err
	}
	if err := r.finishScan(ctx, sessionID, roots, result.protectedPaths); err != nil {
		return Metrics{}, err
	}
	sessionID = 0
	if err := r.Store.UpdateDirectorySizes(ctx, result.sizes); err != nil {
		return Metrics{}, err
	}
	result.emitProgress()
	return result.metrics, nil
}

type scanRunResult struct {
	metrics        Metrics
	sizes          map[string]int64
	protectedPaths []string
	emitProgress   func()
}

type scanCollector struct {
	start       time.Time
	progress    func(Progress)
	scanned     int64
	indexed     int64
	skipped     int64
	currentPath atomic.Value
	sizes       *directorySizeAccumulator
	protected   map[string]struct{}
	protectedMu sync.Mutex
}

func newScanCollector(start time.Time, progress func(Progress)) *scanCollector {
	return &scanCollector{
		start:     start,
		progress:  progress,
		sizes:     newDirectorySizeAccumulator(),
		protected: make(map[string]struct{}),
	}
}

func (c *scanCollector) emitProgress() {
	if c.progress == nil {
		return
	}
	elapsed := time.Since(c.start)
	scanned := atomic.LoadInt64(&c.scanned)
	progress := Progress{
		Scanned:     scanned,
		Indexed:     atomic.LoadInt64(&c.indexed),
		Skipped:     atomic.LoadInt64(&c.skipped),
		Elapsed:     elapsed,
		CurrentPath: "",
	}
	if path, ok := c.currentPath.Load().(string); ok {
		progress.CurrentPath = path
	}
	if elapsed > 0 {
		progress.FilesPerSecond = float64(scanned) / elapsed.Seconds()
	}
	c.progress(progress)
}

func (c *scanCollector) progressState() scanProgress {
	return scanProgress{
		Scanned:     &c.scanned,
		Skipped:     &c.skipped,
		CurrentPath: &c.currentPath,
		Emit:        c.emitProgress,
		Protect:     c.protect,
	}
}

func (c *scanCollector) protect(path string) {
	path = strings.TrimSpace(path)
	if path == "" {
		return
	}
	c.protectedMu.Lock()
	c.protected[filepath.Clean(path)] = struct{}{}
	c.protectedMu.Unlock()
}

func (c *scanCollector) emitEntry(ctx context.Context, entriesCh chan<- db.Entry, entry db.Entry) error {
	c.sizes.add(entry)
	select {
	case entriesCh <- entry:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (c *scanCollector) metrics() Metrics {
	elapsed := time.Since(c.start)
	metrics := Metrics{
		Scanned: atomic.LoadInt64(&c.scanned),
		Indexed: atomic.LoadInt64(&c.indexed),
		Skipped: atomic.LoadInt64(&c.skipped),
		Elapsed: elapsed,
	}
	if elapsed > 0 {
		metrics.FilesPerSecond = float64(metrics.Scanned) / elapsed.Seconds()
	}
	return metrics
}

func (c *scanCollector) protectedPaths() []string {
	c.protectedMu.Lock()
	defer c.protectedMu.Unlock()
	paths := make([]string, 0, len(c.protected))
	for path := range c.protected {
		paths = append(paths, path)
	}
	return paths
}

func (r Runner) runScanBackend(ctx context.Context, roots []string, sessionID int64, batchSize int, start time.Time) (scanRunResult, error) {
	collector := newScanCollector(start, r.Progress)
	entriesCh := make(chan db.Entry, batchSize*2)
	errCh := make(chan error, 1)
	writerDone := make(chan struct{})
	go scanWriter{
		ctx:          ctx,
		store:        r.Store,
		sessionID:    sessionID,
		batchSize:    batchSize,
		entries:      entriesCh,
		errors:       errCh,
		indexed:      &collector.indexed,
		emitProgress: collector.emitProgress,
		done:         writerDone,
	}.run()

	backendErr := r.backend().Scan(ctx, roots, func(entry db.Entry) error {
		return collector.emitEntry(ctx, entriesCh, entry)
	}, collector.progressState())
	close(entriesCh)
	<-writerDone
	if backendErr != nil {
		return scanRunResult{}, backendErr
	}
	select {
	case err := <-errCh:
		return scanRunResult{}, err
	default:
	}
	if err := ctx.Err(); err != nil {
		return scanRunResult{}, err
	}
	return scanRunResult{
		metrics:        collector.metrics(),
		sizes:          collector.sizes.snapshot(),
		protectedPaths: collector.protectedPaths(),
		emitProgress:   collector.emitProgress,
	}, nil
}

type scanWriter struct {
	ctx          context.Context
	store        *db.Store
	sessionID    int64
	batchSize    int
	entries      <-chan db.Entry
	errors       chan<- error
	indexed      *int64
	emitProgress func()
	done         chan<- struct{}
}

func (w scanWriter) run() {
	defer close(w.done)
	batch := make([]db.Entry, 0, w.batchSize)
	for {
		select {
		case <-w.ctx.Done():
			return
		case entry, ok := <-w.entries:
			if !ok {
				if _, err := w.flush(batch); err != nil {
					sendErr(w.errors, err)
				}
				return
			}
			batch = append(batch, entry)
			if len(batch) >= w.batchSize {
				var err error
				batch, err = w.flush(batch)
				if err != nil {
					sendErr(w.errors, err)
					return
				}
			}
		}
	}
}

func (w scanWriter) flush(batch []db.Entry) ([]db.Entry, error) {
	if len(batch) == 0 {
		return batch, nil
	}
	if err := w.store.UpsertBatch(w.ctx, batch); err != nil {
		return batch, err
	}
	if err := w.store.MarkSeenBatch(w.ctx, w.sessionID, batch); err != nil {
		return batch, err
	}
	atomic.AddInt64(w.indexed, int64(len(batch)))
	w.emitProgress()
	return batch[:0], nil
}
func (r Runner) finishScan(ctx context.Context, sessionID int64, roots []string, protectedPaths []string) error {
	for _, path := range protectedPaths {
		if err := r.Store.MarkUnreadablePrefix(ctx, sessionID, path); err != nil {
			return err
		}
	}
	return r.Store.FinishScan(ctx, sessionID, roots)
}

func (r Runner) backend() scanner {
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

func sendErr(errCh chan<- error, err error) {
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
		handler := walkEntryHandler{ctx: ctx, root: root, mountFilter: mountFilter, exclude: exclude, emit: emit, progress: progress}
		err := fastwalk.Walk(&fastwalk.Config{Follow: false, NumWorkers: b.workers}, root, func(path string, d fs.DirEntry, walkErr error) error {
			return handler.handle(path, d, walkErr)
		})
		if err != nil && !errors.Is(err, context.Canceled) {
			return err
		}
	}
	return nil
}

type walkEntryHandler struct {
	ctx         context.Context
	root        string
	mountFilter func(root, path string) bool
	exclude     func(string, bool) bool
	emit        func(db.Entry) error
	progress    scanProgress
}

func (h walkEntryHandler) handle(path string, d fs.DirEntry, walkErr error) error {
	if walkErr != nil {
		h.progress.CurrentPath.Store(path)
		atomic.AddInt64(h.progress.Skipped, 1)
		h.progress.Emit()
		if filepath.Clean(path) == filepath.Clean(h.root) {
			return walkErr
		}
		if h.progress.Protect != nil {
			h.progress.Protect(path)
		}
		return nil
	}
	if d.IsDir() && h.mountFilter != nil && h.mountFilter(h.root, path) {
		return fastwalk.SkipDir
	}
	if h.exclude(path, d.IsDir()) {
		return skipWalkEntry(path, d.IsDir(), h.progress)
	}
	select {
	case <-h.ctx.Done():
		return h.ctx.Err()
	default:
	}
	info, infoErr := d.Info()
	if infoErr != nil {
		atomic.AddInt64(h.progress.Skipped, 1)
		if h.progress.Protect != nil {
			h.progress.Protect(path)
		}
		return nil
	}
	h.progress.CurrentPath.Store(path)
	atomic.AddInt64(h.progress.Scanned, 1)
	h.progress.Emit()
	return h.emit(db.NewEntryFromPath(h.root, path, info.Size(), info.ModTime(), d.IsDir()))
}

func skipWalkEntry(path string, isDir bool, progress scanProgress) error {
	if isDir {
		return fastwalk.SkipDir
	}
	progress.CurrentPath.Store(path)
	atomic.AddInt64(progress.Skipped, 1)
	progress.Emit()
	return nil
}

func newExcludeMatcher(root string, patterns []string) func(path string, isDir bool) bool {
	normalized := normalizeExcludePatterns(patterns)
	root = filepath.Clean(root)

	return func(path string, _ bool) bool {
		path = filepath.Clean(path)
		rel, err := filepath.Rel(root, path)
		if err != nil {
			return false
		}
		rel = filepath.Clean(rel)

		for _, pattern := range normalized {
			if excludePatternMatches(path, rel, pattern) {
				return true
			}
		}

		return false
	}
}

func normalizeExcludePatterns(patterns []string) []string {
	normalized := make([]string, 0, len(patterns))
	for _, pattern := range patterns {
		if trimmed := strings.TrimSpace(pattern); trimmed != "" {
			normalized = append(normalized, filepath.Clean(trimmed))
		}
	}
	return normalized
}

func excludePatternMatches(path, rel, pattern string) bool {
	if strings.Contains(pattern, string(filepath.Separator)) {
		if strings.HasSuffix(pattern, string(filepath.Separator)+"*") {
			prefix := strings.TrimSuffix(pattern, string(filepath.Separator)+"*")
			if rel == prefix || strings.HasPrefix(rel, prefix+string(filepath.Separator)) {
				return true
			}
		}
		matched, _ := filepath.Match(pattern, rel)
		return matched
	}
	base := filepath.Base(path)
	if base == pattern {
		return true
	}
	matched, _ := filepath.Match(pattern, base)
	return matched
}
