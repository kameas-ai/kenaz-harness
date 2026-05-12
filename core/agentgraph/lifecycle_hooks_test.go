package agentgraph

import (
	"context"
	"encoding/json"
	"testing"
)

// fakeLifecycleHookRunner is a test double for LifecycleHookRunner.
type fakeLifecycleHookRunner struct {
	preToolUseCalls  []fakePreToolUseCall
	postToolUseCalls []fakePostToolUseCall
	// preResult is returned from FirePreToolUse.
	preResult LifecycleMergedOutput
	// postResult is returned from FirePostToolUse.
	postResult LifecycleMergedOutput
}

type fakePreToolUseCall struct {
	SessionID string
	ToolName  string
	InputJSON json.RawMessage
}

type fakePostToolUseCall struct {
	SessionID string
	ToolName  string
	IsFailure bool
	ErrMsg    string
}

func (f *fakeLifecycleHookRunner) FirePreToolUse(
	_ context.Context, sessionID, toolName string, inputJSON json.RawMessage, _, _ string,
) (LifecycleMergedOutput, error) {
	f.preToolUseCalls = append(f.preToolUseCalls, fakePreToolUseCall{
		SessionID: sessionID, ToolName: toolName, InputJSON: inputJSON,
	})
	return f.preResult, nil
}

func (f *fakeLifecycleHookRunner) FirePostToolUse(
	_ context.Context, sessionID, toolName string, _, _ json.RawMessage, isFailure bool, errMsg, _, _ string,
) (LifecycleMergedOutput, error) {
	f.postToolUseCalls = append(f.postToolUseCalls, fakePostToolUseCall{
		SessionID: sessionID, ToolName: toolName, IsFailure: isFailure, ErrMsg: errMsg,
	})
	return f.postResult, nil
}

// pendingContextRecorder records AppendSystemContext calls.
type pendingContextRecorder struct {
	entries []string
}

func (p *pendingContextRecorder) AppendSystemContext(_ context.Context, _, text string) error {
	p.entries = append(p.entries, text)
	return nil
}

// fakeToolRegistry for agentgraph tests.
type fakeToolRegistryForLifecycle struct {
	result  ToolResult
	callErr error
}

func (f *fakeToolRegistryForLifecycle) Has(_ string) bool { return true }
func (f *fakeToolRegistryForLifecycle) Call(_ context.Context, _ ToolCall) (ToolResult, error) {
	return f.result, f.callErr
}

func TestLifecycleHookRunner_PreToolUse_Fires(t *testing.T) {
	t.Parallel()
	fake := &fakeLifecycleHookRunner{
		preResult: LifecycleMergedOutput{Blocked: false},
	}
	ctx := context.Background()
	got, err := fake.FirePreToolUse(ctx, "s1", "bash", json.RawMessage(`{"cmd":"ls"}`), "", "")
	if err != nil {
		t.Fatalf("FirePreToolUse: %v", err)
	}
	if got.Blocked {
		t.Error("expected not blocked")
	}
	if len(fake.preToolUseCalls) != 1 {
		t.Fatalf("preToolUseCalls len=%d, want 1", len(fake.preToolUseCalls))
	}
	if fake.preToolUseCalls[0].ToolName != "bash" {
		t.Errorf("ToolName=%q, want %q", fake.preToolUseCalls[0].ToolName, "bash")
	}
}

func TestLifecycleMergedOutput_BlockFields(t *testing.T) {
	t.Parallel()
	m := LifecycleMergedOutput{
		Blocked:     true,
		BlockReason: "denied by policy",
	}
	if !m.Blocked {
		t.Error("expected Blocked=true")
	}
	if m.BlockReason != "denied by policy" {
		t.Errorf("BlockReason=%q", m.BlockReason)
	}
}

func TestLifecycleMergedOutput_UpdatedInputField(t *testing.T) {
	t.Parallel()
	input := json.RawMessage(`{"cmd":"ls -la"}`)
	m := LifecycleMergedOutput{UpdatedInput: input}
	if string(m.UpdatedInput) != string(input) {
		t.Errorf("UpdatedInput=%s", m.UpdatedInput)
	}
}

func TestEventConstants_HookInputRewrite(t *testing.T) {
	t.Parallel()
	if EventHookInputRewrite == "" {
		t.Error("EventHookInputRewrite must be non-empty")
	}
	if EventHookDenied == "" {
		t.Error("EventHookDenied must be non-empty")
	}
}
