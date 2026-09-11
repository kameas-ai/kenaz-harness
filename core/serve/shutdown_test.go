package serve

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"reflect"
	"sync"
	"testing"
	"time"
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

// drainingCore simulates the shape core.Core.Shutdown's real telemetry
// and fleet-OTLP flush paths have: `select { case <-drainDone: ...;
// case <-ctx.Done(): return ctx.Err() }` (this is also exactly how the
// OTel SDK's own BatchSpanProcessor.Shutdown behaves — c.Shutdown here
// isn't inventing a pattern, it's reproducing the one that made the real
// bug possible). If the ctx handed to Shutdown is already Done, the
// select takes that branch immediately and the drain never happens; if
// Shutdown gets a ctx with real time left, the drain completes and sets
// drained=true.
//
// Not a race-fake needing a mutex per CLAUDE.md's race-safe-fakes
// pattern: drained is written and read from the SAME goroutine (the
// caller's — Shutdown blocks on the select before returning, and
// ShutdownServedCore blocks on Shutdown before returning), so there is
// no genuine cross-goroutine access to the field itself. The spawned
// goroutine below only ever touches the drainDone channel, which is
// safe for cross-goroutine signalling by construction.
type drainingCore struct {
	drainFor time.Duration
	drained  bool
}

func (d *drainingCore) Shutdown(ctx context.Context) error {
	drainDone := make(chan struct{})
	go func() {
		time.Sleep(d.drainFor)
		close(drainDone)
	}()
	select {
	case <-drainDone:
		d.drained = true
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

// TestShutdownServedCore_FlushSurvivesAlreadyCancelledCtx is the proof
// for the ctx-reuse defect the review found: both real served-mode
// entry points (main.go's runServeMode, cmd/harness-served's main)
// build ctx via context.WithCancel(context.Background()) and cancel it
// from their own SIGTERM/SIGINT handler to unblock srv.Serve(ctx) —
// then call ShutdownServedCore with that SAME, now-Done ctx. Before the
// fix, ShutdownServedCore passed that ctx straight through to
// c.Shutdown(ctx), so core.Core.Shutdown's telemetry and fleet-OTLP
// pipeline flushes (which select on ctx.Done() exactly like
// drainingCore above) took the already-fired cancelled branch instantly
// and returned context.Canceled without draining anything — silently,
// on every real SIGTERM, which is the opposite of what #70's commit
// message claimed ("telemetry flush" on shutdown).
//
// Passing an ALREADY-CANCELLED ctx into ShutdownServedCore and asserting
// drainingCore.Shutdown still completes its (simulated) drain is exactly
// what distinguishes "ShutdownServedCore gives c.Shutdown a fresh,
// independently-bounded context" from "ShutdownServedCore forwards
// whatever ctx it was given". Mutating shutdown.go back to
// `c.Shutdown(ctx)` (the pre-fix line) makes this test fail — see the
// commit message / PR description for the actual `go test -run` output
// from that mutation.
func TestShutdownServedCore_FlushSurvivesAlreadyCancelledCtx(t *testing.T) {
	dc := &drainingCore{drainFor: 50 * time.Millisecond}
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	rec := &recordingShutdowner{}

	cancelledCtx, cancel := context.WithCancel(context.Background())
	cancel() // simulate the SIGTERM handler having already cancelled it, as both real call sites do

	ShutdownServedCore(cancelledCtx, &fakeAPI{rec: rec}, dc, log, "test")

	if !dc.drained {
		t.Fatalf("ShutdownServedCore did not let core.Shutdown's simulated flush complete when given " +
			"an already-cancelled ctx — it must be forwarding the caller's cancellation straight into " +
			"c.Shutdown(), so the select inside took the ctx.Done() branch instead of waiting for the " +
			"drain. ShutdownServedCore must derive a fresh, independently-bounded context for the " +
			"c.Shutdown() call instead of reusing the ctx it was handed.")
	}
}
