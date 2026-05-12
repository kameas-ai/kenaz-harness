package hooks

import (
	"context"
	"encoding/json"
	"sync/atomic"
	"testing"
	"time"
)

// TestFire_BuiltinV2_GenericFire verifies that a registered v2 builtin
// is dispatched through Fire with the correct event and payload.
func TestFire_BuiltinV2_GenericFire(t *testing.T) {
	t.Parallel()
	reg, _ := NewRegistry("")
	builtins := NewBuiltinRegistry()

	var captured struct {
		event   string
		payload any
	}
	builtins.RegisterGenericFire("test.generic", func(_ context.Context, event string, payload any, _ map[string]any) (HookOutput, error) {
		captured.event = event
		captured.payload = payload
		return HookOutput{AdditionalContext: "injected"}, nil
	}, BuiltinDescriptor{ID: "test.generic", Name: "test", Events: []string{EventPreToolUse}})

	if err := reg.Add(Hook{
		ID: "h", Name: "n", Event: EventPreToolUse, Kind: KindBuiltin,
		Enabled: true, Builtin: "test.generic",
	}); err != nil {
		t.Fatalf("Add: %v", err)
	}
	runner := NewRunner(Config{Registry: reg, Builtins: builtins})
	payload := PreToolUseEvent{SessionID: "s1", ToolName: "bash", Input: json.RawMessage(`{"cmd":"ls"}`)}
	outputs, err := runner.Fire(context.Background(), EventPreToolUse, payload)
	if err != nil {
		t.Fatalf("Fire: %v", err)
	}
	if len(outputs) != 1 {
		t.Fatalf("outputs len=%d, want 1", len(outputs))
	}
	if outputs[0].AdditionalContext != "injected" {
		t.Errorf("AdditionalContext = %q, want %q", outputs[0].AdditionalContext, "injected")
	}
	if captured.event != EventPreToolUse {
		t.Errorf("captured event = %q, want %q", captured.event, EventPreToolUse)
	}
}

// TestFire_ShellHook_V2Event verifies a shell hook receives the v2 fire
// envelope and returns a parsed HookOutput.
func TestFire_ShellHook_V2Event(t *testing.T) {
	t.Parallel()
	if !commandAvailable("python3") {
		t.Skip("python3 not available; skipping shell fire test")
	}
	reg, _ := NewRegistry("")
	// Simple script: reads the event name from stdin and emits a HookOutput.
	// Avoids f-strings with nested quotes to sidestep shell escaping issues.
	cmd := "python3 -c 'import json,sys; d=json.load(sys.stdin); e=d[\"event\"]; print(json.dumps({\"additional_context\":\"from-shell-hook:\"+e,\"decision\":\"approve\"}))'"
	if err := reg.Add(Hook{
		ID: "h", Name: "shell-v2", Event: EventPreToolUse, Kind: KindShell,
		Enabled: true, Command: cmd,
	}); err != nil {
		t.Fatalf("Add: %v", err)
	}
	runner := NewRunner(Config{Registry: reg})
	outputs, err := runner.Fire(context.Background(), EventPreToolUse, PreToolUseEvent{
		SessionID: "s", ToolName: "bash",
	})
	if err != nil {
		t.Fatalf("Fire: %v", err)
	}
	if len(outputs) != 1 {
		t.Fatalf("outputs len=%d, want 1", len(outputs))
	}
	wantCtx := "from-shell-hook:pre_tool_use"
	if outputs[0].AdditionalContext != wantCtx {
		t.Errorf("AdditionalContext = %q, want %q", outputs[0].AdditionalContext, wantCtx)
	}
	if outputs[0].Decision != "approve" {
		t.Errorf("Decision = %q", outputs[0].Decision)
	}
}

// TestFire_NoMatchingHooks returns nil without error.
func TestFire_NoMatchingHooks(t *testing.T) {
	t.Parallel()
	reg, _ := NewRegistry("")
	runner := NewRunner(Config{Registry: reg})
	outputs, err := runner.Fire(context.Background(), EventPreToolUse, nil)
	if err != nil {
		t.Fatalf("Fire: %v", err)
	}
	if outputs != nil {
		t.Fatalf("expected nil outputs, got %v", outputs)
	}
}

// TestFire_TimeoutReturnsNonBlockingError verifies a slow hook times out
// without blocking the pipeline.
func TestFire_TimeoutReturnsNonBlockingError(t *testing.T) {
	t.Parallel()
	reg, _ := NewRegistry("")
	if err := reg.Add(Hook{
		ID: "h", Name: "slow", Event: EventPreToolUse, Kind: KindShell,
		Enabled: true, Command: "sleep 10", TimeoutMs: 100,
	}); err != nil {
		t.Fatalf("Add: %v", err)
	}
	runner := NewRunner(Config{Registry: reg})
	start := time.Now()
	outputs, err := runner.Fire(context.Background(), EventPreToolUse, PreToolUseEvent{SessionID: "s"})
	dur := time.Since(start)
	if err != nil {
		t.Fatalf("Fire returned error: %v", err)
	}
	// Slow hook is skipped (non-blocking error); outputs should be empty.
	if len(outputs) != 0 {
		t.Fatalf("expected no outputs from timed-out hook, got %v", outputs)
	}
	if dur > 2*time.Second {
		t.Fatalf("Fire took %v; timeout did not kick in", dur)
	}
}

// TestFireAsync_ReturnsImmediately verifies FireAsync is non-blocking.
func TestFireAsync_ReturnsImmediately(t *testing.T) {
	t.Parallel()
	reg, _ := NewRegistry("")
	builtins := NewBuiltinRegistry()
	var calls atomic.Int32
	builtins.RegisterGenericFire("async.counter", func(_ context.Context, _ string, _ any, _ map[string]any) (HookOutput, error) {
		time.Sleep(50 * time.Millisecond) // simulate async work
		calls.Add(1)
		return HookOutput{}, nil
	}, BuiltinDescriptor{ID: "async.counter", Name: "counter", Events: []string{EventPostToolUse}})

	if err := reg.Add(Hook{
		ID: "h", Name: "n", Event: EventPostToolUse, Kind: KindBuiltin,
		Enabled: true, Builtin: "async.counter",
	}); err != nil {
		t.Fatalf("Add: %v", err)
	}
	runner := NewRunner(Config{Registry: reg, Builtins: builtins})
	start := time.Now()
	runner.FireAsync(context.Background(), EventPostToolUse, PostToolUseEvent{SessionID: "s"})
	dur := time.Since(start)
	if dur > 10*time.Millisecond {
		t.Fatalf("FireAsync took %v; should return immediately", dur)
	}
	// Wait for the async work to finish.
	runner.Shutdown()
	if calls.Load() != 1 {
		t.Fatalf("async hook fired %d times, want 1", calls.Load())
	}
}

// TestFireAsync_PoolSaturation verifies that when the pool is saturated
// work is dropped gracefully (no panic, no block).
func TestFireAsync_PoolSaturation(t *testing.T) {
	t.Parallel()
	reg, _ := NewRegistry("")
	builtins := NewBuiltinRegistry()
	// A hook that blocks long enough for the pool to saturate.
	builtins.RegisterGenericFire("blocker", func(ctx context.Context, _ string, _ any, _ map[string]any) (HookOutput, error) {
		select {
		case <-time.After(500 * time.Millisecond):
		case <-ctx.Done():
		}
		return HookOutput{}, nil
	}, BuiltinDescriptor{ID: "blocker", Name: "blocker", Events: []string{EventFileChanged}})

	// Add many hooks so the pool queue fills quickly.
	for i := 0; i < 50; i++ {
		id := "h" + string(rune('0'+i%10)) + string(rune('0'+i/10))
		_ = reg.Add(Hook{
			ID: id, Name: id, Event: EventFileChanged, Kind: KindBuiltin,
			Enabled: true, Builtin: "blocker",
		})
	}

	// Small pool of 1 worker so it saturates fast.
	runner := NewRunner(Config{Registry: reg, Builtins: builtins, AsyncPoolSize: 1})
	start := time.Now()
	runner.FireAsync(context.Background(), EventFileChanged, FileChangedEvent{SessionID: "s"})
	dur := time.Since(start)
	if dur > 100*time.Millisecond {
		t.Fatalf("FireAsync with saturated pool took %v; should not block", dur)
	}
	// Cleanup — shut down without waiting for all slow workers.
	go runner.Shutdown()
}

// TestExtractMatchKeys verifies the payload key extractor handles the
// common payloads correctly.
func TestExtractMatchKeys(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name      string
		payload   any
		wantSess  string
		wantKind  string
		wantModel string
	}{
		{
			name:      "PreToolUseEvent",
			payload:   PreToolUseEvent{SessionID: "s1", Kind: "mcp", Model: "claude-3"},
			wantSess:  "s1",
			wantKind:  "mcp",
			wantModel: "claude-3",
		},
		{
			name:     "SubagentStartEvent uses parent_session_id",
			payload:  SubagentStartEvent{ParentSessionID: "parent-1"},
			wantSess: "parent-1",
		},
		{
			name:    "nil payload",
			payload: nil,
		},
	}
	for _, c := range cases {
		sess, kind, model := extractMatchKeys(c.payload)
		if sess != c.wantSess {
			t.Errorf("%s: sessionID=%q, want %q", c.name, sess, c.wantSess)
		}
		if kind != c.wantKind {
			t.Errorf("%s: kind=%q, want %q", c.name, kind, c.wantKind)
		}
		if model != c.wantModel {
			t.Errorf("%s: model=%q, want %q", c.name, model, c.wantModel)
		}
	}
}

// TestFire_DisabledHookSkipped ensures Fire skips disabled hooks.
func TestFire_DisabledHookSkipped(t *testing.T) {
	t.Parallel()
	reg, _ := NewRegistry("")
	builtins := NewBuiltinRegistry()
	var calls atomic.Int32
	builtins.RegisterGenericFire("disabled.hook", func(_ context.Context, _ string, _ any, _ map[string]any) (HookOutput, error) {
		calls.Add(1)
		return HookOutput{}, nil
	}, BuiltinDescriptor{ID: "disabled.hook", Name: "x"})

	if err := reg.Add(Hook{
		ID: "h", Name: "n", Event: EventSessionStart, Kind: KindBuiltin,
		Enabled: false, Builtin: "disabled.hook",
	}); err != nil {
		t.Fatalf("Add: %v", err)
	}
	runner := NewRunner(Config{Registry: reg, Builtins: builtins})
	_, _ = runner.Fire(context.Background(), EventSessionStart, SessionStartEvent{SessionID: "s"})
	if calls.Load() != 0 {
		t.Fatalf("disabled hook fired %d times, want 0", calls.Load())
	}
}
