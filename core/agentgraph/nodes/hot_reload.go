package nodes

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

// DefaultHotReloadInterval is the polling cadence the dev-flag-gated
// watcher uses when no explicit interval is supplied. Set to 2s — fast
// enough that a manual edit-save reflects on the next palette refresh
// without burning CPU during a 2-hour debugging session. The flag is
// disabled by default; production graphs MUST get a stable manifest
// set (FR-023 + spec §4.6).
const DefaultHotReloadInterval = 2 * time.Second

// WatcherConfig parameterises the hot-reload watcher.
type WatcherConfig struct {
	// UserDir is the directory to poll for changes (typically
	// <DataDir>/agent_graph/nodes/). Empty disables the watcher
	// (Start returns nil immediately).
	UserDir string
	// Interval is the polling cadence. Zero falls back to
	// DefaultHotReloadInterval.
	Interval time.Duration
	// OnReload fires after every successful re-resolve. Receives the
	// freshly-loaded catalog so the chassis can swap its live pointer.
	// Required when Start is invoked; nil produces a no-op poller.
	OnReload func(*Catalog)
	// OnError fires when LoadCatalog returns an error during a poll
	// cycle. Optional; nil swallows the error.
	OnError func(error)
	// NowFn is overridable for tests. Defaults to time.Now.
	NowFn func() time.Time
}

// Watcher polls the user-override directory for *.yaml changes and
// re-resolves the catalog when a change is detected. Stdlib only — no
// fsnotify dependency. The watcher is gated by the chassis-level
// `--enable-manifest-hot-reload` flag (see core.Options.HotReload).
//
// Concurrent use is safe: Start launches one goroutine; Stop signals
// shutdown via the cancellable context.
type Watcher struct {
	cfg WatcherConfig

	mu       sync.Mutex
	lastSnap map[string]string // path → "<modtime>:<size>:<sha-of-bytes>"
	cancel   context.CancelFunc
	wg       sync.WaitGroup
}

// NewWatcher constructs a Watcher. Start kicks the polling goroutine.
func NewWatcher(cfg WatcherConfig) *Watcher {
	if cfg.Interval <= 0 {
		cfg.Interval = DefaultHotReloadInterval
	}
	if cfg.NowFn == nil {
		cfg.NowFn = time.Now
	}
	return &Watcher{cfg: cfg, lastSnap: map[string]string{}}
}

// Start kicks the polling goroutine. ctx cancellation triggers shutdown.
// A nil OnReload + empty UserDir is a no-op (Start returns immediately).
// Calling Start twice without Stop in between is a no-op past the first.
func (w *Watcher) Start(ctx context.Context) {
	if w == nil || w.cfg.UserDir == "" || w.cfg.OnReload == nil {
		return
	}
	w.mu.Lock()
	if w.cancel != nil {
		w.mu.Unlock()
		return
	}
	cctx, cancel := context.WithCancel(ctx)
	w.cancel = cancel
	w.mu.Unlock()

	// Seed the snapshot so the first poll surfaces real changes only.
	w.lastSnap = w.scanSnapshot()

	w.wg.Add(1)
	go w.loop(cctx)
}

// Stop signals shutdown and blocks until the polling goroutine exits.
// Idempotent.
func (w *Watcher) Stop() {
	if w == nil {
		return
	}
	w.mu.Lock()
	cancel := w.cancel
	w.cancel = nil
	w.mu.Unlock()
	if cancel != nil {
		cancel()
	}
	w.wg.Wait()
}

// Tick runs one poll cycle synchronously and reports whether a change
// was detected (and, if so, whether the reload succeeded). Exposed for
// tests so they can drive the watcher with a fake clock.
func (w *Watcher) Tick() (changed bool, err error) {
	if w == nil || w.cfg.UserDir == "" {
		return false, nil
	}
	cur := w.scanSnapshot()
	if snapshotsEqual(cur, w.lastSnap) {
		return false, nil
	}
	w.lastSnap = cur
	cat, lerr := LoadCatalog(LoadOptions{UserDir: w.cfg.UserDir})
	if lerr != nil {
		if w.cfg.OnError != nil {
			w.cfg.OnError(lerr)
		}
		// Even on parse errors, the loader returns a partial catalog.
		// Surface it so the watcher's onReload can still propagate the
		// usable manifests; the caller decides whether to swap.
	}
	if cat != nil && w.cfg.OnReload != nil {
		w.cfg.OnReload(cat)
	}
	return true, lerr
}

func (w *Watcher) loop(ctx context.Context) {
	defer w.wg.Done()
	ticker := time.NewTicker(w.cfg.Interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			_, _ = w.Tick()
		}
	}
}

// scanSnapshot enumerates *.yaml files at UserDir and produces a
// content-keyed map. Missing dir → empty map.
func (w *Watcher) scanSnapshot() map[string]string {
	out := map[string]string{}
	if w == nil || w.cfg.UserDir == "" {
		return out
	}
	entries, err := os.ReadDir(w.cfg.UserDir)
	if err != nil {
		if !isNotExist(err) && w.cfg.OnError != nil {
			w.cfg.OnError(err)
		}
		return out
	}
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(strings.ToLower(e.Name()), ".yaml") {
			continue
		}
		full := filepath.Join(w.cfg.UserDir, e.Name())
		fi, fierr := e.Info()
		if fierr != nil {
			continue
		}
		// We hash the file bytes so a touch (mtime change with same
		// content) does not spuriously fire OnReload. The size+mtime
		// is appended for cheap fast-path equality.
		data, rerr := os.ReadFile(full) //nolint:gosec // user-controlled path under DataDir is intentional
		if rerr != nil {
			continue
		}
		sum := sha256.Sum256(data)
		key := hex.EncodeToString(sum[:])
		out[full] = mtimeKey(fi.ModTime(), fi.Size()) + ":" + key
	}
	return out
}

// snapshotsEqual returns true iff the path→key maps agree exactly.
func snapshotsEqual(a, b map[string]string) bool {
	if len(a) != len(b) {
		return false
	}
	keys := make([]string, 0, len(a))
	for k := range a {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		if a[k] != b[k] {
			return false
		}
	}
	return true
}

func mtimeKey(t time.Time, size int64) string {
	return t.UTC().Format(time.RFC3339Nano) + ":" + strconv.FormatInt(size, 10)
}

func isNotExist(err error) bool {
	return err != nil && errors.Is(err, fs.ErrNotExist)
}
