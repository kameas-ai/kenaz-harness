package toolloop

// dispatch_test.go — WP06 gate-hook tests.
//
// Coverage matrix:
//   • Nil engine → no-op (gate passes).
//   • Built-in tool (kaneaz server, engine says Allow) → passes without prompt.
//   • Untagged MCP tool (engine says NotApplicable, promptOnFirstUse=false)
//       → passes without prompt (backwards-compat default).
//   • Tagged tool first call (engine says NotApplicable, promptOnFirstUse=true)
//       → prompt fires, GrantAllowOnce → passes.
//   • Tagged tool second call after GrantAllowOnce (transient cache hit)
//       → gate bypasses engine entirely; no second prompt.
//   • Tagged tool with GrantAllowAlways → passes and cache is populated.
//   • Tagged tool with GrantDeny → blocked.
//   • Tagged tool with prompt error → blocked.
//   • Tagged tool with nil registry (engine NotApplicable) → safe-fail Deny.
//   • Cedar Deny outcome → blocked via ToolPermissionDeniedError wrapping
//       PolicyDeniedError.
//   • IsToolPermissionDenied helper.

import (
	"context"
	"errors"
	"testing"

	cedarraw "github.com/cedar-policy/cedar-go"
	"github.com/kameas-ai/kenaz-harness/core/policy/cedar"
)

// ────────────────────────────────────────────────────────────────────────────
// Fakes
// ────────────────────────────────────────────────────────────────────────────

// fakeGate is a Cedar Gate stub that returns a fixed Outcome.
type fakeGate struct {
	outcome cedar.Outcome
	reason  string
	// evaluations counts how many times Evaluate was called.
	evaluations int
}

func (g *fakeGate) Evaluate(
	_ context.Context,
	_ cedarraw.EntityUID,
	_ string,
	_ cedarraw.EntityUID,
	_ map[cedarraw.String]cedarraw.Value,
) cedar.Decision {
	g.evaluations++
	return cedar.Decision{
		Outcome: g.outcome,
		Reason:  g.reason,
	}
}

// stubPromptRegistry is a PromptRegistry stub that returns a fixed GrantKind
// and optionally a fixed error.
type stubPromptRegistry struct {
	grant  GrantKind
	err    error
	calls  []PromptSurface
}

func (r *stubPromptRegistry) RequestInteractive(_ context.Context, surface PromptSurface) (GrantKind, error) {
	r.calls = append(r.calls, surface)
	return r.grant, r.err
}

// ────────────────────────────────────────────────────────────────────────────
// Tests
// ────────────────────────────────────────────────────────────────────────────

func TestEvaluateToolGate_NilEngine_IsNoop(t *testing.T) {
	t.Parallel()
	err := EvaluateToolGate(context.Background(), GateInput{
		Engine:           nil,
		ServerName:       "filesystem",
		ToolName:         "write_file",
		PromptOnFirstUse: true, // even tagged tools are no-ops when engine is nil
	})
	if err != nil {
		t.Fatalf("nil engine should be a no-op, got: %v", err)
	}
}

func TestEvaluateToolGate_BuiltinAllowed_NoPrompt(t *testing.T) {
	t.Parallel()
	// The default_tool_policy permits kaneaz tools (server_name=="kaneaz").
	// Simulate with a gate that returns Allow.
	gate := &fakeGate{outcome: cedar.Allow}
	registry := &stubPromptRegistry{grant: GrantDeny} // would fail if called

	err := EvaluateToolGate(context.Background(), GateInput{
		Engine:           gate,
		Registry:         registry,
		ServerName:       "kaneaz",
		ToolName:         "bash",
		PromptOnFirstUse: false,
	})
	if err != nil {
		t.Fatalf("Allow outcome should pass, got: %v", err)
	}
	if len(registry.calls) != 0 {
		t.Errorf("prompt should not fire for Allow outcome; registry called %d times", len(registry.calls))
	}
	if gate.evaluations != 1 {
		t.Errorf("engine should be called exactly once; got %d", gate.evaluations)
	}
}

func TestEvaluateToolGate_UntaggedTool_NotApplicable_Passes(t *testing.T) {
	t.Parallel()
	// An MCP tool that did NOT opt into the gate (PromptOnFirstUse=false).
	// When the engine says NotApplicable, the gate must pass without prompting
	// (backwards-compat default: unknown tools are treated as allowed).
	gate := &fakeGate{outcome: cedar.NotApplicable}
	registry := &stubPromptRegistry{grant: GrantDeny}

	err := EvaluateToolGate(context.Background(), GateInput{
		Engine:           gate,
		Registry:         registry,
		ServerName:       "github",
		ToolName:         "get_issue",
		PromptOnFirstUse: false,
	})
	if err != nil {
		t.Fatalf("NotApplicable + untagged should pass, got: %v", err)
	}
	if len(registry.calls) != 0 {
		t.Errorf("prompt should not fire for untagged tool; called %d times", len(registry.calls))
	}
}

func TestEvaluateToolGate_TaggedTool_FirstCall_PromptFires_AllowOnce(t *testing.T) {
	t.Parallel()
	gate := &fakeGate{outcome: cedar.NotApplicable}
	registry := &stubPromptRegistry{grant: GrantAllowOnce}
	cache := NewMemTransientGrantCache()

	in := GateInput{
		Engine:           gate,
		Registry:         registry,
		Cache:            cache,
		SessionID:        "sess-abc",
		ServerName:       "filesystem",
		ToolName:         "write_file",
		PromptOnFirstUse: true,
		ArgsSummary:      "calls write_file with 1 path argument",
	}

	// First call: engine returns NotApplicable, prompt fires.
	err := EvaluateToolGate(context.Background(), in)
	if err != nil {
		t.Fatalf("first call with GrantAllowOnce should succeed, got: %v", err)
	}
	if len(registry.calls) != 1 {
		t.Fatalf("prompt should fire once on first call; called %d times", len(registry.calls))
	}
	// Verify the PromptSurface was populated correctly.
	s := registry.calls[0]
	if s.ServerName != "filesystem" || s.ToolName != "write_file" {
		t.Errorf("PromptSurface = %+v, want filesystem/write_file", s)
	}
	if s.ArgsSummary != "calls write_file with 1 path argument" {
		t.Errorf("ArgsSummary = %q, want redaction-safe summary", s.ArgsSummary)
	}
	// Transient cache should have been populated.
	if !cache.Has("sess-abc", "write_file") {
		t.Error("transient cache should have a grant for sess-abc/write_file after GrantAllowOnce")
	}
}

func TestEvaluateToolGate_TaggedTool_SecondCall_CacheHit_NoRePrompt(t *testing.T) {
	t.Parallel()
	// After a GrantAllowOnce the transient cache holds the grant.
	// The second call must skip the engine entirely and NOT re-prompt.
	gate := &fakeGate{outcome: cedar.NotApplicable}
	registry := &stubPromptRegistry{grant: GrantDeny} // would block if called
	cache := NewMemTransientGrantCache()
	cache.Set("sess-abc", "write_file") // simulate prior grant

	err := EvaluateToolGate(context.Background(), GateInput{
		Engine:           gate,
		Registry:         registry,
		Cache:            cache,
		SessionID:        "sess-abc",
		ServerName:       "filesystem",
		ToolName:         "write_file",
		PromptOnFirstUse: true,
	})
	if err != nil {
		t.Fatalf("cache hit should allow immediately, got: %v", err)
	}
	if gate.evaluations != 0 {
		t.Errorf("engine should NOT be called on cache hit; called %d times", gate.evaluations)
	}
	if len(registry.calls) != 0 {
		t.Errorf("prompt should NOT fire on cache hit; called %d times", len(registry.calls))
	}
}

func TestEvaluateToolGate_TaggedTool_GrantAllowAlways_CachePopulated(t *testing.T) {
	t.Parallel()
	gate := &fakeGate{outcome: cedar.NotApplicable}
	registry := &stubPromptRegistry{grant: GrantAllowAlways}
	cache := NewMemTransientGrantCache()

	err := EvaluateToolGate(context.Background(), GateInput{
		Engine:           gate,
		Registry:         registry,
		Cache:            cache,
		SessionID:        "sess-xyz",
		ServerName:       "filesystem",
		ToolName:         "delete_file",
		PromptOnFirstUse: true,
	})
	if err != nil {
		t.Fatalf("GrantAllowAlways should succeed, got: %v", err)
	}
	// GrantAllowAlways also populates the transient cache.
	if !cache.Has("sess-xyz", "delete_file") {
		t.Error("transient cache should be set for GrantAllowAlways")
	}
}

func TestEvaluateToolGate_TaggedTool_GrantDeny_Blocked(t *testing.T) {
	t.Parallel()
	gate := &fakeGate{outcome: cedar.NotApplicable}
	registry := &stubPromptRegistry{grant: GrantDeny}
	cache := NewMemTransientGrantCache()

	err := EvaluateToolGate(context.Background(), GateInput{
		Engine:           gate,
		Registry:         registry,
		Cache:            cache,
		SessionID:        "sess-1",
		ServerName:       "filesystem",
		ToolName:         "write_file",
		PromptOnFirstUse: true,
	})
	if err == nil {
		t.Fatal("GrantDeny should return an error")
	}
	if !IsToolPermissionDenied(err) {
		t.Errorf("err should be ToolPermissionDeniedError, got %T: %v", err, err)
	}
	var te *ToolPermissionDeniedError
	if errors.As(err, &te) {
		if te.ServerName != "filesystem" || te.ToolName != "write_file" {
			t.Errorf("ToolPermissionDeniedError fields: server=%q tool=%q", te.ServerName, te.ToolName)
		}
	}
	// Cache must NOT be populated after a denial.
	if cache.Has("sess-1", "write_file") {
		t.Error("transient cache must not be set after GrantDeny")
	}
}

func TestEvaluateToolGate_TaggedTool_PromptError_Blocked(t *testing.T) {
	t.Parallel()
	gate := &fakeGate{outcome: cedar.NotApplicable}
	promptErr := errors.New("prompt UI unavailable")
	registry := &stubPromptRegistry{err: promptErr}

	err := EvaluateToolGate(context.Background(), GateInput{
		Engine:           gate,
		Registry:         registry,
		ServerName:       "filesystem",
		ToolName:         "write_file",
		PromptOnFirstUse: true,
	})
	if err == nil {
		t.Fatal("prompt error should block dispatch")
	}
	if !IsToolPermissionDenied(err) {
		t.Errorf("err should be ToolPermissionDeniedError, got %T", err)
	}
	if !errors.Is(err, promptErr) {
		t.Errorf("err should wrap the prompt error; errors.Is(err, promptErr) = false")
	}
}

func TestEvaluateToolGate_TaggedTool_NilRegistry_SafeFailDeny(t *testing.T) {
	t.Parallel()
	// Engine returns NotApplicable, tool is tagged, but no registry.
	// Must safe-fail (deny) rather than silently allowing.
	gate := &fakeGate{outcome: cedar.NotApplicable}

	err := EvaluateToolGate(context.Background(), GateInput{
		Engine:           gate,
		Registry:         nil,
		ServerName:       "filesystem",
		ToolName:         "write_file",
		PromptOnFirstUse: true,
	})
	if err == nil {
		t.Fatal("nil registry with tagged tool should safe-fail")
	}
	if !IsToolPermissionDenied(err) {
		t.Errorf("err should be ToolPermissionDeniedError, got %T", err)
	}
}

func TestEvaluateToolGate_CedarDeny_Blocked(t *testing.T) {
	t.Parallel()
	// An explicit Cedar forbid policy matched — should block even if
	// the tool is not tagged for prompt-on-first-use.
	gate := &fakeGate{outcome: cedar.Deny, reason: "forbid policy matched"}
	registry := &stubPromptRegistry{grant: GrantAllowOnce}

	err := EvaluateToolGate(context.Background(), GateInput{
		Engine:           gate,
		Registry:         registry,
		ServerName:       "filesystem",
		ToolName:         "delete_file",
		PromptOnFirstUse: false,
	})
	if err == nil {
		t.Fatal("Cedar Deny should block")
	}
	if !IsToolPermissionDenied(err) {
		t.Errorf("err should be ToolPermissionDeniedError, got %T", err)
	}
	var te *ToolPermissionDeniedError
	if errors.As(err, &te) {
		if te.Reason != "forbid policy matched" {
			t.Errorf("Reason = %q, want 'forbid policy matched'", te.Reason)
		}
		// Wrapped should be a cedar.PolicyDeniedError.
		if !cedar.IsPolicyDenied(te.Wrapped) {
			t.Errorf("Wrapped should be cedar.PolicyDeniedError, got %T", te.Wrapped)
		}
	}
	// Prompt registry must NOT be called on a Cedar Deny.
	if len(registry.calls) != 0 {
		t.Errorf("prompt should not fire for Cedar Deny; called %d times", len(registry.calls))
	}
}

func TestIsToolPermissionDenied_NilAndNonDeny(t *testing.T) {
	t.Parallel()
	if IsToolPermissionDenied(nil) {
		t.Error("IsToolPermissionDenied(nil) = true, want false")
	}
	if IsToolPermissionDenied(errors.New("ordinary error")) {
		t.Error("IsToolPermissionDenied(ordinary error) = true, want false")
	}
	if !IsToolPermissionDenied(&ToolPermissionDeniedError{}) {
		t.Error("IsToolPermissionDenied(ToolPermissionDeniedError) = false, want true")
	}
}

func TestMemTransientGrantCache_HasSet(t *testing.T) {
	t.Parallel()
	c := NewMemTransientGrantCache()
	if c.Has("s1", "tool-a") {
		t.Error("fresh cache should not have any entries")
	}
	c.Set("s1", "tool-a")
	if !c.Has("s1", "tool-a") {
		t.Error("cache should have entry after Set")
	}
	// Different session — should not match.
	if c.Has("s2", "tool-a") {
		t.Error("cache hit leaked to different session")
	}
	// Different tool — should not match.
	if c.Has("s1", "tool-b") {
		t.Error("cache hit leaked to different tool")
	}
}

func TestEvaluateToolGate_RealCedarEngine_BuiltinAllow(t *testing.T) {
	t.Parallel()
	// Integration-style test: use the real Cedar engine with the
	// embedded default_tool_policy to verify kaneaz tools are Allowed
	// without a prompt, even when tagged.
	engine, err := cedar.NewEngine(cedar.Options{
		IncludeEmbedded: true,
		LoadFromDisk:    false,
	})
	if err != nil {
		t.Fatalf("NewEngine: %v", err)
	}
	registry := &stubPromptRegistry{grant: GrantDeny} // should not be called

	callErr := EvaluateToolGate(context.Background(), GateInput{
		Engine:           engine,
		Registry:         registry,
		ServerName:       "kaneaz",
		ToolName:         "bash",
		PromptOnFirstUse: true, // tagged, but policy allows → no prompt
	})
	if callErr != nil {
		t.Fatalf("kaneaz built-in should be Allowed by default policy, got: %v", callErr)
	}
	if len(registry.calls) != 0 {
		t.Errorf("prompt should not fire for Allow; called %d times", len(registry.calls))
	}
}

func TestEvaluateToolGate_RealCedarEngine_MCPToolNotApplicable_Tagged(t *testing.T) {
	t.Parallel()
	// A non-kaneaz MCP tool returns NotApplicable from the real engine.
	// With PromptOnFirstUse=true, the prompt fires.
	engine, err := cedar.NewEngine(cedar.Options{
		IncludeEmbedded: true,
		LoadFromDisk:    false,
	})
	if err != nil {
		t.Fatalf("NewEngine: %v", err)
	}
	registry := &stubPromptRegistry{grant: GrantAllowOnce}
	cache := NewMemTransientGrantCache()

	callErr := EvaluateToolGate(context.Background(), GateInput{
		Engine:           engine,
		Registry:         registry,
		Cache:            cache,
		SessionID:        "sess-real",
		ServerName:       "filesystem",
		ToolName:         "write_file",
		PromptOnFirstUse: true,
		ArgsSummary:      "calls write_file with 1 argument",
	})
	if callErr != nil {
		t.Fatalf("GrantAllowOnce should succeed, got: %v", callErr)
	}
	if len(registry.calls) != 1 {
		t.Errorf("prompt should fire exactly once; called %d times", len(registry.calls))
	}
	if !cache.Has("sess-real", "write_file") {
		t.Error("transient cache should be set after GrantAllowOnce")
	}
}

func TestEvaluateToolGate_RealCedarEngine_MCPToolNotApplicable_Untagged(t *testing.T) {
	t.Parallel()
	// A non-kaneaz MCP tool returns NotApplicable from the real engine.
	// With PromptOnFirstUse=false (untagged), the tool passes without prompt.
	engine, err := cedar.NewEngine(cedar.Options{
		IncludeEmbedded: true,
		LoadFromDisk:    false,
	})
	if err != nil {
		t.Fatalf("NewEngine: %v", err)
	}
	registry := &stubPromptRegistry{grant: GrantDeny}

	callErr := EvaluateToolGate(context.Background(), GateInput{
		Engine:           engine,
		Registry:         registry,
		ServerName:       "github",
		ToolName:         "list_issues",
		PromptOnFirstUse: false,
	})
	if callErr != nil {
		t.Fatalf("untagged tool with NotApplicable should pass, got: %v", callErr)
	}
	if len(registry.calls) != 0 {
		t.Errorf("prompt should not fire for untagged tool; called %d times", len(registry.calls))
	}
}
