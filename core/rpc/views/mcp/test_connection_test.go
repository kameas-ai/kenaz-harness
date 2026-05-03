package mcp

import (
	"context"
	"encoding/json"
	"fmt"
	"testing"
	"time"

	coremcp "github.com/sigil-tech/kaneaz-harness/core/mcp"
	mcptransport "github.com/sigil-tech/kaneaz-harness/core/mcp/transport"
	"github.com/sigil-tech/kaneaz-harness/core/mcp/recipes"
)

// ── fakeConn ──────────────────────────────────────────────────────────────
//
// fakeConn is a synchronous in-process Connection used to test
// driveHandshake without spawning a real subprocess or HTTP server.
// It implements mcptransport.Connection over a pair of channels.

type fakeConn struct {
	recv      chan mcptransport.RawMessage
	done      chan struct{}
	stderrMsg string
}

func newFakeConn() *fakeConn {
	return &fakeConn{
		recv: make(chan mcptransport.RawMessage, 32),
		done: make(chan struct{}),
	}
}

func (f *fakeConn) Open(_ context.Context) error  { return nil }
func (f *fakeConn) Send(_ any) error               { return nil }
func (f *fakeConn) PID() int                       { return 0 }
func (f *fakeConn) StderrTail(_ int) string        { return f.stderrMsg }

func (f *fakeConn) Recv() (mcptransport.RawMessage, error) {
	select {
	case msg := <-f.recv:
		return msg, nil
	case <-f.done:
		return mcptransport.RawMessage{}, fmt.Errorf("fakeConn: closed")
	}
}

func (f *fakeConn) Close() error {
	select {
	case <-f.done:
	default:
		close(f.done)
	}
	return nil
}

// injectResponse queues a fake response for the given int64 request id.
func (f *fakeConn) injectResponse(id int64, result any) {
	idJSON, _ := json.Marshal(id)
	resultJSON, _ := json.Marshal(result)
	raw := json.RawMessage(idJSON)
	f.recv <- mcptransport.RawMessage{
		JSONRPC: "2.0",
		ID:      &raw,
		Result:  resultJSON,
	}
}

// compile-time check
var _ mcptransport.Connection = (*fakeConn)(nil)

// ── driveHandshake tests ──────────────────────────────────────────────────

func TestDriveHandshake_ToolsOnly(t *testing.T) {
	conn := newFakeConn()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	go func() {
		// id=1 → initialize response
		conn.injectResponse(1, map[string]any{
			"protocolVersion": "2024-11-05",
			"capabilities":    map[string]any{"tools": map[string]any{}},
			"serverInfo":      map[string]any{"name": "test-srv", "version": "0.0.1"},
		})
		// id=2 → tools/list response (initialize sends notifications/initialized
		// which gets no response; driveHandshake skips it in the send path)
		conn.injectResponse(2, map[string]any{
			"tools": []map[string]any{
				{"name": "tool_a", "description": "desc"},
				{"name": "tool_b", "description": "desc"},
			},
		})
	}()

	result, err := driveHandshake(ctx, conn)
	if err != nil {
		t.Fatalf("driveHandshake: %v", err)
	}
	if !result.OK {
		t.Errorf("expected OK=true, got false")
	}
	if result.ToolCount != 2 {
		t.Errorf("expected 2 tools, got %d", result.ToolCount)
	}
	if result.ResourceCount != -1 {
		t.Errorf("expected ResourceCount=-1 (not advertised), got %d", result.ResourceCount)
	}
	if result.PromptCount != -1 {
		t.Errorf("expected PromptCount=-1 (not advertised), got %d", result.PromptCount)
	}
	if result.ServerInfo.Name != "test-srv" {
		t.Errorf("expected ServerInfo.Name=%q, got %q", "test-srv", result.ServerInfo.Name)
	}
	if result.ProtocolVersion != "2024-11-05" {
		t.Errorf("expected ProtocolVersion=%q, got %q", "2024-11-05", result.ProtocolVersion)
	}
}

func TestDriveHandshake_AllCapabilities(t *testing.T) {
	conn := newFakeConn()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	go func() {
		// id=1 → initialize
		conn.injectResponse(1, map[string]any{
			"protocolVersion": "2024-11-05",
			"capabilities": map[string]any{
				"tools":     map[string]any{},
				"resources": map[string]any{},
				"prompts":   map[string]any{},
			},
			"serverInfo": map[string]any{"name": "full-srv", "version": "1.0.0"},
		})
		// id=2 → tools/list (3 tools)
		conn.injectResponse(2, map[string]any{
			"tools": []map[string]any{
				{"name": "t1"}, {"name": "t2"}, {"name": "t3"},
			},
		})
		// id=3 → resources/list (2 resources)
		conn.injectResponse(3, map[string]any{
			"resources": []map[string]any{
				{"uri": "file:///r1", "name": "r1"},
				{"uri": "file:///r2", "name": "r2"},
			},
		})
		// id=4 → prompts/list (1 prompt)
		conn.injectResponse(4, map[string]any{
			"prompts": []map[string]any{
				{"name": "p1"},
			},
		})
	}()

	result, err := driveHandshake(ctx, conn)
	if err != nil {
		t.Fatalf("driveHandshake: %v", err)
	}
	if result.ToolCount != 3 {
		t.Errorf("expected 3 tools, got %d", result.ToolCount)
	}
	if result.ResourceCount != 2 {
		t.Errorf("expected 2 resources, got %d", result.ResourceCount)
	}
	if result.PromptCount != 1 {
		t.Errorf("expected 1 prompt, got %d", result.PromptCount)
	}
}

func TestDriveHandshake_NoCapabilities(t *testing.T) {
	conn := newFakeConn()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	go func() {
		conn.injectResponse(1, map[string]any{
			"protocolVersion": "2024-11-05",
			"capabilities":    map[string]any{},
			"serverInfo":      map[string]any{"name": "no-cap-srv", "version": "0.0.0"},
		})
	}()

	result, err := driveHandshake(ctx, conn)
	if err != nil {
		t.Fatalf("driveHandshake: %v", err)
	}
	if !result.OK {
		t.Errorf("expected OK=true")
	}
	if result.ToolCount != -1 || result.ResourceCount != -1 || result.PromptCount != -1 {
		t.Errorf("expected all counts -1, got tools=%d resources=%d prompts=%d",
			result.ToolCount, result.ResourceCount, result.PromptCount)
	}
}

func TestDriveHandshake_InitializeRPCError(t *testing.T) {
	conn := newFakeConn()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	go func() {
		idJSON, _ := json.Marshal(int64(1))
		raw := json.RawMessage(idJSON)
		conn.recv <- mcptransport.RawMessage{
			JSONRPC: "2.0",
			ID:      &raw,
			Error:   &mcptransport.RPCError{Code: -32600, Message: "invalid request"},
		}
	}()

	_, err := driveHandshake(ctx, conn)
	if err == nil {
		t.Fatal("expected error from initialize RPC error, got nil")
	}
}

func TestDriveHandshake_ContextTimeout(t *testing.T) {
	conn := newFakeConn() // never injects a response
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()

	_, err := driveHandshake(ctx, conn)
	if err == nil {
		t.Fatal("expected context timeout error, got nil")
	}
}

// ── TestConnection dispatch tests ─────────────────────────────────────────

func TestTestConnection_UnknownTransport(t *testing.T) {
	spec := coremcp.ServerSpec{Transport: "grpc", Name: "test", Command: []string{"echo"}}
	result := TestConnection(context.Background(), spec)
	if result.OK {
		t.Errorf("expected OK=false for unknown transport")
	}
	if result.Error == "" {
		t.Errorf("expected non-empty error for unknown transport")
	}
}

func TestTestConnection_HTTPNotYetImplemented(t *testing.T) {
	spec := coremcp.ServerSpec{Transport: "http", Name: "test", URL: "http://localhost:9999"}
	result := TestConnection(context.Background(), spec)
	if result.OK {
		t.Errorf("expected OK=false for unimplemented HTTP transport")
	}
	if result.Error == "" {
		t.Errorf("expected non-empty error for HTTP not-yet-implemented")
	}
}

func TestTestConnection_SSENotYetImplemented(t *testing.T) {
	spec := coremcp.ServerSpec{Transport: "sse", Name: "test", URL: "http://localhost:9999"}
	result := TestConnection(context.Background(), spec)
	if result.OK {
		t.Errorf("expected OK=false for unimplemented SSE transport")
	}
}

func TestTestConnection_StdioEmptyCommand(t *testing.T) {
	spec := coremcp.ServerSpec{Transport: "stdio", Name: "test", Command: nil}
	result := TestConnection(context.Background(), spec)
	if result.OK {
		t.Errorf("expected OK=false for empty stdio command")
	}
}

func TestTestConnection_StdioNonExistentBinary(t *testing.T) {
	spec := coremcp.ServerSpec{
		Transport: "stdio",
		Name:      "no-such-bin",
		Command:   []string{"/no/such/binary/mcp-server"},
	}
	start := time.Now()
	result := TestConnection(context.Background(), spec)
	elapsed := time.Since(start)

	if result.OK {
		t.Errorf("expected OK=false for non-existent binary")
	}
	// Should fail fast (exec error), not wait for DefaultTestTimeout.
	if elapsed >= DefaultTestTimeout {
		t.Errorf("test connection took %v, expected fast exec-fail", elapsed)
	}
	if result.DurationMs < 0 {
		t.Errorf("DurationMs should be >= 0, got %d", result.DurationMs)
	}
}

func TestTestConnection_DefaultsToStdio(t *testing.T) {
	// Transport="" should fall through to stdio path.
	spec := coremcp.ServerSpec{
		Transport: "",
		Name:      "no-such-bin",
		Command:   []string{"/no/such/binary/mcp-server"},
	}
	result := TestConnection(context.Background(), spec)
	// Should fail with a stdio exec error, not "unknown transport".
	if result.OK {
		t.Errorf("expected OK=false")
	}
	// Error should NOT mention "unknown transport".
	if result.Error == "test connection: unknown transport \"\"" {
		t.Errorf("empty transport should default to stdio, got: %s", result.Error)
	}
}

// ── TestRecipe view-layer tests ───────────────────────────────────────────

func TestTestRecipe_CatalogNotConfigured(t *testing.T) {
	api := NewAPI() // no WithCatalog
	_, err := api.TestRecipe(context.Background(), "any", nil, nil)
	if err != ErrCatalogNotConfigured {
		t.Errorf("expected ErrCatalogNotConfigured, got %v", err)
	}
}

func TestTestRecipe_RecipeNotFound(t *testing.T) {
	api := NewAPI(WithCatalog(emptyCatalog{}))
	_, err := api.TestRecipe(context.Background(), "no-such-recipe", nil, nil)
	if err == nil {
		t.Fatal("expected error for recipe not found")
	}
}

func TestTestRecipe_BadCommandFails(t *testing.T) {
	// Wire a catalog that returns a recipe with a non-existent binary.
	api := NewAPI(WithCatalog(singleRecipeCatalog{
		recipe: recipes.Recipe{
			ID:      "test-recipe",
			Command: []string{"/no/such/binary/mcp-server"},
		},
	}))
	result, err := api.TestRecipe(context.Background(), "test-recipe", nil, nil)
	if err != nil {
		t.Fatalf("expected nil Go error, pre-flight failures go into result.Error; got %v", err)
	}
	if result.OK {
		t.Errorf("expected OK=false for non-existent binary")
	}
	if result.Error == "" {
		t.Errorf("expected non-empty Error in result")
	}
}

// emptyCatalog satisfies RecipeCatalog with zero entries.
type emptyCatalog struct{}

func (emptyCatalog) Get(_ string) (recipes.Recipe, bool) { return recipes.Recipe{}, false }

// singleRecipeCatalog returns a single recipe for matching id.
type singleRecipeCatalog struct {
	recipe recipes.Recipe
}

func (s singleRecipeCatalog) Get(id string) (recipes.Recipe, bool) {
	if id == s.recipe.ID {
		return s.recipe, true
	}
	return recipes.Recipe{}, false
}
