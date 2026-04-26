package toolloop

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"sync"
	"testing"

	corellm "github.com/sigil-tech/kaneaz-harness/core/llm"
	"github.com/sigil-tech/kaneaz-harness/core/event/redact"
)

// captureHookRunner records every pre/post invocation and lets tests
// script the pre-hook reply.
type captureHookRunner struct {
	mu       sync.Mutex
	preEvts  []PreToolUseEvent
	postEvts []PostToolUseEvent
	preFn    func(PreToolUseEvent) (PreToolUseResult, error)
}

func (r *captureHookRunner) RunPreToolUse(_ context.Context, ev PreToolUseEvent) (PreToolUseResult, error) {
	r.mu.Lock()
	r.preEvts = append(r.preEvts, ev)
	fn := r.preFn
	r.mu.Unlock()
	if fn != nil {
		return fn(ev)
	}
	return PreToolUseResult{Continue: true}, nil
}

func (r *captureHookRunner) RunPostToolUse(_ context.Context, ev PostToolUseEvent) {
	r.mu.Lock()
	r.postEvts = append(r.postEvts, ev)
	r.mu.Unlock()
}

func (r *captureHookRunner) snapshotPre() []PreToolUseEvent {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make([]PreToolUseEvent, len(r.preEvts))
	copy(out, r.preEvts)
	return out
}

func (r *captureHookRunner) snapshotPost() []PostToolUseEvent {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make([]PostToolUseEvent, len(r.postEvts))
	copy(out, r.postEvts)
	return out
}

// captureAuditEmitter records emitted attrs after running them through
// a real redact.Pipeline so tests can assert that no raw secret survives.
// This mirrors the production wiring: event.Emitter.Append redacts the
// payload before persistence (NFR-003 / C-004).
type captureAuditEmitter struct {
	mu        sync.Mutex
	pipeline  *redact.Pipeline
	captured  []capturedAuditEntry
}

type capturedAuditEntry struct {
	Kind     string
	RawJSON  string
	AttrsMap map[string]any
}

func newCaptureAuditEmitter(t *testing.T) *captureAuditEmitter {
	t.Helper()
	resolver := redact.NewStaticResolver(map[string][]byte{
		"event-log-redaction-salt": []byte("test-salt"),
	})
	pipeline, err := redact.New(redact.DefaultPolicy(), resolver)
	if err != nil {
		t.Fatalf("redact.New: %v", err)
	}
	return &captureAuditEmitter{pipeline: pipeline}
}

func (c *captureAuditEmitter) Emit(_ context.Context, kind string, attrs map[string]any) {
	cleaned, _, err := c.pipeline.Apply(attrs)
	if err != nil {
		// surface as an entry with the error so tests can fail clearly
		c.mu.Lock()
		c.captured = append(c.captured, capturedAuditEntry{Kind: kind, RawJSON: "ERR:" + err.Error()})
		c.mu.Unlock()
		return
	}
	var asMap map[string]any
	_ = json.Unmarshal(cleaned, &asMap)
	c.mu.Lock()
	c.captured = append(c.captured, capturedAuditEntry{
		Kind:     kind,
		RawJSON:  string(cleaned),
		AttrsMap: asMap,
	})
	c.mu.Unlock()
}

func (c *captureAuditEmitter) snapshot() []capturedAuditEntry {
	c.mu.Lock()
	defer c.mu.Unlock()
	out := make([]capturedAuditEntry, len(c.captured))
	copy(out, c.captured)
	return out
}

func (c *captureAuditEmitter) byKind(kind string) []capturedAuditEntry {
	c.mu.Lock()
	defer c.mu.Unlock()
	var out []capturedAuditEntry
	for _, e := range c.captured {
		if e.Kind == kind {
			out = append(out, e)
		}
	}
	return out
}

// TestHooks_PreToolUseReceivesEvent verifies the pre-hook gets the
// session, tool, server, and args fields populated correctly.
func TestHooks_PreToolUseReceivesEvent(t *testing.T) {
	pool := newFakePool()
	pool.register("github", "get_issue", func(_ context.Context, _ json.RawMessage) (json.RawMessage, error) {
		return json.RawMessage(`"ok"`), nil
	})
	reg := &fakeRegistry{
		streams: []corellm.Stream{
			&scriptedStream{final: corellm.Response{
				Content:      []corellm.ContentBlock{{Type: "text", Text: "done"}},
				FinishReason: "end_turn",
			}},
		},
	}
	hooks := &captureHookRunner{}
	loop, err := New(Config{Registry: reg, Pool: pool, Hooks: hooks})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	initial := &corellm.Response{
		FinishReason: "tool_use",
		ToolCalls: []corellm.ToolUse{
			{ID: "t1", Name: "get_issue", Input: json.RawMessage(`{"number":42}`)},
		},
	}
	if err := loop.Run(context.Background(), "sess-1", "sub-1", initial, corellm.GenerationRequest{}); err != nil {
		t.Fatalf("Run: %v", err)
	}
	preEvts := hooks.snapshotPre()
	if len(preEvts) != 1 {
		t.Fatalf("pre events = %d, want 1", len(preEvts))
	}
	got := preEvts[0]
	if got.SessionID != "sess-1" {
		t.Errorf("session_id = %q, want sess-1", got.SessionID)
	}
	if got.Tool != "get_issue" {
		t.Errorf("tool = %q, want get_issue", got.Tool)
	}
	if got.Server != "github" {
		t.Errorf("server = %q, want github", got.Server)
	}
	if string(got.Args) != `{"number":42}` {
		t.Errorf("args = %q, want %q", got.Args, `{"number":42}`)
	}
	if got.AttemptNo != 1 {
		t.Errorf("attempt_no = %d, want 1", got.AttemptNo)
	}
}

// TestHooks_PreToolUseShortCircuits verifies Continue=false skips
// pool.Call and surfaces a synthetic tool_blocked into the conversation.
func TestHooks_PreToolUseShortCircuits(t *testing.T) {
	pool := newFakePool()
	pool.register("filesystem", "delete", func(_ context.Context, _ json.RawMessage) (json.RawMessage, error) {
		t.Fatal("pool.Call must not run when pre-hook denies")
		return nil, nil
	})
	reg := &fakeRegistry{
		streams: []corellm.Stream{
			&scriptedStream{final: corellm.Response{
				Content:      []corellm.ContentBlock{{Type: "text", Text: "ok, skipping delete"}},
				FinishReason: "end_turn",
			}},
		},
	}
	hooks := &captureHookRunner{
		preFn: func(_ PreToolUseEvent) (PreToolUseResult, error) {
			return PreToolUseResult{Continue: false, Reason: "policy: no deletes"}, nil
		},
	}
	loop, _ := New(Config{Registry: reg, Pool: pool, Hooks: hooks})
	initial := &corellm.Response{
		FinishReason: "tool_use",
		ToolCalls: []corellm.ToolUse{
			{ID: "t1", Name: "delete", Input: json.RawMessage(`{"path":"/tmp/x"}`)},
		},
	}
	if err := loop.Run(context.Background(), "sess", "sub", initial, corellm.GenerationRequest{}); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if len(pool.calls) != 0 {
		t.Fatalf("pool.calls = %d, want 0 — pre-hook deny must skip dispatch", len(pool.calls))
	}
	// The conversation must continue: the registry was re-invoked with
	// the synthetic blocked result threaded as a tool message.
	if len(reg.requests) != 1 {
		t.Fatalf("registry requests = %d, want 1", len(reg.requests))
	}
	last := reg.requests[0].Messages[len(reg.requests[0].Messages)-1]
	if last.Role != corellm.RoleTool {
		t.Fatalf("last msg role = %q, want tool", last.Role)
	}
	var payload map[string]any
	_ = json.Unmarshal(last.Content[0].ToolData, &payload)
	if payload["is_error"] != true {
		t.Fatalf("is_error = %v, want true", payload["is_error"])
	}
	output, _ := payload["output"].(string)
	if !strings.Contains(output, "policy: no deletes") {
		t.Fatalf("output = %q, missing pre-hook reason", output)
	}
	// Post-hook still fires on a pre-hook short-circuit so consumers
	// (like a memory-persist hook) still observe the blocked attempt.
	if len(hooks.snapshotPost()) != 1 {
		t.Fatalf("post events = %d, want 1 (post fires even on block)", len(hooks.snapshotPost()))
	}
}

// TestHooks_PreToolUseMutatesArgs verifies a non-nil Args reply
// replaces the bytes that reach pool.Call.
func TestHooks_PreToolUseMutatesArgs(t *testing.T) {
	pool := newFakePool()
	pool.register("filesystem", "read_file", func(_ context.Context, args json.RawMessage) (json.RawMessage, error) {
		return json.RawMessage(`{"contents":"hello"}`), nil
	})
	reg := &fakeRegistry{
		streams: []corellm.Stream{
			&scriptedStream{final: corellm.Response{
				Content:      []corellm.ContentBlock{{Type: "text", Text: "done"}},
				FinishReason: "end_turn",
			}},
		},
	}
	hooks := &captureHookRunner{
		preFn: func(ev PreToolUseEvent) (PreToolUseResult, error) {
			// Hook replaces the path with a redacted form before dispatch.
			return PreToolUseResult{
				Continue: true,
				Args:     json.RawMessage(`{"path":"/tmp/redacted"}`),
			}, nil
		},
	}
	loop, _ := New(Config{Registry: reg, Pool: pool, Hooks: hooks})
	initial := &corellm.Response{
		FinishReason: "tool_use",
		ToolCalls: []corellm.ToolUse{
			{ID: "t1", Name: "read_file", Input: json.RawMessage(`{"path":"/Users/alice/.ssh/id_rsa"}`)},
		},
	}
	if err := loop.Run(context.Background(), "sess", "sub", initial, corellm.GenerationRequest{}); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if len(pool.calls) != 1 {
		t.Fatalf("pool.calls = %d, want 1", len(pool.calls))
	}
	// The args that reached the pool MUST be the hook's mutation, not
	// the model's original args.
	if string(pool.calls[0].args) != `{"path":"/tmp/redacted"}` {
		t.Fatalf("dispatched args = %s, want /tmp/redacted (mutated)", pool.calls[0].args)
	}
	// Post-hook receives the post-mutation args.
	postEvts := hooks.snapshotPost()
	if len(postEvts) != 1 {
		t.Fatalf("post events = %d, want 1", len(postEvts))
	}
	if string(postEvts[0].Args) != `{"path":"/tmp/redacted"}` {
		t.Fatalf("post args = %s, want mutated", postEvts[0].Args)
	}
}

// TestHooks_PostToolUseFiresOnSuccess verifies the post-hook runs after
// a successful dispatch with the result and latency populated.
func TestHooks_PostToolUseFiresOnSuccess(t *testing.T) {
	pool := newFakePool()
	pool.register("svc", "ok", func(_ context.Context, _ json.RawMessage) (json.RawMessage, error) {
		return json.RawMessage(`{"value":1}`), nil
	})
	reg := &fakeRegistry{
		streams: []corellm.Stream{
			&scriptedStream{final: corellm.Response{
				Content:      []corellm.ContentBlock{{Type: "text", Text: "done"}},
				FinishReason: "end_turn",
			}},
		},
	}
	hooks := &captureHookRunner{}
	loop, _ := New(Config{Registry: reg, Pool: pool, Hooks: hooks})
	initial := &corellm.Response{
		FinishReason: "tool_use",
		ToolCalls:    []corellm.ToolUse{{ID: "1", Name: "ok", Input: json.RawMessage(`{}`)}},
	}
	if err := loop.Run(context.Background(), "sess", "sub", initial, corellm.GenerationRequest{}); err != nil {
		t.Fatalf("Run: %v", err)
	}
	postEvts := hooks.snapshotPost()
	if len(postEvts) != 1 {
		t.Fatalf("post events = %d, want 1", len(postEvts))
	}
	got := postEvts[0]
	if got.Tool != "ok" || got.Server != "svc" {
		t.Errorf("post (server, tool) = (%q, %q)", got.Server, got.Tool)
	}
	if string(got.Result) != `{"value":1}` {
		t.Errorf("post result = %s, want {value:1}", got.Result)
	}
	if got.Error != "" {
		t.Errorf("post error = %q, want empty on success", got.Error)
	}
	if got.LatencyMS < 0 {
		t.Errorf("latency_ms = %d, want >= 0", got.LatencyMS)
	}
}

// TestHooks_PostToolUseFiresOnError verifies the post-hook runs on
// dispatch error with the error message populated.
func TestHooks_PostToolUseFiresOnError(t *testing.T) {
	pool := newFakePool()
	pool.register("svc", "boom", func(_ context.Context, _ json.RawMessage) (json.RawMessage, error) {
		return nil, errors.New("upstream 500")
	})
	reg := &fakeRegistry{
		streams: []corellm.Stream{
			&scriptedStream{final: corellm.Response{
				Content:      []corellm.ContentBlock{{Type: "text", Text: "sorry"}},
				FinishReason: "end_turn",
			}},
		},
	}
	hooks := &captureHookRunner{}
	loop, _ := New(Config{Registry: reg, Pool: pool, Hooks: hooks})
	initial := &corellm.Response{
		FinishReason: "tool_use",
		ToolCalls:    []corellm.ToolUse{{ID: "1", Name: "boom", Input: json.RawMessage(`{}`)}},
	}
	if err := loop.Run(context.Background(), "sess", "sub", initial, corellm.GenerationRequest{}); err != nil {
		t.Fatalf("Run: %v", err)
	}
	postEvts := hooks.snapshotPost()
	if len(postEvts) != 1 {
		t.Fatalf("post events = %d, want 1", len(postEvts))
	}
	if !strings.Contains(postEvts[0].Error, "upstream 500") {
		t.Fatalf("post error = %q, want contains upstream 500", postEvts[0].Error)
	}
	if len(postEvts[0].Result) != 0 {
		t.Errorf("post result on error = %s, want empty", postEvts[0].Result)
	}
}

// TestAudit_ToolInvokedOnSuccess verifies a successful dispatch emits
// tool_invoked with redacted args.
func TestAudit_ToolInvokedOnSuccess(t *testing.T) {
	pool := newFakePool()
	pool.register("svc", "ok", func(_ context.Context, _ json.RawMessage) (json.RawMessage, error) {
		return json.RawMessage(`"ok"`), nil
	})
	reg := &fakeRegistry{
		streams: []corellm.Stream{
			&scriptedStream{final: corellm.Response{
				Content:      []corellm.ContentBlock{{Type: "text", Text: "done"}},
				FinishReason: "end_turn",
			}},
		},
	}
	emitter := newCaptureAuditEmitter(t)
	loop, _ := New(Config{Registry: reg, Pool: pool, Audit: emitter})
	initial := &corellm.Response{
		FinishReason: "tool_use",
		ToolCalls:    []corellm.ToolUse{{ID: "1", Name: "ok", Input: json.RawMessage(`{"foo":"bar"}`)}},
	}
	if err := loop.Run(context.Background(), "sess-1", "sub-1", initial, corellm.GenerationRequest{}); err != nil {
		t.Fatalf("Run: %v", err)
	}
	invoked := emitter.byKind("tool_invoked")
	if len(invoked) != 1 {
		t.Fatalf("tool_invoked count = %d, want 1", len(invoked))
	}
	if invoked[0].AttrsMap["server"] != "svc" {
		t.Errorf("server attr = %v, want svc", invoked[0].AttrsMap["server"])
	}
	if invoked[0].AttrsMap["tool"] != "ok" {
		t.Errorf("tool attr = %v, want ok", invoked[0].AttrsMap["tool"])
	}
	if len(emitter.byKind("tool_failed")) != 0 {
		t.Errorf("tool_failed count = %d, want 0 on success", len(emitter.byKind("tool_failed")))
	}
}

// TestAudit_ToolFailedOnPoolError verifies a dispatch failure emits
// tool_failed.
func TestAudit_ToolFailedOnPoolError(t *testing.T) {
	pool := newFakePool()
	pool.register("svc", "boom", func(_ context.Context, _ json.RawMessage) (json.RawMessage, error) {
		return nil, errors.New("upstream 500")
	})
	reg := &fakeRegistry{
		streams: []corellm.Stream{
			&scriptedStream{final: corellm.Response{
				Content:      []corellm.ContentBlock{{Type: "text", Text: "sorry"}},
				FinishReason: "end_turn",
			}},
		},
	}
	emitter := newCaptureAuditEmitter(t)
	loop, _ := New(Config{Registry: reg, Pool: pool, Audit: emitter})
	initial := &corellm.Response{
		FinishReason: "tool_use",
		ToolCalls:    []corellm.ToolUse{{ID: "1", Name: "boom", Input: json.RawMessage(`{}`)}},
	}
	if err := loop.Run(context.Background(), "sess", "sub", initial, corellm.GenerationRequest{}); err != nil {
		t.Fatalf("Run: %v", err)
	}
	failed := emitter.byKind("tool_failed")
	if len(failed) != 1 {
		t.Fatalf("tool_failed count = %d, want 1", len(failed))
	}
	if !strings.Contains(failed[0].RawJSON, "upstream 500") {
		t.Errorf("tool_failed error not surfaced: %s", failed[0].RawJSON)
	}
}

// TestAudit_RedactsSecretArgs verifies NFR-003: a credential-shaped
// substring in args does NOT appear verbatim in any audit attr.
func TestAudit_RedactsSecretArgs(t *testing.T) {
	const sentinel = "test_secret_xyz_should_never_leak_to_audit"
	pool := newFakePool()
	pool.register("brave", "search", func(_ context.Context, _ json.RawMessage) (json.RawMessage, error) {
		return json.RawMessage(`"results"`), nil
	})
	reg := &fakeRegistry{
		streams: []corellm.Stream{
			&scriptedStream{final: corellm.Response{
				Content:      []corellm.ContentBlock{{Type: "text", Text: "done"}},
				FinishReason: "end_turn",
			}},
		},
	}
	emitter := newCaptureAuditEmitter(t)
	loop, _ := New(Config{Registry: reg, Pool: pool, Audit: emitter})
	// Embed a sentinel in args under a credential-shaped key. The
	// generic-password-querystring matcher in the default redactor
	// matches "BRAVE_API_KEY=<value>" — the value (the sentinel) gets
	// replaced with a deterministic placeholder before emission.
	argsJSON := `{"query":"hello","auth":"BRAVE_API_KEY=` + sentinel + `"}`
	initial := &corellm.Response{
		FinishReason: "tool_use",
		ToolCalls:    []corellm.ToolUse{{ID: "1", Name: "search", Input: json.RawMessage(argsJSON)}},
	}
	if err := loop.Run(context.Background(), "sess", "sub", initial, corellm.GenerationRequest{}); err != nil {
		t.Fatalf("Run: %v", err)
	}
	all := emitter.snapshot()
	if len(all) == 0 {
		t.Fatal("no audit events emitted")
	}
	for _, ev := range all {
		if strings.Contains(ev.RawJSON, sentinel) {
			t.Fatalf("sentinel %q leaked into audit %s payload: %s", sentinel, ev.Kind, ev.RawJSON)
		}
	}
}

// TestAudit_ToolFailedOnPreHookDeny verifies a pre-hook short-circuit
// emits tool_failed (not tool_invoked) and surfaces the reason.
func TestAudit_ToolFailedOnPreHookDeny(t *testing.T) {
	pool := newFakePool()
	pool.register("filesystem", "delete", func(_ context.Context, _ json.RawMessage) (json.RawMessage, error) {
		t.Fatal("pool.Call must not run on pre-hook deny")
		return nil, nil
	})
	reg := &fakeRegistry{
		streams: []corellm.Stream{
			&scriptedStream{final: corellm.Response{
				Content:      []corellm.ContentBlock{{Type: "text", Text: "ok"}},
				FinishReason: "end_turn",
			}},
		},
	}
	emitter := newCaptureAuditEmitter(t)
	hooks := &captureHookRunner{
		preFn: func(_ PreToolUseEvent) (PreToolUseResult, error) {
			return PreToolUseResult{Continue: false, Reason: "denied by pre-hook"}, nil
		},
	}
	loop, _ := New(Config{Registry: reg, Pool: pool, Hooks: hooks, Audit: emitter})
	initial := &corellm.Response{
		FinishReason: "tool_use",
		ToolCalls:    []corellm.ToolUse{{ID: "1", Name: "delete", Input: json.RawMessage(`{}`)}},
	}
	if err := loop.Run(context.Background(), "sess", "sub", initial, corellm.GenerationRequest{}); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if len(emitter.byKind("tool_invoked")) != 0 {
		t.Errorf("tool_invoked emitted on deny")
	}
	failed := emitter.byKind("tool_failed")
	if len(failed) != 1 {
		t.Fatalf("tool_failed count = %d, want 1", len(failed))
	}
	if !strings.Contains(failed[0].RawJSON, "denied by pre-hook") {
		t.Errorf("reason not in audit: %s", failed[0].RawJSON)
	}
}

// TestHooks_PreToolUseError treats a hook error as a synthetic deny
// (model still gets a tool_result; audit logs tool_failed).
func TestHooks_PreToolUseError(t *testing.T) {
	pool := newFakePool()
	pool.register("svc", "tool", func(_ context.Context, _ json.RawMessage) (json.RawMessage, error) {
		t.Fatal("pool.Call must not run when pre-hook errors")
		return nil, nil
	})
	reg := &fakeRegistry{
		streams: []corellm.Stream{
			&scriptedStream{final: corellm.Response{
				Content:      []corellm.ContentBlock{{Type: "text", Text: "ok"}},
				FinishReason: "end_turn",
			}},
		},
	}
	hooks := &captureHookRunner{
		preFn: func(_ PreToolUseEvent) (PreToolUseResult, error) {
			return PreToolUseResult{}, errors.New("hook subsystem down")
		},
	}
	emitter := newCaptureAuditEmitter(t)
	loop, _ := New(Config{Registry: reg, Pool: pool, Hooks: hooks, Audit: emitter})
	initial := &corellm.Response{
		FinishReason: "tool_use",
		ToolCalls:    []corellm.ToolUse{{ID: "1", Name: "tool", Input: json.RawMessage(`{}`)}},
	}
	if err := loop.Run(context.Background(), "sess", "sub", initial, corellm.GenerationRequest{}); err != nil {
		t.Fatalf("Run: %v", err)
	}
	failed := emitter.byKind("tool_failed")
	if len(failed) != 1 {
		t.Fatalf("tool_failed count = %d, want 1", len(failed))
	}
	if !strings.Contains(failed[0].RawJSON, "hook subsystem down") {
		t.Errorf("hook error not in audit: %s", failed[0].RawJSON)
	}
}
