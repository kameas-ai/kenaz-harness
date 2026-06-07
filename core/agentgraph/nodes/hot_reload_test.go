package nodes_test

import (
	"context"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/kameas-ai/kenaz-harness/core/agentgraph/nodes"
)

// TestWatcherTickDetectsAddedFile drops a YAML file under a fresh
// directory, ticks the watcher synchronously, and asserts OnReload
// fired with a catalog containing the new manifest.
func TestWatcherTickDetectsAddedFile(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()

	var (
		mu        sync.Mutex
		reloadCnt atomic.Int32
		lastCat   *nodes.Catalog
	)
	w := nodes.NewWatcher(nodes.WatcherConfig{
		UserDir:  dir,
		Interval: 50 * time.Millisecond,
		OnReload: func(cat *nodes.Catalog) {
			reloadCnt.Add(1)
			mu.Lock()
			lastCat = cat
			mu.Unlock()
		},
	})

	// First tick on an empty dir produces no signal.
	changed, err := w.Tick()
	if err != nil {
		t.Fatalf("first tick: %v", err)
	}
	if changed {
		t.Errorf("expected no change on empty dir, got changed=true")
	}

	// Drop a manifest and re-tick.
	if err := os.WriteFile(filepath.Join(dir, "_archetype.compute.yaml"), []byte(`archetype: compute
category: compute
description: tuned-by-watcher-test
budget: llm
`), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	changed, err = w.Tick()
	if err != nil {
		t.Fatalf("second tick: %v", err)
	}
	if !changed {
		t.Errorf("expected change after writing file")
	}
	if reloadCnt.Load() == 0 {
		t.Errorf("OnReload never fired")
	}
	mu.Lock()
	if lastCat == nil {
		t.Fatal("OnReload received nil catalog")
	}
	rm, gErr := lastCat.Get("compute")
	mu.Unlock()
	if gErr != nil {
		t.Fatalf("Get(compute): %v", gErr)
	}
	if rm.Manifest.Description != "tuned-by-watcher-test" {
		t.Errorf("description: got %q", rm.Manifest.Description)
	}
}

// TestWatcherTickIgnoresNoOpTouch asserts that re-running Tick without
// content changes does NOT spuriously fire OnReload (i.e. the watcher
// is content-keyed, not just mtime-keyed).
func TestWatcherTickIgnoresNoOpTouch(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "_archetype.compute.yaml"), []byte(`archetype: compute
category: compute
description: stable
budget: llm
`), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}

	var fired atomic.Int32
	w := nodes.NewWatcher(nodes.WatcherConfig{
		UserDir:  dir,
		Interval: 50 * time.Millisecond,
		OnReload: func(*nodes.Catalog) { fired.Add(1) },
	})
	// Seed: first tick records the snapshot but does fire the initial
	// reload (file appeared from "empty seed" → "1 file").
	if _, err := w.Tick(); err != nil {
		t.Fatalf("tick 1: %v", err)
	}
	first := fired.Load()
	// Tick again — content is identical; fired should not increment.
	if changed, err := w.Tick(); err != nil {
		t.Fatalf("tick 2: %v", err)
	} else if changed {
		t.Errorf("expected no change on second tick (content stable)")
	}
	if got := fired.Load(); got != first {
		t.Errorf("OnReload fired again on stable content: %d → %d", first, got)
	}
}

// TestWatcherTickReportsRemoval covers FR-023 + reload diff: removing
// a file flips the snapshot and OnReload sees an empty user dir.
func TestWatcherTickReportsRemoval(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	path := filepath.Join(dir, "_archetype.compute.yaml")
	if err := os.WriteFile(path, []byte(`archetype: compute
category: compute
description: pre-remove
budget: llm
`), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	var fired atomic.Int32
	w := nodes.NewWatcher(nodes.WatcherConfig{
		UserDir:  dir,
		Interval: 50 * time.Millisecond,
		OnReload: func(*nodes.Catalog) { fired.Add(1) },
	})
	if _, err := w.Tick(); err != nil {
		t.Fatalf("tick 1: %v", err)
	}
	prior := fired.Load()
	if err := os.Remove(path); err != nil {
		t.Fatalf("remove: %v", err)
	}
	changed, err := w.Tick()
	if err != nil {
		t.Fatalf("tick after remove: %v", err)
	}
	if !changed {
		t.Errorf("expected change after file removal")
	}
	if fired.Load() == prior {
		t.Errorf("OnReload did not fire after removal")
	}
}

// TestWatcherStartStopLifecycle starts the watcher in goroutine mode,
// waits for the polling loop to fire OnReload at least once, and then
// signals shutdown via Stop.
func TestWatcherStartStopLifecycle(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "_archetype.compute.yaml"), []byte(`archetype: compute
category: compute
description: live
budget: llm
`), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	var fired atomic.Int32
	w := nodes.NewWatcher(nodes.WatcherConfig{
		UserDir:  dir,
		Interval: 25 * time.Millisecond,
		OnReload: func(*nodes.Catalog) { fired.Add(1) },
	})
	w.Start(context.Background())
	defer w.Stop()

	// Mutate the file and wait briefly for the loop to detect.
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if err := os.WriteFile(filepath.Join(dir, "_archetype.compute.yaml"), []byte(`archetype: compute
category: compute
description: changed-`+time.Now().Format(time.RFC3339Nano)+`
budget: llm
`), 0o644); err != nil {
			t.Fatalf("rewrite: %v", err)
		}
		if fired.Load() > 0 {
			break
		}
		time.Sleep(40 * time.Millisecond)
	}
	if fired.Load() == 0 {
		t.Errorf("watcher loop never fired OnReload within 2s")
	}
}

// TestWatcherDisabledWithoutOnReload: Start is a no-op when OnReload is
// nil OR the user dir is empty. Stop on a never-Started watcher is also
// safe.
func TestWatcherDisabledWithoutOnReload(t *testing.T) {
	t.Parallel()
	w := nodes.NewWatcher(nodes.WatcherConfig{UserDir: "", OnReload: nil})
	w.Start(context.Background())
	w.Stop()
	w2 := nodes.NewWatcher(nodes.WatcherConfig{UserDir: t.TempDir(), OnReload: nil})
	w2.Start(context.Background())
	w2.Stop()
}

// TestWatcherTickIsErrorTolerant asserts that a malformed YAML file
// surfaces the err to OnError but the watcher still treats the
// snapshot as updated (it does not retry forever).
func TestWatcherTickIsErrorTolerant(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "broken.yaml"), []byte("not: [valid"), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	var errCount atomic.Int32
	w := nodes.NewWatcher(nodes.WatcherConfig{
		UserDir:  dir,
		Interval: 50 * time.Millisecond,
		OnReload: func(*nodes.Catalog) {},
		OnError:  func(error) { errCount.Add(1) },
	})
	changed, _ := w.Tick()
	if !changed {
		t.Errorf("expected change on first tick with new file")
	}
	if errCount.Load() == 0 {
		t.Errorf("OnError never fired for malformed YAML")
	}
}
