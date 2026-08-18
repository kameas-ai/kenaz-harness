package rpc

// api_update_construction_test.go — Layer 1 regression guard for v0.4.4.
//
// v0.4.0–v0.4.2 silently skipped wiring the update service because
// main.go never set Options.BuildVersion, so BuildVersion() returned "".
// The gate at line ~800 of api.go checked BuildVersion != "" and
// short-circuited without any log noise.
//
// These tests boot the REAL chassis (rpc.New), assert the update fields
// are wired when both DataDir and BuildVersion are present, and assert
// the "update.service.skipped" WARN log fires when they are absent.

import (
	"bytes"
	"log/slog"
	"strings"
	"sync"
	"testing"

	"github.com/kameas-ai/kenaz-harness/core"
	"github.com/kameas-ai/kenaz-harness/core/logging"
)

// syncBuffer wraps bytes.Buffer with a mutex so it is safe as an
// io.Writer target for a slog.Handler invoked from more than one
// goroutine at once — e.g. stdio.Pool.Open's errgroup-based concurrent
// per-spec spawns (mcp-connector-lifecycle-01PMMC01 WP01: found by
// go test -race flaking on TestMCPUserSource_BootstrapAndToolsListAgree,
// which drives a real c.Start(ctx) recipe bootstrap through captureLog).
// slog's own commonHandler already serialises Handle() calls through its
// own mutex, but that guards the Handler, not this buffer directly — two
// concurrently-constructed Handler values derived from the same parent
// (WithAttrs/WithGroup child handlers, which the bootstrap's per-recipe
// logging produces) can still race on the underlying io.Writer without
// this. Per CLAUDE.md's "Race-safe test fakes": anything written from a
// goroutine and read by the test body needs a mutex + snapshot helper.
type syncBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (b *syncBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.Write(p)
}

func (b *syncBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.String()
}

// captureLog replaces the process-global logger with a buffer-backed
// handler for the duration of f, then restores whatever was there before.
// Returns all JSON-line output produced during f. Safe for f to log from
// background goroutines (see syncBuffer).
func captureLog(t *testing.T, f func()) string {
	t.Helper()
	buf := &syncBuffer{}
	h := slog.NewJSONHandler(buf, &slog.HandlerOptions{Level: slog.LevelDebug})
	logging.Replace(h)
	t.Cleanup(func() {
		// Re-init to the default file handler on teardown.
		// L() already called once.Do so we just restore via Replace.
		logging.Replace(logging.FileHandler())
	})
	f()
	return buf.String()
}

// TestUpdateConstruction_PositiveCase exercises the happy path: New with
// a real Core that has both DataDir and BuildVersion set must wire
// updateSvc and updateAPI (not nil).
//
// This test WOULD HAVE CAUGHT the v0.4.0 bug: without BuildVersion set,
// both fields stay nil.
func TestUpdateConstruction_PositiveCase(t *testing.T) {
	c, err := core.New(core.Options{
		DataDir:      t.TempDir(),
		BuildVersion: "v0.4.4-test",
	})
	if err != nil {
		t.Fatalf("core.New: %v", err)
	}

	logs := captureLog(t, func() {
		api := New(c)
		if api.updateSvc == nil {
			t.Error("updateSvc is nil — update service was not wired; check BuildVersion/DataDir gate in api.go")
		}
		if api.updateAPI == nil {
			t.Error("updateAPI is nil — update view was not wired; check BuildVersion/DataDir gate in api.go")
		}
	})

	// Also assert the positive log line fired.
	if !strings.Contains(logs, "update.service.init_ok") {
		t.Errorf("expected 'update.service.init_ok' log line; got:\n%s", logs)
	}
}

// TestUpdateConstruction_NegativeCase_EmptyBuildVersion asserts that when
// the chassis is built with a Core that has a non-empty DataDir but an
// EMPTY BuildVersion, the update service is skipped AND the loud WARN
// log "update.service.skipped" is emitted with build_version_empty=true.
//
// This covers the exact failure mode of v0.4.0–v0.4.2.
func TestUpdateConstruction_NegativeCase_EmptyBuildVersion(t *testing.T) {
	c, err := core.New(core.Options{
		DataDir:      t.TempDir(),
		BuildVersion: "", // intentionally empty — simulates the v0.4.0 bug
	})
	if err != nil {
		t.Fatalf("core.New: %v", err)
	}

	logs := captureLog(t, func() {
		api := New(c)
		if api.updateSvc != nil {
			t.Error("updateSvc should be nil when BuildVersion is empty")
		}
		if api.updateAPI != nil {
			t.Error("updateAPI should be nil when BuildVersion is empty")
		}
	})

	if !strings.Contains(logs, "update.service.skipped") {
		t.Errorf("expected 'update.service.skipped' WARN log; got:\n%s", logs)
	}
	if !strings.Contains(logs, "build_version_empty") {
		t.Errorf("expected 'build_version_empty' attr in log; got:\n%s", logs)
	}
}

// TestUpdateConstruction_NegativeCase_NilCore asserts that New(nil)
// (test-chassis path) also emits "update.service.skipped" with core_nil=true.
func TestUpdateConstruction_NegativeCase_NilCore(t *testing.T) {
	logs := captureLog(t, func() {
		api := New(nil)
		if api.updateSvc != nil {
			t.Error("updateSvc should be nil for nil-core chassis")
		}
		if api.updateAPI != nil {
			t.Error("updateAPI should be nil for nil-core chassis")
		}
	})

	if !strings.Contains(logs, "update.service.skipped") {
		t.Errorf("expected 'update.service.skipped' WARN log; got:\n%s", logs)
	}
	if !strings.Contains(logs, "core_nil") {
		t.Errorf("expected 'core_nil' attr in log; got:\n%s", logs)
	}
}
