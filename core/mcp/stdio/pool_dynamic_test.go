package stdio

import (
	"context"
	"errors"
	"os"
	"runtime"
	"syscall"
	"testing"
	"time"

	coremcp "github.com/sigil-tech/kaneaz-harness/core/mcp"
)

// TestPool_OpenOne_HappyPath spawns one server through the dynamic
// add path and verifies its tools land in the aggregated Tools()
// list. Mirrors the Open(...) path but exercises the WP05 single-
// add API rpc/views/tools/InstallRecipe consumes.
func TestPool_OpenOne_HappyPath(t *testing.T) {
	t.Parallel()
	bin := buildFakeServer(t)
	p := NewPool(PoolOptions{})
	defer p.Close(context.Background())

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	spec := coremcp.ServerSpec{Name: "alpha", Transport: "stdio", Command: []string{bin}}
	if err := p.OpenOne(ctx, spec); err != nil {
		t.Fatalf("OpenOne: %v", err)
	}
	tools, err := p.Tools(ctx)
	if err != nil {
		t.Fatalf("Tools: %v", err)
	}
	if len(tools) != 2 {
		t.Fatalf("tools = %d, want 2", len(tools))
	}
	for _, tool := range tools {
		if tool.Server != "alpha" {
			t.Fatalf("tool.Server = %q, want %q", tool.Server, "alpha")
		}
	}
}

// TestPool_OpenOne_DuplicateID rejects a second OpenOne against the
// same ID with ErrServerExists. The rpc view's InstallRecipe path
// relies on this so a buggy double-toggle does not orphan the first
// process.
func TestPool_OpenOne_DuplicateID(t *testing.T) {
	t.Parallel()
	bin := buildFakeServer(t)
	p := NewPool(PoolOptions{})
	defer p.Close(context.Background())

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	spec := coremcp.ServerSpec{Name: "alpha", Transport: "stdio", Command: []string{bin}}
	if err := p.OpenOne(ctx, spec); err != nil {
		t.Fatalf("OpenOne#1: %v", err)
	}
	err := p.OpenOne(ctx, spec)
	if !errors.Is(err, ErrServerExists) {
		t.Fatalf("OpenOne#2 err = %v, want ErrServerExists", err)
	}
}

// TestPool_CloseOne_KillsSubprocess verifies CloseOne reaps the
// child process within the pool's close-grace budget. Asserts the
// subprocess is no longer signal-able (process.Signal(0) reports
// process-done) within 2.5 s of CloseOne — A2 from the spec.
func TestPool_CloseOne_KillsSubprocess(t *testing.T) {
	t.Parallel()
	bin := buildFakeServer(t)
	p := NewPool(PoolOptions{})
	defer p.Close(context.Background())

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	spec := coremcp.ServerSpec{Name: "alpha", Transport: "stdio", Command: []string{bin}}
	if err := p.OpenOne(ctx, spec); err != nil {
		t.Fatalf("OpenOne: %v", err)
	}
	inst := p.Server("alpha")
	if inst == nil {
		t.Fatalf("Server(alpha) = nil after OpenOne")
	}
	inst.lifecycleMu.Lock()
	cmd := inst.cmd
	inst.lifecycleMu.Unlock()
	if cmd == nil || cmd.Process == nil {
		t.Fatalf("server has no live process after OpenOne")
	}
	pid := cmd.Process.Pid

	if err := p.CloseOne(context.Background(), "alpha"); err != nil {
		t.Fatalf("CloseOne: %v", err)
	}

	// After CloseOne the pool should not know the id anymore.
	if got := p.Server("alpha"); got != nil {
		t.Fatalf("Server(alpha) = %v after CloseOne, want nil", got)
	}

	// Confirm the OS reaped the child within 2.5 s. Signal(0) on a
	// reaped child returns os.ErrProcessDone; on macOS / Linux the
	// kernel keeps the entry until Wait, which Close already ran.
	deadline := time.Now().Add(2500 * time.Millisecond)
	for time.Now().Before(deadline) {
		err := cmd.Process.Signal(syscall.Signal(0))
		if errors.Is(err, os.ErrProcessDone) {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("subprocess pid=%d still signalable 2.5s after CloseOne", pid)
}

// TestPool_CloseOne_UnknownID returns ErrServerNotFound for an id
// that was never installed. The rpc view treats this as the "already
// gone" outcome so a double-Uninstall is non-fatal.
func TestPool_CloseOne_UnknownID(t *testing.T) {
	t.Parallel()
	p := NewPool(PoolOptions{})
	defer p.Close(context.Background())
	err := p.CloseOne(context.Background(), "ghost")
	if !errors.Is(err, ErrServerNotFound) {
		t.Fatalf("CloseOne(ghost) err = %v, want ErrServerNotFound", err)
	}
}

// TestPool_OpenCloseOne_NoGoroutineLeak covers NFR-002 across the
// dynamic add/remove path: snapshot the runtime's goroutine count,
// run an OpenOne+CloseOne cycle, allow 100 ms for tear-down, and
// verify the count returns to within ±2 of baseline.
func TestPool_OpenCloseOne_NoGoroutineLeak(t *testing.T) {
	t.Parallel()
	bin := buildFakeServer(t)
	baseline := runtime.NumGoroutine()
	p := NewPool(PoolOptions{})
	defer p.Close(context.Background())

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	spec := coremcp.ServerSpec{Name: "alpha", Transport: "stdio", Command: []string{bin}}
	if err := p.OpenOne(ctx, spec); err != nil {
		t.Fatalf("OpenOne: %v", err)
	}
	if err := p.CloseOne(context.Background(), "alpha"); err != nil {
		t.Fatalf("CloseOne: %v", err)
	}

	deadline := time.Now().Add(200 * time.Millisecond)
	for time.Now().Before(deadline) {
		got := runtime.NumGoroutine()
		if got <= baseline+2 {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("goroutines = %d after OpenOne+CloseOne, baseline %d", runtime.NumGoroutine(), baseline)
}

