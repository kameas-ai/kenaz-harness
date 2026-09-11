package serve

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"reflect"
	"sync"
	"testing"
)

// recordingShutdowner is a race-safe call recorder shared by fakeAPI and
// fakeCore (CLAUDE.md's "race-safe test fakes" pattern) — go test -race
// runs this in CI and both Shutdown methods could in principle be called
// from goroutines in a future caller.
type recordingShutdowner struct {
	mu    sync.Mutex
	calls []string
}

func (r *recordingShutdowner) record(name string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.calls = append(r.calls, name)
}

func (r *recordingShutdowner) snapshot() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make([]string, len(r.calls))
	copy(out, r.calls)
	return out
}

type fakeAPI struct{ rec *recordingShutdowner }

func (f *fakeAPI) Shutdown() { f.rec.record("api") }

type fakeCore struct {
	rec     *recordingShutdowner
	closeAt error
}

func (f *fakeCore) Shutdown(ctx context.Context) error {
	f.rec.record("core")
	return f.closeAt
}

// TestShutdownServedCore_CallsBothInOrder is #70's proof: served mode
// used to call neither api.Shutdown() nor core.Core.Shutdown() on exit.
// This asserts both run, and specifically that api.Shutdown() (which
// stops background pollers that may still call into storage/MCP) runs
// BEFORE core.Core.Shutdown() (which closes that storage/MCP) — the
// inverted order would risk a poller calling into an already-closed
// handle mid-drain.
func TestShutdownServedCore_CallsBothInOrder(t *testing.T) {
	rec := &recordingShutdowner{}
	log := slog.New(slog.NewTextHandler(io.Discard, nil))

	ShutdownServedCore(context.Background(), &fakeAPI{rec: rec}, &fakeCore{rec: rec}, log, "test")

	got := rec.snapshot()
	want := []string{"api", "core"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("ShutdownServedCore call sequence = %v, want %v (api.Shutdown() before core.Core.Shutdown())", got, want)
	}
}

// TestShutdownServedCore_CoreErrorIsNonFatal pins that a core.Shutdown
// error is logged, not propagated as a panic/fatal — main.go and
// cmd/harness-served/main.go both continue on to their own os.Exit(1)
// decision based on serveErr, independent of this error.
func TestShutdownServedCore_CoreErrorIsNonFatal(t *testing.T) {
	rec := &recordingShutdowner{}
	log := slog.New(slog.NewTextHandler(io.Discard, nil))

	// Must not panic.
	ShutdownServedCore(context.Background(), &fakeAPI{rec: rec}, &fakeCore{rec: rec, closeAt: errors.New("boom")}, log, "test")

	got := rec.snapshot()
	want := []string{"api", "core"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("ShutdownServedCore call sequence = %v, want %v", got, want)
	}
}
