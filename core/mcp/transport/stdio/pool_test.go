package stdio

import (
	"context"
	"encoding/json"
	"fmt"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	coremcp "github.com/kameas-ai/kenaz-harness/core/mcp"
)

func TestPool_OpenAndTools(t *testing.T) {
	t.Parallel()
	bin := buildFakeServer(t)
	p := NewPool(PoolOptions{})

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := p.Open(ctx, []coremcp.ServerSpec{
		{Name: "alpha", Transport: "stdio", Command: []string{bin}},
		{Name: "beta", Transport: "stdio", Command: []string{bin}},
	}); err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer p.Close(context.Background())

	tools, err := p.Tools(ctx)
	if err != nil {
		t.Fatalf("Tools: %v", err)
	}
	// Each fake exposes 2 tools, so 4 total. Server names propagate
	// through coremcp.Tool.Server.
	if len(tools) != 4 {
		t.Fatalf("tools = %d, want 4", len(tools))
	}
	servers := map[string]int{}
	for _, tool := range tools {
		servers[tool.Server]++
	}
	if servers["alpha"] != 2 || servers["beta"] != 2 {
		t.Fatalf("server distribution = %v", servers)
	}
}

func TestPool_ConcurrentCalls(t *testing.T) {
	t.Parallel()
	bin := buildFakeServer(t)
	p := NewPool(PoolOptions{})

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	if err := p.Open(ctx, []coremcp.ServerSpec{
		{Name: "alpha", Transport: "stdio", Command: []string{bin}},
		{Name: "beta", Transport: "stdio", Command: []string{bin}},
	}); err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer p.Close(context.Background())

	const n = 50
	var wg sync.WaitGroup
	wg.Add(n)
	results := make([]error, n)
	for i := 0; i < n; i++ {
		i := i
		server := "alpha"
		if i%2 == 1 {
			server = "beta"
		}
		go func() {
			defer wg.Done()
			args := json.RawMessage(fmt.Sprintf(`{"i":%d}`, i))
			_, err := p.Call(ctx, server, "fake_echo", args)
			results[i] = err
		}()
	}
	wg.Wait()
	for i, err := range results {
		if err != nil {
			t.Fatalf("call %d: %v", i, err)
		}
	}
}

func TestPool_ClosedRejects(t *testing.T) {
	t.Parallel()
	bin := buildFakeServer(t)
	p := NewPool(PoolOptions{})
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := p.Open(ctx, []coremcp.ServerSpec{
		{Name: "alpha", Transport: "stdio", Command: []string{bin}},
	}); err != nil {
		t.Fatalf("Open: %v", err)
	}
	if err := p.Close(context.Background()); err != nil && !isExpectedExit(err) {
		t.Fatalf("Close: %v", err)
	}
	if _, err := p.Tools(ctx); err == nil {
		t.Fatalf("Tools after Close: want error")
	}
	if _, err := p.Call(ctx, "alpha", "fake_echo", nil); err == nil {
		t.Fatalf("Call after Close: want error")
	}
}

func TestPool_BadSpecDoesNotPoison(t *testing.T) {
	t.Parallel()
	bin := buildFakeServer(t)
	p := NewPool(PoolOptions{})
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	err := p.Open(ctx, []coremcp.ServerSpec{
		{Name: "good", Transport: "stdio", Command: []string{bin}},
		{Name: "bad", Transport: "stdio", Command: []string{"/nonexistent-binary-kenaz-test"}},
	})
	if err == nil {
		t.Fatalf("Open: expected partial failure, got nil")
	}
	defer p.Close(context.Background())

	tools, err := p.Tools(ctx)
	if err != nil {
		t.Fatalf("Tools: %v", err)
	}
	if len(tools) != 2 {
		t.Fatalf("tools = %d want 2 (good only)", len(tools))
	}
}

func TestPool_GoroutinesReturnToBaseline(t *testing.T) {
	t.Parallel()
	bin := buildFakeServer(t)
	baseline := runtime.NumGoroutine()
	p := NewPool(PoolOptions{})
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := p.Open(ctx, []coremcp.ServerSpec{
		{Name: "alpha", Transport: "stdio", Command: []string{bin}},
		{Name: "beta", Transport: "stdio", Command: []string{bin}},
	}); err != nil {
		t.Fatalf("Open: %v", err)
	}
	if err := p.Close(context.Background()); err != nil && !isExpectedExit(err) {
		t.Fatalf("Close: %v", err)
	}
	deadline := time.Now().Add(200 * time.Millisecond)
	for time.Now().Before(deadline) {
		if got := runtime.NumGoroutine(); got <= baseline+2 {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("goroutines = %d, baseline %d", runtime.NumGoroutine(), baseline)
}

func TestPool_CompileTimeImplementsMcpPool(t *testing.T) {
	t.Parallel()
	var _ coremcp.Pool = (*Pool)(nil)
}

// TestPool_ServerSpecInitTimeoutMs_OverridesPoolDefault is
// connector-lifecycle-truth-01PMZ303 UNIT-12 / AC-008's wire-branch
// assertion: a recipe's declared init_timeout_ms must reach the actual
// initialize deadline the connection applies, not just get copied into
// another struct field along the way (the "non-consumption" false-pass
// CLAUDE.md warns about — and spec.md §7 AC-008 warns about explicitly
// for this exact finding).
//
// Drives coremcp.ServerSpec.InitTimeoutMs through the real Pool.Open path
// (not SpawnSpec directly — that would only prove the field exists, not
// that the pool wires it) against a --no-init fake server, and asserts
// the resulting error surfaces the short custom deadline (250ms) rather
// than the pool-wide default (5s, which the 10s test context would allow
// to complete uninterrupted — proving the override actually shortened
// the deadline, not just that SOME timeout eventually fired).
//
// Mutation: revert openOne's initTimeout/pingPeriod override so it always
// uses p.opts.InitTimeout regardless of spec.InitTimeoutMs. Must fail —
// the pool-wide default of 0 (unset PoolOptions) falls back to
// transport.DefaultInitTimeout (5s), so Open would not return within this
// test's shorter deadline.
func TestPool_ServerSpecInitTimeoutMs_OverridesPoolDefault(t *testing.T) {
	t.Parallel()
	bin := buildFakeServer(t)
	p := NewPool(PoolOptions{})

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	start := time.Now()
	err := p.Open(ctx, []coremcp.ServerSpec{
		{
			Name:          "slow",
			Transport:     "stdio",
			Command:       []string{bin, "--no-init"},
			InitTimeoutMs: 250,
		},
	})
	elapsed := time.Since(start)
	if err == nil {
		t.Fatalf("Open: want an initialize-timeout error, got nil")
	}
	if !strings.Contains(err.Error(), "initialize timeout") {
		t.Fatalf("Open err = %v, want an initialize timeout error", err)
	}
	// Generous upper bound (1.5s) to absorb CI scheduling jitter while
	// still being far short of the 5s pool-wide default — if the
	// override were not applied, this would either hit the 2s test ctx
	// deadline (a different error) or hang past it.
	if elapsed > 1500*time.Millisecond {
		t.Fatalf("Open took %v to fail; want well under the 5s pool default, close to the 250ms override", elapsed)
	}
}
