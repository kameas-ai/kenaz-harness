// mcp_dispatch_test.go — finding #71: kind=mcp hooks registered
// cleanly but had no production MCPInvoker wired at Config.MCP
// (core/rpc's newHooksStack never set it), so every kind=mcp dispatch
// failed with "mcp invoker not configured" no matter what the hook
// was actually configured to do. Before this file, core/hooks had
// zero test coverage of a kind=mcp dispatch actually reaching an
// MCPInvoker — TestRegistry_Validate_RejectsBadHooks only covers
// registration ("mcp missing tool"), never dispatch.
//
// fakeMCPInvoker is deliberately a mutex+snapshot() fake, not a bare
// struct the test reads directly: RunPostSend dispatches on
// hooks.Runner's async worker pool, a goroutine the test body does not
// control, so a plain field write there and a plain field read here
// would be exactly the race CLAUDE.md's "Race-safe test fakes" section
// warns about (`go test -race` catches it; a non-race run would not).
package hooks

import (
	"context"
	"encoding/json"
	"strings"
	"sync"
	"testing"
)

// mcpCall records one InvokeTool invocation.
type mcpCall struct {
	tool    string
	payload string // decoded to a string for cheap equality assertions
}

// fakeMCPInvoker implements MCPInvoker. calls is guarded by mu because
// InvokeTool is called from a hooks.Runner worker goroutine (RunPostSend's
// async pool) while the test body reads via snapshot() from the main
// goroutine.
type fakeMCPInvoker struct {
	mu     sync.Mutex
	calls  []mcpCall
	result []byte
	err    error
}

func (f *fakeMCPInvoker) InvokeTool(_ context.Context, tool string, payload []byte) ([]byte, error) {
	f.mu.Lock()
	f.calls = append(f.calls, mcpCall{tool: tool, payload: string(payload)})
	f.mu.Unlock()
	return f.result, f.err
}

func (f *fakeMCPInvoker) snapshot() []mcpCall {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make([]mcpCall, len(f.calls))
	copy(out, f.calls)
	return out
}

// TestRunner_KindMCP_PostSend_DispatchesToConfiguredInvoker is the
// dispatch proof for finding #71's async path: a kind=mcp post_send
// hook, once Config.MCP is set, actually reaches the configured
// invoker with the right tool identifier and a JSON-encoded event —
// not just "the field is non-nil" but "a real event flowed through
// the dispatch". Shutdown() (draining the async pool, mirroring
// post_send_async_test.go's pattern) is what makes reading the fake
// after RunPostSend race-free without a sleep.
func TestRunner_KindMCP_PostSend_DispatchesToConfiguredInvoker(t *testing.T) {
	t.Parallel()
	reg, _ := NewRegistry("")
	invoker := &fakeMCPInvoker{result: []byte(`{}`)}

	if err := reg.Add(Hook{
		ID: "h", Name: "n", Event: EventPostSend, Kind: KindMCP,
		Enabled: true, MCPTool: "notion__create_page",
	}); err != nil {
		t.Fatalf("Add: %v", err)
	}

	runner := NewRunner(Config{Registry: reg, Builtins: NewBuiltinRegistry(), MCP: invoker})
	runner.RunPostSend(context.Background(), PostSendEvent{
		SessionID: "sess-1", UserTurn: "hello", AssistantTurn: "hi there", FinishReason: "stop",
	})
	runner.Shutdown() // drains the async pool; safe to read invoker.snapshot() after this returns

	calls := invoker.snapshot()
	if len(calls) != 1 {
		t.Fatalf("InvokeTool call count = %d, want 1 (calls=%+v)", len(calls), calls)
	}
	if calls[0].tool != "notion__create_page" {
		t.Errorf("tool = %q, want %q", calls[0].tool, "notion__create_page")
	}
	var decoded PostSendEvent
	if err := json.Unmarshal([]byte(calls[0].payload), &decoded); err != nil {
		t.Fatalf("payload did not decode as PostSendEvent: %v (payload=%s)", err, calls[0].payload)
	}
	if decoded.SessionID != "sess-1" || decoded.UserTurn != "hello" || decoded.AssistantTurn != "hi there" {
		t.Errorf("decoded event = %+v, want session/user/assistant fields from the fired event", decoded)
	}
}

// TestRunner_KindMCP_Fire_DispatchesToConfiguredInvoker covers the
// v2 Fire path (pre_tool_use / post_tool_use / etc.) — a separate
// dispatch function (fireOne, fire.go) from RunPostSend's, so it needs
// its own proof that Config.MCP reaches it too.
func TestRunner_KindMCP_Fire_DispatchesToConfiguredInvoker(t *testing.T) {
	t.Parallel()
	reg, _ := NewRegistry("")
	invoker := &fakeMCPInvoker{result: []byte(`{"decision":"approve","additional_context":"from mcp"}`)}

	if err := reg.Add(Hook{
		ID: "h", Name: "n", Event: EventPreToolUse, Kind: KindMCP,
		Enabled: true, MCPTool: "kenaz-test__lookup",
	}); err != nil {
		t.Fatalf("Add: %v", err)
	}

	runner := NewRunner(Config{Registry: reg, Builtins: NewBuiltinRegistry(), MCP: invoker})
	outputs, err := runner.Fire(context.Background(), EventPreToolUse, PreToolUseEvent{
		SessionID: "sess-2", ToolName: "bash",
	})
	if err != nil {
		t.Fatalf("Fire: %v", err)
	}
	if len(outputs) != 1 {
		t.Fatalf("outputs = %+v, want exactly 1", outputs)
	}
	if outputs[0].Decision != "approve" || outputs[0].AdditionalContext != "from mcp" {
		t.Errorf("outputs[0] = %+v, want the fake invoker's result to flow back through fireOne", outputs[0])
	}

	calls := invoker.snapshot()
	if len(calls) != 1 || calls[0].tool != "kenaz-test__lookup" {
		t.Fatalf("InvokeTool calls = %+v, want exactly one call to %q", calls, "kenaz-test__lookup")
	}
}

// TestRunner_KindMCP_NoInvokerConfigured_FailsHonestly is the mutation
// counterpart, expressed as a direct assertion rather than an
// edit-and-rerun: a Runner built with Config.MCP left nil (the exact
// pre-fix production shape — newHooksStack used to never set it) must
// still fail the dispatch with a clear "not configured" error, never
// silently succeed. This is what proves
// TestRunner_KindMCP_PostSend_DispatchesToConfiguredInvoker and
// TestRunner_KindMCP_Fire_DispatchesToConfiguredInvoker are actually
// exercising the MCP.Config wiring and not some other pass-through:
// remove `MCP: invoker` from either test above and both would still
// need to pass unless dispatch genuinely depends on Config.MCP being
// set to a non-nil invoker — this test pins that the nil case is
// still the documented failure, not a crash or a fabricated success.
func TestRunner_KindMCP_NoInvokerConfigured_FailsHonestly(t *testing.T) {
	t.Parallel()
	h := Hook{
		ID: "h", Name: "n", Event: EventPreToolUse, Kind: KindMCP,
		Enabled: true, MCPTool: "notion__create_page",
	}
	if err := h.Validate(); err != nil {
		t.Fatalf("Validate: %v", err)
	}
	reg, _ := NewRegistry("")
	runner := NewRunner(Config{Registry: reg, Builtins: NewBuiltinRegistry()}) // MCP left nil, deliberately

	_, err := runner.fireOne(context.Background(), EventPreToolUse, h, PreToolUseEvent{SessionID: "s"})
	if err == nil {
		t.Fatal("fireOne with no MCP invoker configured: want an error, got nil")
	}
	if !strings.Contains(err.Error(), "not configured") {
		t.Fatalf("err = %q, want it to mention \"not configured\"", err.Error())
	}
}
