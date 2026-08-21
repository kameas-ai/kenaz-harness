package dispatch_test

import (
	"context"
	"encoding/json"
	"errors"
	"sync"
	"testing"

	coremcp "github.com/kameas-ai/kenaz-harness/core/mcp"
	"github.com/kameas-ai/kenaz-harness/core/mcp/dispatch"
	"github.com/kameas-ai/kenaz-harness/core/mcp/transport"
)

// ─── fake in-process server connection ────────────────────────────────────

// fakeInProcessServer is a minimal InProcessConnection double standing in
// for *harness.Transport without pulling in the harness package (keeps
// the routing arm's tests independent of any harness-self involvement,
// per tasks.md UNIT-5: "a routing bug never looks like a seam bug").
// It answers tools/list with one canned tool and echoes tools/call
// arguments back as the result, so a test can distinguish "reached this
// fake" from "reached some other pool" by content, not just by absence
// of error.
type fakeInProcessServer struct {
	mu       sync.Mutex
	closed   bool
	closeErr error
	toolName string
	lastEnv  transport.RequestEnvelope
}

func newFakeInProcessServer(toolName string) *fakeInProcessServer {
	return &fakeInProcessServer{toolName: toolName}
}

func (f *fakeInProcessServer) Send(_ context.Context, env transport.RequestEnvelope) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.closed {
		return errors.New("fakeInProcessServer: closed")
	}
	f.lastEnv = env
	return nil
}

func (f *fakeInProcessServer) Recv(_ context.Context) (transport.ResponseEnvelope, error) {
	f.mu.Lock()
	env := f.lastEnv
	closed := f.closed
	f.mu.Unlock()
	if closed {
		return transport.ResponseEnvelope{}, errors.New("fakeInProcessServer: closed")
	}
	switch env.Method {
	case transport.MethodToolsList:
		return transport.ResponseEnvelope{
			JSONRPC: transport.JSONRPCVersion,
			Result: transport.ToolsListResult{
				Tools: []transport.ToolDefinition{
					{Name: f.toolName, Description: "fake in-process tool"},
				},
			},
		}, nil
	case transport.MethodToolsCall:
		return transport.ResponseEnvelope{
			JSONRPC: transport.JSONRPCVersion,
			Result: transport.ToolsCallResult{
				Content: []json.RawMessage{json.RawMessage(`{"type":"text","text":"fake-called"}`)},
			},
		}, nil
	default:
		return transport.ResponseEnvelope{}, errors.New("fakeInProcessServer: unhandled method")
	}
}

func (f *fakeInProcessServer) Close() error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.closed = true
	return f.closeErr
}

// ─── InProcessSubPool unit tests ──────────────────────────────────────────

func TestInProcessSubPool_RegisterListCall(t *testing.T) {
	t.Parallel()
	p := dispatch.NewInProcessSubPool()
	fake := newFakeInProcessServer("harness_read_get_status")

	if err := p.RegisterServer(context.Background(), "harness-self", fake); err != nil {
		t.Fatalf("RegisterServer: %v", err)
	}

	tools, err := p.Tools(context.Background())
	if err != nil {
		t.Fatalf("Tools: %v", err)
	}
	if len(tools) != 1 || tools[0].Name != "harness_read_get_status" || tools[0].Server != "harness-self" {
		t.Fatalf("Tools = %+v, want one harness_read_get_status tool owned by harness-self", tools)
	}

	result, err := p.Call(context.Background(), "harness-self", "harness_read_get_status", json.RawMessage(`{}`))
	if err != nil {
		t.Fatalf("Call: %v", err)
	}
	if !contains(result, "fake-called") {
		t.Errorf("Call result = %s, want it to contain the fake's canned marker", result)
	}
}

func TestInProcessSubPool_RegisterDuplicateRejected(t *testing.T) {
	t.Parallel()
	p := dispatch.NewInProcessSubPool()
	fake1 := newFakeInProcessServer("t1")
	fake2 := newFakeInProcessServer("t2")

	if err := p.RegisterServer(context.Background(), "dup", fake1); err != nil {
		t.Fatalf("first RegisterServer: %v", err)
	}
	err := p.RegisterServer(context.Background(), "dup", fake2)
	if !errors.Is(err, dispatch.ErrInProcessServerExists) {
		t.Fatalf("second RegisterServer err = %v, want ErrInProcessServerExists", err)
	}
}

func TestInProcessSubPool_OpenRejectsSpecDrivenCalls(t *testing.T) {
	t.Parallel()
	p := dispatch.NewInProcessSubPool()
	err := p.Open(context.Background(), []coremcp.ServerSpec{{Name: "x", Transport: "inprocess"}})
	if err == nil {
		t.Fatal("Open with a spec: want an explicit error, got nil (spec-driven Open must not silently accept)")
	}
}

func TestInProcessSubPool_CloseOneRemovesServer(t *testing.T) {
	t.Parallel()
	p := dispatch.NewInProcessSubPool()
	fake := newFakeInProcessServer("t1")
	if err := p.RegisterServer(context.Background(), "s1", fake); err != nil {
		t.Fatalf("RegisterServer: %v", err)
	}

	if err := p.CloseOne(context.Background(), "s1"); err != nil {
		t.Fatalf("CloseOne: %v", err)
	}
	fake.mu.Lock()
	closed := fake.closed
	fake.mu.Unlock()
	if !closed {
		t.Error("CloseOne did not close the underlying connection")
	}

	if _, err := p.Call(context.Background(), "s1", "t1", nil); !errors.Is(err, dispatch.ErrInProcessServerNotFound) {
		t.Errorf("Call after CloseOne err = %v, want ErrInProcessServerNotFound", err)
	}
}

// ─── dispatch.Pool routing tests: the fourth arm end-to-end ──────────────

func TestPool_InProcessArm_RegisterToolsCallClose(t *testing.T) {
	t.Parallel()
	inproc := dispatch.NewInProcessSubPool()
	pool := dispatch.New(dispatch.Options{InProcess: inproc})

	fake := newFakeInProcessServer("harness_write_set_setting")
	if err := pool.RegisterInProcess(context.Background(), "harness-self", fake); err != nil {
		t.Fatalf("RegisterInProcess: %v", err)
	}

	// Appears in dispatch.Pool.Tools(ctx) — proves routing did not
	// silently drop into the stdio arm (which has no servers here and
	// would report zero tools rather than the fake's one).
	tools, err := pool.Tools(context.Background())
	if err != nil {
		t.Fatalf("Tools: %v", err)
	}
	if len(tools) != 1 || tools[0].Name != "harness_write_set_setting" {
		t.Fatalf("Tools = %+v, want exactly the fake's one tool — a stdio-arm misroute would report zero", tools)
	}

	// Callable via dispatch.Pool.Call.
	result, err := pool.Call(context.Background(), "harness-self", "harness_write_set_setting", json.RawMessage(`{}`))
	if err != nil {
		t.Fatalf("Call: %v", err)
	}
	if !contains(result, "fake-called") {
		t.Errorf("Call result = %s, want the fake's canned marker — a stdio-arm misroute would return ErrServerNotFound instead", result)
	}

	// Closing it removes it.
	if err := pool.CloseOne(context.Background(), "harness-self"); err != nil {
		t.Fatalf("CloseOne: %v", err)
	}
	toolsAfter, err := pool.Tools(context.Background())
	if err != nil {
		t.Fatalf("Tools after CloseOne: %v", err)
	}
	if len(toolsAfter) != 0 {
		t.Errorf("Tools after CloseOne = %+v, want empty", toolsAfter)
	}
	if _, err := pool.Call(context.Background(), "harness-self", "harness_write_set_setting", nil); err == nil {
		t.Error("Call after CloseOne: want an error, got nil")
	}
}

// TestPool_InProcessArm_NotWiredFailsLoudly pins the "no InProcess
// option supplied" case: RegisterInProcess must return an explicit
// error, not silently do nothing and not route to another arm.
func TestPool_InProcessArm_NotWiredFailsLoudly(t *testing.T) {
	t.Parallel()
	pool := dispatch.New(dispatch.Options{}) // no InProcess sub-pool
	fake := newFakeInProcessServer("t1")
	err := pool.RegisterInProcess(context.Background(), "s1", fake)
	if err == nil {
		t.Fatal("RegisterInProcess with no InProcess sub-pool wired: want an error, got nil")
	}
}

func contains(b json.RawMessage, substr string) bool {
	return len(b) > 0 && jsonContains(string(b), substr)
}

func jsonContains(haystack, needle string) bool {
	for i := 0; i+len(needle) <= len(haystack); i++ {
		if haystack[i:i+len(needle)] == needle {
			return true
		}
	}
	return false
}
