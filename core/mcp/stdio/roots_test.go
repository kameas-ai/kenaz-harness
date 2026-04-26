package stdio

import (
	"context"
	"encoding/json"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

// stubRootsHandler is a minimal RootsHandler used to assert the
// reader-loop dispatch path actually invokes a registered handler.
type stubRootsHandler struct {
	calls atomic.Int32
	roots []Root
}

func (s *stubRootsHandler) Roots(_ context.Context, _ string) ([]Root, error) {
	s.calls.Add(1)
	return s.roots, nil
}

func TestDefaultRoots_DataDirOnly(t *testing.T) {
	t.Parallel()
	h := DefaultRoots("/tmp/kaneaz", nil)
	got, err := h.Roots(context.Background(), "any-server")
	if err != nil {
		t.Fatalf("Roots: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("roots = %d, want 1", len(got))
	}
	if got[0].URI != "file:///tmp/kaneaz" {
		t.Fatalf("uri = %q", got[0].URI)
	}
}

func TestDefaultRoots_WithProjectRoot(t *testing.T) {
	t.Parallel()
	h := DefaultRoots("/tmp/data", func() string { return "/home/me/proj" })
	got, _ := h.Roots(context.Background(), "any-server")
	if len(got) != 2 {
		t.Fatalf("roots = %d, want 2", len(got))
	}
	uris := []string{got[0].URI, got[1].URI}
	want := []string{"file:///tmp/data", "file:///home/me/proj"}
	for i, w := range want {
		if uris[i] != w {
			t.Fatalf("uri[%d] = %q, want %q", i, uris[i], w)
		}
	}
}

func TestDefaultRoots_NilProjectFn(t *testing.T) {
	t.Parallel()
	h := DefaultRoots("/d", nil)
	got, _ := h.Roots(context.Background(), "x")
	if len(got) != 1 {
		t.Fatalf("got %d roots", len(got))
	}
}

func TestDefaultRoots_EmptyProjectPath(t *testing.T) {
	t.Parallel()
	h := DefaultRoots("/d", func() string { return "" })
	got, _ := h.Roots(context.Background(), "x")
	if len(got) != 1 {
		t.Fatalf("got %d roots; empty project path should be skipped", len(got))
	}
}

// TestRoots_ServerInitiatedDispatch drives a fake server that emits a
// roots/list request after initialize. The host must answer through
// the registered handler and the response must reach the server's
// reader loop without crashing it.
func TestRoots_ServerInitiatedDispatch(t *testing.T) {
	t.Parallel()
	bin := buildFakeServer(t)

	stub := &stubRootsHandler{
		roots: []Root{
			{URI: "file:///tmp/data", Name: "data"},
			{URI: "file:///proj", Name: "project"},
		},
	}
	inst := newServerInstance("fake", nil, nil, stub, nil, instanceOptions{})

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := inst.Spawn(ctx, SpawnSpec{
		ID:      "fake",
		Command: []string{bin, "--emit-roots-list"},
	}); err != nil {
		t.Fatalf("Spawn: %v", err)
	}
	defer inst.Close(context.Background())

	// The fake emits roots/list asynchronously after initialize; wait
	// for the handler to be invoked.
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if stub.calls.Load() > 0 {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("RootsHandler never invoked")
}

// TestRoots_ResponseShape sanity-checks the JSON encoding of the
// RootsListResult — not strictly a unit test of the dispatch path
// but cheap insurance against a struct-tag drift.
func TestRoots_ResponseShape(t *testing.T) {
	t.Parallel()
	res := RootsListResult{Roots: []Root{{URI: "file:///x", Name: "x"}}}
	b, err := json.Marshal(res)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	s := string(b)
	if !strings.Contains(s, `"roots"`) || !strings.Contains(s, `"file:///x"`) {
		t.Fatalf("unexpected json: %s", s)
	}
}
