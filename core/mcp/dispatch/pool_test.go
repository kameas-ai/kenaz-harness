package dispatch_test

import (
	"context"
	"encoding/json"
	"errors"
	"sync"
	"testing"
	"time"

	coremcp "github.com/kameas-ai/kenaz-harness/core/mcp"
	"github.com/kameas-ai/kenaz-harness/core/mcp/dispatch"
	"github.com/kameas-ai/kenaz-harness/core/mcp/transport/stdio"
)

// ─── fake stdio sub-pool ─────────────────────────────────────────────────────

// fakeStdioPool is a test double satisfying dispatch.StdioSubPool.
// It records which server names were opened/closed/called so routing
// assertions can verify the correct sub-pool was selected.
type fakeStdioPool struct {
	mu      sync.Mutex
	opened  []string
	closed  bool
	callErr error
}

func (f *fakeStdioPool) Open(_ context.Context, specs []coremcp.ServerSpec) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, s := range specs {
		f.opened = append(f.opened, s.Name)
	}
	return nil
}
func (f *fakeStdioPool) OpenOne(_ context.Context, spec coremcp.ServerSpec) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.opened = append(f.opened, spec.Name)
	return nil
}
func (f *fakeStdioPool) Close(_ context.Context) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.closed = true
	return nil
}
func (f *fakeStdioPool) CloseOne(_ context.Context, id string) error {
	return nil // best-effort for tests
}
func (f *fakeStdioPool) Tools(_ context.Context) ([]coremcp.Tool, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	var out []coremcp.Tool
	for _, name := range f.opened {
		out = append(out, coremcp.Tool{Server: name, Name: "tool1"})
	}
	return out, nil
}
func (f *fakeStdioPool) Call(_ context.Context, _, _ string, _ json.RawMessage) (json.RawMessage, error) {
	if f.callErr != nil {
		return nil, f.callErr
	}
	return json.RawMessage(`"ok"`), nil
}
func (f *fakeStdioPool) RecipeStatus(id string) (stdio.RecipeStatus, bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, n := range f.opened {
		if n == id {
			return stdio.RecipeStatus{
				ID:        id,
				Enabled:   true,
				State:     "running",
				UpdatedAt: time.Now().UTC(),
			}, true
		}
	}
	return stdio.RecipeStatus{}, false
}
func (f *fakeStdioPool) ServerTools(_ string) []coremcp.Tool { return nil }
func (f *fakeStdioPool) AllRecipeStatuses() []stdio.RecipeStatus {
	f.mu.Lock()
	defer f.mu.Unlock()
	var out []stdio.RecipeStatus
	for _, n := range f.opened {
		out = append(out, stdio.RecipeStatus{ID: n, Enabled: true, State: "running"})
	}
	return out
}

// snapshot returns a copy of opened for safe inspection.
func (f *fakeStdioPool) snapshot() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make([]string, len(f.opened))
	copy(out, f.opened)
	return out
}

// compile-time check: fakeStdioPool satisfies dispatch.StdioSubPool.
var _ dispatch.StdioSubPool = (*fakeStdioPool)(nil)

// ─── fake generic sub-pool ───────────────────────────────────────────────────

// fakeGenericPool satisfies coremcp.Pool for http/sse tests.
type fakeGenericPool struct {
	mu      sync.Mutex
	opened  []string
	closed  bool
	callErr error
}

func (f *fakeGenericPool) Open(_ context.Context, specs []coremcp.ServerSpec) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, s := range specs {
		f.opened = append(f.opened, s.Name)
	}
	return nil
}
func (f *fakeGenericPool) Close(_ context.Context) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.closed = true
	return nil
}
func (f *fakeGenericPool) Tools(_ context.Context) ([]coremcp.Tool, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	var out []coremcp.Tool
	for _, name := range f.opened {
		out = append(out, coremcp.Tool{Server: name, Name: "tool1"})
	}
	return out, nil
}
func (f *fakeGenericPool) Call(_ context.Context, _, _ string, _ json.RawMessage) (json.RawMessage, error) {
	if f.callErr != nil {
		return nil, f.callErr
	}
	return json.RawMessage(`"ok"`), nil
}

func (f *fakeGenericPool) snapshot() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make([]string, len(f.opened))
	copy(out, f.opened)
	return out
}

// newTestPool builds a dispatch.Pool wired with fake sub-pools and
// returns the pool plus the three fakes for assertion.
func newTestPool() (d *dispatch.Pool, sf *fakeStdioPool, hf *fakeGenericPool, ef *fakeGenericPool) {
	sf = &fakeStdioPool{}
	hf = &fakeGenericPool{}
	ef = &fakeGenericPool{}
	d = dispatch.NewForTest(dispatch.TestOptions{
		Stdio: sf,
		HTTP:  hf,
		SSE:   ef,
	})
	return d, sf, hf, ef
}

// ─── compile-time surface check ──────────────────────────────────────────────

var _ coremcp.Pool = (*dispatch.Pool)(nil)

// ─── routing tests ───────────────────────────────────────────────────────────

// TestDispatch_StdioRecipeRoutedToStdioPool verifies that a spec with
// Transport=="" (the legacy / default) is opened on the stdio sub-pool.
func TestDispatch_StdioRecipeRoutedToStdioPool(t *testing.T) {
	t.Parallel()
	d, sf, hf, ef := newTestPool()

	spec := coremcp.ServerSpec{Name: "brave-search", Transport: ""}
	if err := d.OpenOne(context.Background(), spec); err != nil {
		t.Fatalf("OpenOne: %v", err)
	}

	if names := sf.snapshot(); len(names) != 1 || names[0] != "brave-search" {
		t.Errorf("stdio pool opened %v; want [brave-search]", names)
	}
	if names := hf.snapshot(); len(names) != 0 {
		t.Errorf("http pool unexpectedly opened %v", names)
	}
	if names := ef.snapshot(); len(names) != 0 {
		t.Errorf("sse pool unexpectedly opened %v", names)
	}
}

// TestDispatch_HTTPRecipeRoutedToHTTPPool verifies that a spec with
// Transport=="http" is opened on the http sub-pool.
func TestDispatch_HTTPRecipeRoutedToHTTPPool(t *testing.T) {
	t.Parallel()
	d, sf, hf, ef := newTestPool()

	spec := coremcp.ServerSpec{Name: "github", Transport: "http", URL: "https://api.githubcopilot.com/mcp/"}
	if err := d.OpenOne(context.Background(), spec); err != nil {
		t.Fatalf("OpenOne: %v", err)
	}

	if names := hf.snapshot(); len(names) != 1 || names[0] != "github" {
		t.Errorf("http pool opened %v; want [github]", names)
	}
	if names := sf.snapshot(); len(names) != 0 {
		t.Errorf("stdio pool unexpectedly opened %v", names)
	}
	if names := ef.snapshot(); len(names) != 0 {
		t.Errorf("sse pool unexpectedly opened %v", names)
	}
}

// TestDispatch_SSERecipeRoutedToSSEPool verifies that a spec with
// Transport=="sse" is opened on the sse sub-pool.
func TestDispatch_SSERecipeRoutedToSSEPool(t *testing.T) {
	t.Parallel()
	d, sf, hf, ef := newTestPool()

	spec := coremcp.ServerSpec{Name: "my-sse", Transport: "sse", URL: "http://localhost:9999/sse"}
	if err := d.OpenOne(context.Background(), spec); err != nil {
		t.Fatalf("OpenOne: %v", err)
	}

	if names := ef.snapshot(); len(names) != 1 || names[0] != "my-sse" {
		t.Errorf("sse pool opened %v; want [my-sse]", names)
	}
	if names := sf.snapshot(); len(names) != 0 {
		t.Errorf("stdio pool unexpectedly opened %v", names)
	}
	if names := hf.snapshot(); len(names) != 0 {
		t.Errorf("http pool unexpectedly opened %v", names)
	}
}

// TestDispatch_CallRoutedToOwnerSubPool verifies that Call is dispatched
// to the sub-pool that originally opened the server.
func TestDispatch_CallRoutedToOwnerSubPool(t *testing.T) {
	t.Parallel()
	d, sf, hf, _ := newTestPool()

	// Mark the http fake to return a sentinel error so we can tell if
	// Call reached it.
	httpErr := errors.New("http_sentinel")
	hf.callErr = httpErr

	_ = d.OpenOne(context.Background(), coremcp.ServerSpec{Name: "stdio-srv", Transport: ""})
	_ = d.OpenOne(context.Background(), coremcp.ServerSpec{Name: "http-srv", Transport: "http", URL: "http://x"})

	// Call the stdio server — should succeed (sf.callErr is nil).
	_, err := d.Call(context.Background(), "stdio-srv", "t", nil)
	if err != nil {
		t.Errorf("Call stdio-srv: unexpected error %v", err)
	}

	// Call the http server — should return the sentinel error.
	_, err = d.Call(context.Background(), "http-srv", "t", nil)
	if !errors.Is(err, httpErr) {
		t.Errorf("Call http-srv: want httpErr, got %v", err)
	}

	// Call an unknown server — should return ErrServerNotFound.
	_, err = d.Call(context.Background(), "no-such", "t", nil)
	if !errors.Is(err, stdio.ErrServerNotFound) {
		t.Errorf("Call no-such: want ErrServerNotFound, got %v", err)
	}

	_ = sf
}

// TestDispatch_CloseOneRemovedFromOwnership verifies that CloseOne removes
// the server from the ownership table so subsequent calls return
// ErrServerNotFound.
func TestDispatch_CloseOneRemovedFromOwnership(t *testing.T) {
	t.Parallel()
	d, _, _, _ := newTestPool()

	_ = d.OpenOne(context.Background(), coremcp.ServerSpec{Name: "srv", Transport: ""})

	// CloseOne an unknown id returns ErrServerNotFound.
	if err := d.CloseOne(context.Background(), "unknown"); !errors.Is(err, stdio.ErrServerNotFound) {
		t.Errorf("CloseOne unknown: want ErrServerNotFound, got %v", err)
	}

	// CloseOne a known id succeeds.
	if err := d.CloseOne(context.Background(), "srv"); err != nil {
		t.Errorf("CloseOne srv: %v", err)
	}

	// After close, Call returns ErrServerNotFound.
	_, err := d.Call(context.Background(), "srv", "t", nil)
	if !errors.Is(err, stdio.ErrServerNotFound) {
		t.Errorf("Call after CloseOne: want ErrServerNotFound, got %v", err)
	}
}

// TestDispatch_RecipeStatusStdio verifies that RecipeStatus delegates to
// the stdio pool for stdio-owned servers.
func TestDispatch_RecipeStatusStdio(t *testing.T) {
	t.Parallel()
	d, _, _, _ := newTestPool()

	_ = d.OpenOne(context.Background(), coremcp.ServerSpec{Name: "stdio-srv", Transport: ""})

	rs, ok := d.RecipeStatus("stdio-srv")
	if !ok {
		t.Fatal("RecipeStatus stdio-srv: want ok=true")
	}
	if rs.ID != "stdio-srv" {
		t.Errorf("RecipeStatus stdio-srv: ID=%q, want %q", rs.ID, "stdio-srv")
	}

	// Unknown server.
	if _, ok := d.RecipeStatus("unknown"); ok {
		t.Error("RecipeStatus unknown: want ok=false")
	}
}

// TestDispatch_RecipeStatusHTTP verifies that RecipeStatus synthesises a
// running status for http-owned servers.
func TestDispatch_RecipeStatusHTTP(t *testing.T) {
	t.Parallel()
	d, _, _, _ := newTestPool()

	_ = d.OpenOne(context.Background(), coremcp.ServerSpec{Name: "http-srv", Transport: "http", URL: "http://x"})

	rs, ok := d.RecipeStatus("http-srv")
	if !ok {
		t.Fatal("RecipeStatus http-srv: want ok=true")
	}
	if rs.ID != "http-srv" {
		t.Errorf("RecipeStatus http-srv: ID=%q, want %q", rs.ID, "http-srv")
	}
	if rs.State != "running" {
		t.Errorf("RecipeStatus http-srv: State=%q, want running", rs.State)
	}
	if !rs.Enabled {
		t.Error("RecipeStatus http-srv: want Enabled=true")
	}
	if rs.UpdatedAt.IsZero() {
		t.Error("RecipeStatus http-srv: UpdatedAt is zero")
	}
}

// TestDispatch_ToolsAggregatesAcrossPools verifies that Tools() fans out
// across all sub-pools and concatenates results.
func TestDispatch_ToolsAggregatesAcrossPools(t *testing.T) {
	t.Parallel()
	d, _, _, _ := newTestPool()

	_ = d.OpenOne(context.Background(), coremcp.ServerSpec{Name: "stdio-srv", Transport: ""})
	_ = d.OpenOne(context.Background(), coremcp.ServerSpec{Name: "http-srv", Transport: "http", URL: "http://x"})

	tools, err := d.Tools(context.Background())
	if err != nil {
		t.Fatalf("Tools: %v", err)
	}
	// Each fake returns one tool per opened server: stdio-srv + http-srv.
	if len(tools) != 2 {
		t.Errorf("Tools: got %d, want 2; tools=%v", len(tools), tools)
	}
}

// TestDispatch_StdioRegressionOpenAndCall verifies that stdio recipes
// still open and call correctly through the dispatch pool.
func TestDispatch_StdioRegressionOpenAndCall(t *testing.T) {
	t.Parallel()
	d, sf, _, _ := newTestPool()

	spec := coremcp.ServerSpec{Name: "filesystem", Transport: "stdio"}
	if err := d.OpenOne(context.Background(), spec); err != nil {
		t.Fatalf("OpenOne: %v", err)
	}
	if snap := sf.snapshot(); len(snap) != 1 || snap[0] != "filesystem" {
		t.Errorf("opened=%v, want [filesystem]", snap)
	}

	result, err := d.Call(context.Background(), "filesystem", "list_directory", json.RawMessage(`{"path":"/"}`))
	if err != nil {
		t.Fatalf("Call: %v", err)
	}
	if string(result) != `"ok"` {
		t.Errorf("Call result=%q, want %q", string(result), `"ok"`)
	}
}

// TestDispatch_AllRecipeStatuses verifies that AllRecipeStatuses includes
// both stdio and synthesised http entries.
func TestDispatch_AllRecipeStatuses(t *testing.T) {
	t.Parallel()
	d, _, _, _ := newTestPool()

	_ = d.OpenOne(context.Background(), coremcp.ServerSpec{Name: "stdio-srv", Transport: ""})
	_ = d.OpenOne(context.Background(), coremcp.ServerSpec{Name: "http-srv", Transport: "http", URL: "http://x"})

	statuses := d.AllRecipeStatuses()

	// Verify both servers appear.
	byID := make(map[string]struct{})
	for _, s := range statuses {
		byID[s.ID] = struct{}{}
	}
	for _, want := range []string{"stdio-srv", "http-srv"} {
		if _, ok := byID[want]; !ok {
			t.Errorf("AllRecipeStatuses: %q not found; got %v", want, statuses)
		}
	}
}

// TestDispatch_BulkOpen verifies that Open() partitions specs by transport
// and sends each bucket to the correct sub-pool.
func TestDispatch_BulkOpen(t *testing.T) {
	t.Parallel()
	d, sf, hf, _ := newTestPool()

	specs := []coremcp.ServerSpec{
		{Name: "s1", Transport: ""},
		{Name: "s2", Transport: "stdio"},
		{Name: "h1", Transport: "http", URL: "http://a"},
		{Name: "h2", Transport: "http", URL: "http://b"},
	}
	if err := d.Open(context.Background(), specs); err != nil {
		t.Fatalf("Open: %v", err)
	}

	sNames := sf.snapshot()
	hNames := hf.snapshot()
	if len(sNames) != 2 {
		t.Errorf("stdio pool opened %v; want 2 servers", sNames)
	}
	if len(hNames) != 2 {
		t.Errorf("http pool opened %v; want 2 servers", hNames)
	}
}

// TestDispatch_CloseStopsAll verifies that Close propagates to every
// active sub-pool.
func TestDispatch_CloseStopsAll(t *testing.T) {
	t.Parallel()
	d, sf, hf, _ := newTestPool()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := d.Close(ctx); err != nil {
		t.Fatalf("Close: %v", err)
	}
	if !sf.closed {
		t.Error("stdio sub-pool not closed after dispatch Close")
	}
	if !hf.closed {
		t.Error("http sub-pool not closed after dispatch Close")
	}
}

// TestDispatch_ServerToolsDelegatesToStdio verifies that ServerTools
// delegates to the stdio pool and returns nil for unknown/http servers.
func TestDispatch_ServerToolsDelegatesToStdio(t *testing.T) {
	t.Parallel()
	d, _, _, _ := newTestPool()

	_ = d.OpenOne(context.Background(), coremcp.ServerSpec{Name: "srv", Transport: ""})

	// ServerTools for a stdio server delegates to the stdio fake (which returns nil here).
	tools := d.ServerTools("srv")
	_ = tools // nil is acceptable from the fake

	// Unknown server returns nil.
	if tools := d.ServerTools("no-such"); tools != nil {
		t.Errorf("ServerTools unknown: got %v, want nil", tools)
	}

	// HTTP server returns nil (no per-server cache).
	_ = d.OpenOne(context.Background(), coremcp.ServerSpec{Name: "http-srv", Transport: "http", URL: "http://x"})
	if tools := d.ServerTools("http-srv"); tools != nil {
		t.Errorf("ServerTools http-srv: got %v, want nil", tools)
	}
}
