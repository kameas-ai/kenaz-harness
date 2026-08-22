package sse_test

import (
	"context"
	"errors"
	"testing"

	coremcp "github.com/kameas-ai/kenaz-harness/core/mcp"
	"github.com/kameas-ai/kenaz-harness/core/mcp/transport/sse"
)

// connector-lifecycle-truth-01PMZ303 UNIT-6: the sse.Pool half of the
// same defect http.Pool had — no CloseOne method, so
// dispatch.Pool.CloseOne's sse arm was equally a no-op. Per spec.md
// §1.11 N-6, ZERO shipped recipes use transport:"sse" today, so this
// is parity work with no current user reach — but an empty arm is
// exactly what the mission's planned G-1 gate exists to catch, and
// the fix must not be priced as though it covers half the blast
// radius. These tests reuse the fakeSSEServer fixture from
// connection_test.go (same package) rather than building a second SSE
// server double.
func newSSETestPool(t *testing.T) *sse.Pool {
	t.Helper()
	return sse.NewPool(sse.PoolOptions{})
}

// TestPoolCloseOneClosesConnectionAndStopsRouting is the sse-transport
// AC-004 case: after CloseOne, the server's tool is gone from Tools()
// and Call() returns an error. sse has no per-server probe goroutine
// today (see sse.ErrServerNotFound's doc comment), so there is no
// probe-exit assertion to make here — only the http transport has
// one.
func TestPoolCloseOneClosesConnectionAndStopsRouting(t *testing.T) {
	t.Parallel()
	fake := newFakeSSEServer(t)
	t.Cleanup(fake.srv.Close)

	pool := newSSETestPool(t)
	spec := coremcp.ServerSpec{Name: "srv1", Transport: "sse", URL: fake.streamURL(), PostURL: fake.postURL()}
	if err := pool.Open(context.Background(), []coremcp.ServerSpec{spec}); err != nil {
		t.Fatalf("Open: %v", err)
	}

	// Confirm routing works before CloseOne, so "gone after" is a real
	// assertion and not a fixture that never worked.
	toolsBefore, err := pool.Tools(context.Background())
	if err != nil || len(toolsBefore) != 1 {
		t.Fatalf("Tools before CloseOne = %+v, err=%v; want exactly one tool", toolsBefore, err)
	}

	if err := pool.CloseOne(context.Background(), "srv1"); err != nil {
		t.Fatalf("CloseOne: %v", err)
	}

	toolsAfter, err := pool.Tools(context.Background())
	if err != nil {
		t.Fatalf("Tools after CloseOne: %v", err)
	}
	if len(toolsAfter) != 0 {
		t.Errorf("Tools after CloseOne = %+v, want empty", toolsAfter)
	}
	if _, err := pool.Call(context.Background(), "srv1", "echo", nil); err == nil {
		t.Error("Call after CloseOne: want an error, got nil")
	}
}

// TestPoolCloseOneLeavesOtherServersRunning is the sse-transport
// negative half of AC-004: a teardown that over-deletes is worse than
// one that under-deletes.
func TestPoolCloseOneLeavesOtherServersRunning(t *testing.T) {
	t.Parallel()
	fakeA := newFakeSSEServer(t)
	t.Cleanup(fakeA.srv.Close)
	fakeB := newFakeSSEServer(t)
	t.Cleanup(fakeB.srv.Close)

	pool := newSSETestPool(t)
	// Registered AFTER the two srv.Close cleanups above, so it runs
	// FIRST (t.Cleanup is LIFO): server "b" is deliberately left open
	// by this test (only "a" is closed), and an SSE stream is a
	// long-lived connection — httptest.Server.Close blocks until every
	// connection is idle, so closing the pool before the servers is
	// required to avoid the harness hanging at test-cleanup time on
	// "b"'s still-open stream.
	t.Cleanup(func() { _ = pool.Close(context.Background()) })
	specs := []coremcp.ServerSpec{
		{Name: "a", Transport: "sse", URL: fakeA.streamURL(), PostURL: fakeA.postURL()},
		{Name: "b", Transport: "sse", URL: fakeB.streamURL(), PostURL: fakeB.postURL()},
	}
	if err := pool.Open(context.Background(), specs); err != nil {
		t.Fatalf("Open: %v", err)
	}

	if err := pool.CloseOne(context.Background(), "a"); err != nil {
		t.Fatalf("CloseOne(a): %v", err)
	}

	tools, err := pool.Tools(context.Background())
	if err != nil {
		t.Fatalf("Tools: %v", err)
	}
	if len(tools) != 1 || tools[0].Server != "b" {
		t.Fatalf("Tools after CloseOne(a) = %+v, want exactly server b's tool", tools)
	}
	if _, err := pool.Call(context.Background(), "b", "echo", nil); err != nil {
		t.Errorf("Call(b) after CloseOne(a): unrelated server must still work, got %v", err)
	}
}

// TestPoolCloseOneIdempotentAndSafeBeforeClose pins the AC-004
// idempotence requirement on the sse transport.
func TestPoolCloseOneIdempotentAndSafeBeforeClose(t *testing.T) {
	t.Parallel()
	fake := newFakeSSEServer(t)
	t.Cleanup(fake.srv.Close)

	pool := newSSETestPool(t)
	spec := coremcp.ServerSpec{Name: "srv1", Transport: "sse", URL: fake.streamURL(), PostURL: fake.postURL()}
	if err := pool.Open(context.Background(), []coremcp.ServerSpec{spec}); err != nil {
		t.Fatalf("Open: %v", err)
	}

	if err := pool.CloseOne(context.Background(), "srv1"); err != nil {
		t.Fatalf("first CloseOne: %v", err)
	}
	if err := pool.CloseOne(context.Background(), "srv1"); !errors.Is(err, sse.ErrServerNotFound) {
		t.Errorf("second CloseOne = %v, want ErrServerNotFound", err)
	}
	if err := pool.Close(context.Background()); err != nil {
		t.Errorf("Close after CloseOne: %v", err)
	}
}

// TestPoolCloseOneUnknownServer pins the plain not-found case with no
// server ever opened.
func TestPoolCloseOneUnknownServer(t *testing.T) {
	t.Parallel()
	pool := newSSETestPool(t)
	if err := pool.CloseOne(context.Background(), "never-existed"); !errors.Is(err, sse.ErrServerNotFound) {
		t.Errorf("CloseOne unknown = %v, want ErrServerNotFound", err)
	}
}
