package contexts_test

import (
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/kameas-ai/kenaz-harness/core/contexts"
)

// drainOpEvents collects every typed event the subscriber receives
// over the supplied window. The window is intentionally a bit wider
// than the 200 ms debounce so the timer's last fire is included in
// the assertion's view.
func drainOpEvents(w *contexts.Watcher, window time.Duration) []contexts.WatcherEvent {
	out := []contexts.WatcherEvent{}
	deadline := time.After(window)
	for {
		select {
		case ev := <-w.OpEvents():
			out = append(out, ev)
		case <-deadline:
			return out
		}
	}
}

func TestWatcherDebounceCoalescesRapidWrites(t *testing.T) {
	t.Parallel()
	lib, _ := newLib(t)
	w := lib.Subscribe()
	defer w.Close()

	// Five Save calls in quick succession should fan out to a single
	// debounced tick — the window is 200 ms and these complete well
	// within that envelope.
	for i := 0; i < 5; i++ {
		if err := lib.Save("notes.md", "x"); err != nil {
			t.Fatalf("Save %d: %v", i, err)
		}
	}
	got := drainOpEvents(w, 400*time.Millisecond)
	if len(got) != 1 {
		t.Fatalf("got %d events; want 1 (events: %+v)", len(got), got)
	}
	if got[0].Path != "notes.md" || got[0].Op != contexts.OpModified {
		t.Fatalf("event = %+v; want notes.md/modified", got[0])
	}
}

func TestWatcherDebounceDistinctPathsDistinctTicks(t *testing.T) {
	t.Parallel()
	lib, _ := newLib(t)
	w := lib.Subscribe()
	defer w.Close()

	if err := lib.Save("a.md", "x"); err != nil {
		t.Fatalf("Save a: %v", err)
	}
	if err := lib.Save("b.md", "y"); err != nil {
		t.Fatalf("Save b: %v", err)
	}
	got := drainOpEvents(w, 400*time.Millisecond)
	if len(got) != 2 {
		t.Fatalf("got %d events; want 2 (events: %+v)", len(got), got)
	}
	paths := map[string]bool{got[0].Path: true, got[1].Path: true}
	if !paths["a.md"] || !paths["b.md"] {
		t.Fatalf("missing path coverage: %v", got)
	}
}

func TestWatcherDeleteThenRecreateCollapsesToModified(t *testing.T) {
	t.Parallel()
	lib, _ := newLib(t)
	if err := lib.Save("doc.md", "v1"); err != nil {
		t.Fatalf("seed Save: %v", err)
	}
	// Drain the seed event so the test only sees what the
	// delete-then-recreate window produces.
	w := lib.Subscribe()
	defer w.Close()

	if err := lib.Delete("doc.md"); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	if err := lib.Save("doc.md", "v2"); err != nil {
		t.Fatalf("Save (recreate): %v", err)
	}
	got := drainOpEvents(w, 400*time.Millisecond)
	if len(got) != 1 {
		t.Fatalf("got %d events; want 1 (events: %+v)", len(got), got)
	}
	if got[0].Op != contexts.OpModified {
		t.Fatalf("event op = %v; want OpModified (delete+create collapse)", got[0].Op)
	}
}

func TestWatcherDuplicateOpDropped(t *testing.T) {
	t.Parallel()
	lib, _ := newLib(t)
	w := lib.Subscribe()
	defer w.Close()

	// Two CreateFolder calls (same path, same op) within the window
	// must collapse to a single directory-created tick. The scaffold
	// writes a context.md stub alongside the directory, so we expect
	// exactly two distinct paths in the debounced event set:
	//   - "notes"           (directory created)
	//   - "notes/context.md" (scaffold root file created)
	// A third call on the same already-existing path is a no-op and
	// must NOT produce an additional event (that's the duplicate-drop
	// contract).
	if err := lib.CreateFolder("notes"); err != nil {
		t.Fatalf("CreateFolder 1: %v", err)
	}
	if err := lib.CreateFolder("notes"); err != nil {
		t.Fatalf("CreateFolder 2: %v", err)
	}
	got := drainOpEvents(w, 400*time.Millisecond)
	// Expect exactly 2 events: the directory and the scaffolded root file.
	// The second CreateFolder is a no-op (dir already exists) so its
	// OpCreated on "notes" is dropped by the debouncer.
	if len(got) != 2 {
		t.Fatalf("got %d events; want 2 (notes + notes/context.md)", len(got))
	}
	paths := make(map[string]bool)
	for _, ev := range got {
		paths[ev.Path] = true
	}
	if !paths["notes"] {
		t.Error("missing event for notes directory")
	}
	if !paths["notes/context.md"] {
		t.Error("missing event for notes/context.md scaffold")
	}
}

func TestWatcherDebounceWindowAtLeast200ms(t *testing.T) {
	t.Parallel()
	lib, _ := newLib(t)
	w := lib.Subscribe()
	defer w.Close()

	start := time.Now()
	if err := lib.Save("ping.md", "x"); err != nil {
		t.Fatalf("Save: %v", err)
	}
	select {
	case <-w.Events():
		elapsed := time.Since(start)
		// The ≤500 ms ceiling is the spec's NFR; the ≥150 ms floor
		// guards against an accidental zero-debounce regression.
		// (We allow 50 ms slack under 200 ms for OS-scheduler jitter.)
		if elapsed < 150*time.Millisecond {
			t.Fatalf("tick fired in %v; window should be ~200 ms", elapsed)
		}
		if elapsed > 500*time.Millisecond {
			t.Fatalf("tick fired in %v; budget 500 ms", elapsed)
		}
	case <-time.After(time.Second):
		t.Fatal("watcher did not fire within 1 s")
	}
}

func TestStartWatchingExternalWriteFires(t *testing.T) {
	t.Parallel()
	lib, root := newLib(t)
	if err := lib.StartWatching(); err != nil {
		// Some sandboxed runners deny inotify; skip rather than fail.
		t.Skipf("StartWatching unavailable on this platform: %v", err)
	}
	defer lib.StopWatching()

	w := lib.Subscribe()
	defer w.Close()

	// Simulate an "operator drops a file in via Finder" — write
	// directly to the filesystem without going through Save.
	external := filepath.Join(root, "external.md")
	if err := os.WriteFile(external, []byte("hello"), 0o600); err != nil {
		t.Fatalf("external write: %v", err)
	}

	select {
	case <-w.Events():
	case <-time.After(2 * time.Second):
		t.Fatal("watcher did not fire within 2 s of external write")
	}
}

func TestStartWatchingIdempotent(t *testing.T) {
	t.Parallel()
	lib, _ := newLib(t)
	if err := lib.StartWatching(); err != nil {
		t.Skipf("StartWatching unavailable: %v", err)
	}
	defer lib.StopWatching()
	// A second call must not error nor stack a second backend.
	if err := lib.StartWatching(); err != nil {
		t.Fatalf("second StartWatching: %v", err)
	}
}

func TestWatcherCloseDetachesSubscriber(t *testing.T) {
	t.Parallel()
	lib, _ := newLib(t)
	w := lib.Subscribe()
	w.Close()
	// A Save after Close must not deadlock or panic; the producer
	// should observe the closed flag and skip the dropped subscriber.
	done := make(chan struct{})
	go func() {
		_ = lib.Save("after.md", "x")
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("Save deadlocked after subscriber Close")
	}
}

func TestWatcherConcurrentSaveSafeForRace(t *testing.T) {
	t.Parallel()
	lib, _ := newLib(t)
	w := lib.Subscribe()
	defer w.Close()

	// Drain in the background — the producers must never block on
	// the receiver. With cap-1 + non-blocking send the contract is
	// "drop if full," and -race must not flag the producer/consumer
	// interleaving.
	stopDrain := make(chan struct{})
	var drained sync.WaitGroup
	drained.Add(1)
	go func() {
		defer drained.Done()
		for {
			select {
			case <-stopDrain:
				return
			case <-w.Events():
			case <-w.OpEvents():
			}
		}
	}()

	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			for j := 0; j < 10; j++ {
				name := filepath.Join("d", "f.md")
				_ = lib.Save(name, "x")
				_ = i
				_ = j
			}
		}(i)
	}
	wg.Wait()
	time.Sleep(300 * time.Millisecond) // let last debounce timers fire
	close(stopDrain)
	drained.Wait()
}

func TestTreeIncludeHidden(t *testing.T) {
	t.Parallel()
	lib, root := newLib(t)
	if err := lib.Save("visible.md", "v"); err != nil {
		t.Fatalf("Save visible: %v", err)
	}
	if err := os.WriteFile(filepath.Join(root, ".hidden.md"), []byte("h"), 0o600); err != nil {
		t.Fatalf("seed dotfile: %v", err)
	}

	hidden, err := lib.TreeWithOptions(false)
	if err != nil {
		t.Fatalf("TreeWithOptions(false): %v", err)
	}
	for _, c := range hidden.Children {
		if c.Name == ".hidden.md" {
			t.Fatalf("dotfile leaked into default tree")
		}
	}
	withHidden, err := lib.TreeWithOptions(true)
	if err != nil {
		t.Fatalf("TreeWithOptions(true): %v", err)
	}
	found := false
	for _, c := range withHidden.Children {
		if c.Name == ".hidden.md" {
			found = true
		}
		if c.Name == ".trash" || c.Name == ".recent.json" {
			t.Errorf("chassis plumbing leaked even with includeHidden=true: %s", c.Name)
		}
	}
	if !found {
		t.Fatal("dotfile missing from tree with includeHidden=true")
	}
}
