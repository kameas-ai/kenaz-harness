package stdio

import (
	"context"
	"encoding/json"
	"fmt"
	"runtime"
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
		{Name: "bad", Transport: "stdio", Command: []string{"/nonexistent-binary-kaneaz-test"}},
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
