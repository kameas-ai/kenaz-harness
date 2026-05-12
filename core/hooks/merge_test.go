package hooks

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestMergeOutputs_Empty(t *testing.T) {
	t.Parallel()
	m := MergeOutputs(nil)
	if m.Blocked {
		t.Error("empty outputs: Blocked should be false")
	}
	if m.AdditionalContext != "" {
		t.Errorf("empty outputs: AdditionalContext = %q, want empty", m.AdditionalContext)
	}
	if m.UpdatedInput != nil {
		t.Errorf("empty outputs: UpdatedInput should be nil, got %s", m.UpdatedInput)
	}
}

func TestMergeOutputs_BlockDominatesApprove(t *testing.T) {
	t.Parallel()
	outputs := []HookOutput{
		{Decision: "approve", Reason: "ok"},
		{Decision: "block", Reason: "bad tool"},
		{Decision: "approve", Reason: "also ok"},
	}
	m := MergeOutputs(outputs)
	if !m.Blocked {
		t.Fatal("expected Blocked=true")
	}
	if m.BlockReason != "bad tool" {
		t.Errorf("BlockReason = %q, want %q", m.BlockReason, "bad tool")
	}
}

func TestMergeOutputs_MultipleBlocksFirstReasonWins(t *testing.T) {
	t.Parallel()
	outputs := []HookOutput{
		{Decision: "block", Reason: "first"},
		{Decision: "block", Reason: "second"},
	}
	m := MergeOutputs(outputs)
	if m.BlockReason != "first" {
		t.Errorf("BlockReason = %q, want %q", m.BlockReason, "first")
	}
}

func TestMergeOutputs_AdditionalContextJoined(t *testing.T) {
	t.Parallel()
	outputs := []HookOutput{
		{AdditionalContext: "part1"},
		{AdditionalContext: "part2"},
		{AdditionalContext: ""},
		{AdditionalContext: "part3"},
	}
	m := MergeOutputs(outputs)
	want := "part1\n\npart2\n\npart3"
	if m.AdditionalContext != want {
		t.Errorf("AdditionalContext = %q, want %q", m.AdditionalContext, want)
	}
}

func TestMergeOutputs_UpdatedInputComposeLeftToRight(t *testing.T) {
	t.Parallel()
	first := json.RawMessage(`{"cmd":"ls"}`)
	second := json.RawMessage(`{"cmd":"ls -la"}`)
	outputs := []HookOutput{
		{UpdatedInput: first},
		{UpdatedInput: second},
	}
	m := MergeOutputs(outputs)
	if string(m.UpdatedInput) != string(second) {
		t.Errorf("UpdatedInput = %s, want %s (last wins)", m.UpdatedInput, second)
	}
}

func TestMergeOutputs_UpdatedInputOnlyFirstHook(t *testing.T) {
	t.Parallel()
	first := json.RawMessage(`{"cmd":"ls"}`)
	outputs := []HookOutput{
		{UpdatedInput: first},
		{Decision: "approve"},
	}
	m := MergeOutputs(outputs)
	if string(m.UpdatedInput) != string(first) {
		t.Errorf("UpdatedInput = %s, want %s", m.UpdatedInput, first)
	}
}

func TestMergeOutputs_PermissionDenyDominatesAllow(t *testing.T) {
	t.Parallel()
	outputs := []HookOutput{
		{PermissionDecision: "allow", PermissionDecisionReason: "trusted"},
		{PermissionDecision: "deny", PermissionDecisionReason: "policy"},
		{PermissionDecision: "allow", PermissionDecisionReason: "also trusted"},
	}
	m := MergeOutputs(outputs)
	if !m.PermissionDenied {
		t.Fatal("expected PermissionDenied=true")
	}
	if m.PermissionAllowed {
		t.Error("PermissionAllowed should be false when deny is present")
	}
	if m.PermissionReason != "policy" {
		t.Errorf("PermissionReason = %q, want %q", m.PermissionReason, "policy")
	}
}

func TestMergeOutputs_PermissionAllowOnly(t *testing.T) {
	t.Parallel()
	outputs := []HookOutput{
		{PermissionDecision: "allow", PermissionDecisionReason: "trusted"},
	}
	m := MergeOutputs(outputs)
	if !m.PermissionAllowed {
		t.Fatal("expected PermissionAllowed=true")
	}
	if m.PermissionDenied {
		t.Error("PermissionDenied should be false")
	}
	if m.PermissionReason != "trusted" {
		t.Errorf("PermissionReason = %q, want %q", m.PermissionReason, "trusted")
	}
}

func TestMergeOutputs_WatchPathsDeduped(t *testing.T) {
	t.Parallel()
	outputs := []HookOutput{
		{WatchPaths: []string{"/a", "/b"}},
		{WatchPaths: []string{"/b", "/c"}},
		{WatchPaths: []string{"/a"}},
	}
	m := MergeOutputs(outputs)
	// Expect /a, /b, /c in order, no duplicates.
	if len(m.WatchPaths) != 3 {
		t.Fatalf("WatchPaths len=%d, want 3: %v", len(m.WatchPaths), m.WatchPaths)
	}
	for i, want := range []string{"/a", "/b", "/c"} {
		if m.WatchPaths[i] != want {
			t.Errorf("WatchPaths[%d] = %q, want %q", i, m.WatchPaths[i], want)
		}
	}
}

func TestMergeOutputs_AsyncOutputsExcluded(t *testing.T) {
	t.Parallel()
	outputs := []HookOutput{
		{Async: true, Decision: "block", AdditionalContext: "async-ctx"},
		{Decision: "approve"},
	}
	m := MergeOutputs(outputs)
	if m.Blocked {
		t.Error("async block should not block the sync pipeline")
	}
	if strings.Contains(m.AdditionalContext, "async-ctx") {
		t.Error("async additional_context should not appear in merged output")
	}
}

func TestMergeOutputs_UpdatedMCPOutputComposed(t *testing.T) {
	t.Parallel()
	first := json.RawMessage(`{"result":"v1"}`)
	second := json.RawMessage(`{"result":"v2"}`)
	outputs := []HookOutput{
		{UpdatedMCPOutput: first},
		{UpdatedMCPOutput: second},
	}
	m := MergeOutputs(outputs)
	if string(m.UpdatedMCPOutput) != string(second) {
		t.Errorf("UpdatedMCPOutput = %s, want %s", m.UpdatedMCPOutput, second)
	}
}

func TestMergeOutputs_MixedComposite(t *testing.T) {
	t.Parallel()
	// Realistic scenario: two hooks, first transforms input + injects context,
	// second approves + injects more context.
	input1 := json.RawMessage(`{"cmd":"ls"}`)
	outputs := []HookOutput{
		{
			Decision:          "approve",
			UpdatedInput:      input1,
			AdditionalContext: "project rules injected",
		},
		{
			Decision:          "approve",
			AdditionalContext: "memory context injected",
		},
	}
	m := MergeOutputs(outputs)
	if m.Blocked {
		t.Fatal("should not be blocked")
	}
	if string(m.UpdatedInput) != string(input1) {
		t.Errorf("UpdatedInput = %s, want %s", m.UpdatedInput, input1)
	}
	if !strings.Contains(m.AdditionalContext, "project rules injected") {
		t.Error("AdditionalContext missing 'project rules injected'")
	}
	if !strings.Contains(m.AdditionalContext, "memory context injected") {
		t.Error("AdditionalContext missing 'memory context injected'")
	}
}

func TestHookOutput_JSONRoundTrip(t *testing.T) {
	t.Parallel()
	original := HookOutput{
		Decision:                 "block",
		Reason:                   "test reason",
		AdditionalContext:        "ctx",
		UpdatedInput:             json.RawMessage(`{"a":1}`),
		UpdatedMCPOutput:         json.RawMessage(`{"b":2}`),
		PermissionDecision:       "deny",
		PermissionDecisionReason: "denied by hook",
		WatchPaths:               []string{"/foo", "/bar"},
		Async:                    true,
		AsyncTimeoutMs:           30000,
	}
	data, err := json.Marshal(original)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var decoded HookOutput
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if decoded.Decision != original.Decision {
		t.Errorf("Decision = %q, want %q", decoded.Decision, original.Decision)
	}
	if decoded.Reason != original.Reason {
		t.Errorf("Reason = %q, want %q", decoded.Reason, original.Reason)
	}
	if decoded.PermissionDecision != original.PermissionDecision {
		t.Errorf("PermissionDecision = %q, want %q", decoded.PermissionDecision, original.PermissionDecision)
	}
	if len(decoded.WatchPaths) != 2 {
		t.Errorf("WatchPaths len=%d, want 2", len(decoded.WatchPaths))
	}
	if !decoded.Async {
		t.Error("Async should be true")
	}
}

func TestAllEventsContainsV1AndV2(t *testing.T) {
	t.Parallel()
	required := []string{
		EventPreSend, EventPostSend, EventPreSaveSession, EventPostAssistantTurnComplete,
		EventPreToolUse, EventPostToolUse, EventPostToolUseFailure,
		EventUserPromptSubmit, EventSessionStart, EventSetup,
		EventSubagentStart, EventCwdChanged, EventFileChanged,
		EventPermissionRequest, EventPermissionDenied,
		EventNotification, EventBackgroundTaskComplete, EventWorktreeCreate,
	}
	for _, e := range required {
		if !isKnownEvent(e) {
			t.Errorf("event %q not in AllEvents", e)
		}
	}
}

func TestHook_EffectiveTimeoutMs(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name      string
		hook      Hook
		wantMs    int
	}{
		{"default", Hook{}, DefaultSyncTimeoutMs},
		{"explicit", Hook{TimeoutMs: 2000}, 2000},
		{"zero uses default", Hook{TimeoutMs: 0}, DefaultSyncTimeoutMs},
	}
	for _, c := range cases {
		if got := c.hook.EffectiveTimeoutMs(); got != c.wantMs {
			t.Errorf("%s: EffectiveTimeoutMs()=%d, want %d", c.name, got, c.wantMs)
		}
	}
}
