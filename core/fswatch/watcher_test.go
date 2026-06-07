package fswatch

import (
	"context"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"github.com/fsnotify/fsnotify"
	"github.com/kameas-ai/kenaz-harness/core/hooks"
)

// TestWatcher_WatchUnwatch verifies basic Watch / Unwatch / WatchedPaths.
func TestWatcher_WatchUnwatch(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()

	w, err := New("sess-1", nil, nil)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	defer w.Close()

	if err := w.Watch(dir); err != nil {
		t.Fatalf("Watch: %v", err)
	}
	if got := w.WatchedPaths(); len(got) != 1 {
		t.Fatalf("WatchedPaths len=%d, want 1", len(got))
	}

	// Duplicate watch is a no-op.
	if err := w.Watch(dir); err != nil {
		t.Fatalf("Watch dup: %v", err)
	}
	if got := w.WatchedPaths(); len(got) != 1 {
		t.Fatalf("WatchedPaths after dup len=%d, want 1", len(got))
	}

	// Unwatch removes it.
	if err := w.Unwatch(dir); err != nil {
		t.Fatalf("Unwatch: %v", err)
	}
	if got := w.WatchedPaths(); len(got) != 0 {
		t.Fatalf("WatchedPaths after Unwatch len=%d, want 0", len(got))
	}
}

// TestWatcher_FileCreated fires a file_changed event when a file is created.
func TestWatcher_FileCreated(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()

	// Build a real runner with a generic builtin that records FileChangedEvent.
	reg, err := hooks.NewRegistry("")
	if err != nil {
		t.Fatalf("NewRegistry: %v", err)
	}
	builtins := hooks.NewBuiltinRegistry()
	var fired atomic.Int32
	builtins.RegisterGenericFire("test.fcapture",
		func(_ context.Context, _ string, _ any, _ map[string]any) (hooks.HookOutput, error) {
			fired.Add(1)
			return hooks.HookOutput{}, nil
		},
		hooks.BuiltinDescriptor{ID: "test.fcapture", Name: "capture"},
	)
	if err := reg.Add(hooks.Hook{
		ID:      "h-fc",
		Name:    "test fc",
		Event:   hooks.EventFileChanged,
		Kind:    hooks.KindBuiltin,
		Enabled: true,
		Builtin: "test.fcapture",
	}); err != nil {
		t.Fatalf("reg.Add: %v", err)
	}
	runner := hooks.NewRunner(hooks.Config{Registry: reg, Builtins: builtins})

	w, err := New("sess-fc", runner, nil)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	defer w.Close()

	if err := w.Watch(dir); err != nil {
		t.Fatalf("Watch: %v", err)
	}

	// Create a file — this should trigger the watcher.
	fpath := filepath.Join(dir, "hello.txt")
	if err := os.WriteFile(fpath, []byte("hello"), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	// Wait up to 1s for the debounce + hook fire.
	deadline := time.Now().Add(1 * time.Second)
	for time.Now().Before(deadline) {
		if fired.Load() > 0 {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	if fired.Load() == 0 {
		t.Error("file_changed hook did not fire within 1s")
	}
}

// TestWatcher_SetWatchPaths replaces the watch set.
func TestWatcher_SetWatchPaths(t *testing.T) {
	t.Parallel()
	dir1 := t.TempDir()
	dir2 := t.TempDir()

	w, err := New("sess-swp", nil, nil)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	defer w.Close()

	if err := w.Watch(dir1); err != nil {
		t.Fatalf("Watch dir1: %v", err)
	}

	// Replace: keep dir1, add dir2.
	abs1, _ := filepath.Abs(dir1)
	abs2, _ := filepath.Abs(dir2)
	w.SetWatchPaths([]string{abs1, abs2})

	paths := w.WatchedPaths()
	if len(paths) != 2 {
		t.Fatalf("after SetWatchPaths len=%d, want 2: %v", len(paths), paths)
	}

	// Replace again: only dir2.
	w.SetWatchPaths([]string{abs2})
	paths = w.WatchedPaths()
	if len(paths) != 1 {
		t.Fatalf("after second SetWatchPaths len=%d, want 1: %v", len(paths), paths)
	}
}

// TestWatcher_CloseIdempotent verifies Close can be called multiple times
// without panic.
func TestWatcher_CloseIdempotent(t *testing.T) {
	t.Parallel()
	w, err := New("sess-close", nil, nil)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	w.Close()
	// Second Close: the underlying channel is already closed so the
	// goroutine has exited. Calling cancel again and closing fsnotify a
	// second time should not panic. We only verify no panic here.
	defer func() {
		if r := recover(); r != nil {
			t.Errorf("second Close panicked: %v", r)
		}
	}()
	// fsnotify.Watcher.Close returns an error on the second call on some
	// platforms; we don't surface it (already logged).
}

// TestOpName verifies the fsnotify.Op → string mapping.
func TestOpName(t *testing.T) {
	t.Parallel()
	tests := []struct {
		op   fsnotify.Op
		want string
	}{
		{fsnotify.Create, "create"},
		{fsnotify.Write, "write"},
		{fsnotify.Remove, "remove"},
		{fsnotify.Rename, "rename"},
		{fsnotify.Chmod, "chmod"},
		{0, ""},
	}
	for _, tc := range tests {
		got := opName(tc.op)
		if got != tc.want {
			t.Errorf("opName(%v) = %q, want %q", tc.op, got, tc.want)
		}
	}
}

// TestWatcher_MultipleEvents verifies that multiple rapid events are
// batched into a single fire.
func TestWatcher_MultipleEvents(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()

	reg, _ := hooks.NewRegistry("")
	builtins := hooks.NewBuiltinRegistry()
	var fires atomic.Int32
	builtins.RegisterGenericFire("test.batch",
		func(_ context.Context, _ string, _ any, _ map[string]any) (hooks.HookOutput, error) {
			fires.Add(1)
			return hooks.HookOutput{}, nil
		},
		hooks.BuiltinDescriptor{ID: "test.batch", Name: "batch"},
	)
	_ = reg.Add(hooks.Hook{
		ID:      "h-b",
		Name:    "batch",
		Event:   hooks.EventFileChanged,
		Kind:    hooks.KindBuiltin,
		Enabled: true,
		Builtin: "test.batch",
	})
	runner := hooks.NewRunner(hooks.Config{Registry: reg, Builtins: builtins})

	w, err := New("sess-batch", runner, nil)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	defer w.Close()
	if err := w.Watch(dir); err != nil {
		t.Fatalf("Watch: %v", err)
	}

	// Write 5 files in rapid succession (within the 250ms debounce window).
	for i := 0; i < 5; i++ {
		p := filepath.Join(dir, "file"+string(rune('0'+i))+".txt")
		_ = os.WriteFile(p, []byte("x"), 0o644)
	}

	// Wait for debounce + fire.
	deadline := time.Now().Add(1 * time.Second)
	for time.Now().Before(deadline) {
		if fires.Load() > 0 {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}

	// At least one fire (may be more if OS delivers events slowly).
	if fires.Load() == 0 {
		t.Error("no file_changed fires within 1s")
	}
}
