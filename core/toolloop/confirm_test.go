package toolloop

import (
	"context"
	"encoding/json"
	"errors"
	"sync"
	"testing"
	"time"

	corellm "github.com/sigil-tech/kaneaz-harness/core/llm"
)

// recordingGateway captures every RequestConfirm call and returns a
// queued sequence of decisions. Goroutine-safe so the concurrent
// dispatcher can hammer it from multiple workers.
type recordingGateway struct {
	mu        sync.Mutex
	requests  []ConfirmRequest
	decisions []ConfirmDecision
	err       error
	// block, when set, causes RequestConfirm to wait on it before
	// returning. Used by the cancellation test to drive a goroutine
	// into the blocked state and assert ctx-cancel unblocks it.
	block <-chan struct{}
}

func (g *recordingGateway) RequestConfirm(ctx context.Context, req ConfirmRequest) (ConfirmDecision, error) {
	g.mu.Lock()
	g.requests = append(g.requests, req)
	idx := len(g.requests) - 1
	err := g.err
	block := g.block
	var d ConfirmDecision
	if idx < len(g.decisions) {
		d = g.decisions[idx]
	} else if len(g.decisions) > 0 {
		d = g.decisions[len(g.decisions)-1]
	} else {
		d = ConfirmAllow
	}
	g.mu.Unlock()
	if block != nil {
		select {
		case <-block:
		case <-ctx.Done():
			return "", ctx.Err()
		}
	}
	if err != nil {
		return "", err
	}
	return d, nil
}

func (g *recordingGateway) snapshot() []ConfirmRequest {
	g.mu.Lock()
	defer g.mu.Unlock()
	out := make([]ConfirmRequest, len(g.requests))
	copy(out, g.requests)
	return out
}

// recordingOverrideWriter captures every SetMCPOverride call.
type recordingOverrideWriter struct {
	mu        sync.Mutex
	overrides []recordedOverride
	err       error
}

type recordedOverride struct {
	SessionID string
	Override  MCPOverride
}

func (w *recordingOverrideWriter) SetMCPOverride(_ context.Context, sessionID string, ov MCPOverride) error {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.err != nil {
		return w.err
	}
	w.overrides = append(w.overrides, recordedOverride{SessionID: sessionID, Override: ov})
	return nil
}

func (w *recordingOverrideWriter) snapshot() []recordedOverride {
	w.mu.Lock()
	defer w.mu.Unlock()
	out := make([]recordedOverride, len(w.overrides))
	copy(out, w.overrides)
	return out
}

// confirmEachResolver returns PolicyConfirmEach for every (server,
// tool) match. Used as a one-liner permission resolver in the WP05
// tests so we don't have to recreate the static-resolver wiring per
// test.
type confirmEachResolver struct{}

func (confirmEachResolver) Resolve(_ context.Context, _, server, tool string) (Resolution, error) {
	return Resolution{Server: server, Tool: tool, Policy: PolicyConfirmEach}, nil
}

// helper: builds a Loop wired with a single-tool fake pool, a fake
// registry that responds with end_turn after the tool result is
// threaded, and the supplied gateway / override writer / flag.
func newConfirmTestLoop(t *testing.T, gw ConfirmGateway, ow SessionOverrideWriter, enabled bool) (*Loop, *fakePool, *fakeRegistry, *recordingHistory) {
	t.Helper()
	pool := newFakePool()
	pool.register("svc", "tool", func(_ context.Context, args json.RawMessage) (json.RawMessage, error) {
		return json.RawMessage(`"ok"`), nil
	})
	reg := &fakeRegistry{
		streams: []corellm.Stream{
			&scriptedStream{final: corellm.Response{
				Content:      []corellm.ContentPart{{Type: "text", Text: "done"}},
				FinishReason: "end_turn",
			}},
		},
	}
	hist := &recordingHistory{}
	loop, err := New(Config{
		Registry:           reg,
		Pool:               pool,
		History:            hist,
		Permissions:        confirmEachResolver{},
		Confirm:            gw,
		ConfirmEachEnabled: enabled,
		OverrideWriter:     ow,
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return loop, pool, reg, hist
}

func toolUseInitial() *corellm.Response {
	return &corellm.Response{
		FinishReason: "tool_use",
		ToolCalls: []corellm.ToolUse{
			{ID: "t-1", Name: "tool", Input: json.RawMessage(`{"k":"v"}`)},
		},
	}
}

// TestConfirm_AllowDispatches verifies the gateway's allow decision
// dispatches the call exactly as auto_allow would.
func TestConfirm_AllowDispatches(t *testing.T) {
	gw := &recordingGateway{decisions: []ConfirmDecision{ConfirmAllow}}
	ow := &recordingOverrideWriter{}
	loop, pool, _, _ := newConfirmTestLoop(t, gw, ow, true)

	if err := loop.Run(context.Background(), "sess-1", "sub-1", toolUseInitial(), corellm.GenerationRequest{}); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if len(gw.snapshot()) != 1 {
		t.Fatalf("gateway called %d times, want 1", len(gw.snapshot()))
	}
	if len(pool.calls) != 1 {
		t.Fatalf("pool calls = %d, want 1", len(pool.calls))
	}
	if len(ow.snapshot()) != 0 {
		t.Fatalf("override writes = %d, want 0", len(ow.snapshot()))
	}
}

// TestConfirm_DenyBlocks verifies the gateway's deny decision blocks
// dispatch and surfaces a synthetic tool_blocked.
func TestConfirm_DenyBlocks(t *testing.T) {
	gw := &recordingGateway{decisions: []ConfirmDecision{ConfirmDeny}}
	ow := &recordingOverrideWriter{}
	loop, pool, reg, _ := newConfirmTestLoop(t, gw, ow, true)

	if err := loop.Run(context.Background(), "sess-1", "sub-1", toolUseInitial(), corellm.GenerationRequest{}); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if len(pool.calls) != 0 {
		t.Fatalf("pool.Call ran %d times, want 0 (deny short-circuits)", len(pool.calls))
	}
	if len(ow.snapshot()) != 0 {
		t.Fatalf("deny must not write override; got %d", len(ow.snapshot()))
	}
	// The tool result threaded back to the registry must be marked as
	// an error with the user-blocked reason.
	if len(reg.requests) == 0 {
		t.Fatalf("registry never re-invoked")
	}
	msgs := reg.requests[0].Messages
	if len(msgs) < 2 {
		t.Fatalf("messages = %d, want at least 2 (assistant + tool result)", len(msgs))
	}
	toolMsg := msgs[len(msgs)-1]
	if toolMsg.Role != corellm.RoleTool {
		t.Fatalf("last message role = %q, want tool", toolMsg.Role)
	}
	var envelope map[string]any
	if err := json.Unmarshal(toolMsg.Content[0].ToolData, &envelope); err != nil {
		t.Fatalf("unmarshal envelope: %v", err)
	}
	if envelope["is_error"] != true {
		t.Fatalf("is_error = %v, want true", envelope["is_error"])
	}
	out, _ := envelope["output"].(string)
	if out == "" || !contains(out, "blocked by user") {
		t.Fatalf("output %q does not mention 'blocked by user'", out)
	}
}

// TestConfirm_AlwaysAllowWritesOverrideAndDispatches verifies the
// always_allow decision dispatches AND writes a session-scope
// auto_allow override for (server, tool).
func TestConfirm_AlwaysAllowWritesOverrideAndDispatches(t *testing.T) {
	gw := &recordingGateway{decisions: []ConfirmDecision{ConfirmAlwaysAllow}}
	ow := &recordingOverrideWriter{}
	loop, pool, _, _ := newConfirmTestLoop(t, gw, ow, true)

	if err := loop.Run(context.Background(), "sess-1", "sub-1", toolUseInitial(), corellm.GenerationRequest{}); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if len(pool.calls) != 1 {
		t.Fatalf("pool.Call ran %d times, want 1 (always_allow dispatches)", len(pool.calls))
	}
	overrides := ow.snapshot()
	if len(overrides) != 1 {
		t.Fatalf("override writes = %d, want 1", len(overrides))
	}
	if overrides[0].SessionID != "sess-1" {
		t.Fatalf("override session = %q, want sess-1", overrides[0].SessionID)
	}
	if overrides[0].Override.Server != "svc" || overrides[0].Override.Tool != "tool" {
		t.Fatalf("override target = (%s, %s), want (svc, tool)", overrides[0].Override.Server, overrides[0].Override.Tool)
	}
	if overrides[0].Override.Policy != PolicyAutoAllow {
		t.Fatalf("override policy = %q, want auto_allow", overrides[0].Override.Policy)
	}
}

// TestConfirm_AlwaysDenyWritesOverrideAndBlocks verifies the
// always_deny decision blocks dispatch AND writes a session-scope
// deny override.
func TestConfirm_AlwaysDenyWritesOverrideAndBlocks(t *testing.T) {
	gw := &recordingGateway{decisions: []ConfirmDecision{ConfirmAlwaysDeny}}
	ow := &recordingOverrideWriter{}
	loop, pool, _, _ := newConfirmTestLoop(t, gw, ow, true)

	if err := loop.Run(context.Background(), "sess-1", "sub-1", toolUseInitial(), corellm.GenerationRequest{}); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if len(pool.calls) != 0 {
		t.Fatalf("pool.Call ran %d times, want 0 (always_deny blocks)", len(pool.calls))
	}
	overrides := ow.snapshot()
	if len(overrides) != 1 {
		t.Fatalf("override writes = %d, want 1", len(overrides))
	}
	if overrides[0].Override.Policy != PolicyDeny {
		t.Fatalf("override policy = %q, want deny", overrides[0].Override.Policy)
	}
}

// TestConfirm_FeatureFlagOffDegradesToAutoAllow verifies that when
// ConfirmEachEnabled is false the gateway is never invoked and the
// loop dispatches normally — preserves WP04 behaviour.
func TestConfirm_FeatureFlagOffDegradesToAutoAllow(t *testing.T) {
	gw := &recordingGateway{decisions: []ConfirmDecision{ConfirmDeny}}
	ow := &recordingOverrideWriter{}
	loop, pool, _, _ := newConfirmTestLoop(t, gw, ow, false /* flag off */)

	if err := loop.Run(context.Background(), "sess-1", "sub-1", toolUseInitial(), corellm.GenerationRequest{}); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if len(gw.snapshot()) != 0 {
		t.Fatalf("gateway invoked %d times with flag off, want 0", len(gw.snapshot()))
	}
	if len(pool.calls) != 1 {
		t.Fatalf("pool.Call ran %d times, want 1 (flag-off dispatches)", len(pool.calls))
	}
}

// TestConfirm_NilGatewayDegradesToAutoAllow verifies that when
// Confirm is nil the loop falls back to the noop gateway (allow) even
// with the flag on. Defensive: the rpc layer can wire the flag on
// before plumbing the gateway and the chat surface should not stall.
func TestConfirm_NilGatewayDegradesToAutoAllow(t *testing.T) {
	loop, pool, _, _ := newConfirmTestLoop(t, nil, nil, true)
	if err := loop.Run(context.Background(), "sess-1", "sub-1", toolUseInitial(), corellm.GenerationRequest{}); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if len(pool.calls) != 1 {
		t.Fatalf("pool.Call ran %d times, want 1", len(pool.calls))
	}
}

// TestConfirm_CtxCancelDuringWaitYieldsCancelled verifies that
// cancelling the host ctx while the gateway is blocked surfaces a
// tool_cancelled record (not a tool_blocked) and the loop unwinds.
func TestConfirm_CtxCancelDuringWaitYieldsCancelled(t *testing.T) {
	block := make(chan struct{})
	defer close(block)
	gw := &recordingGateway{
		decisions: []ConfirmDecision{ConfirmAllow},
		block:     block,
	}
	ow := &recordingOverrideWriter{}
	loop, pool, _, _ := newConfirmTestLoop(t, gw, ow, true)

	ctx, cancel := context.WithCancel(context.Background())
	doneCh := make(chan error, 1)
	go func() {
		doneCh <- loop.Run(ctx, "sess-1", "sub-1", toolUseInitial(), corellm.GenerationRequest{})
	}()
	// Give the goroutine a moment to enter RequestConfirm.
	time.Sleep(20 * time.Millisecond)
	cancel()

	select {
	case err := <-doneCh:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("Run err = %v, want context.Canceled", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("Run did not unwind after ctx cancel")
	}
	if len(pool.calls) != 0 {
		t.Fatalf("pool.Call ran %d times, want 0 (cancel before dispatch)", len(pool.calls))
	}
}

// TestConfirm_GatewayErrorBlocks verifies that a non-cancel error from
// the gateway surfaces as a tool_blocked with the gateway error in the
// reason — defensive so a busted modal plumbing can't silently allow
// tool calls.
func TestConfirm_GatewayErrorBlocks(t *testing.T) {
	gwErr := errors.New("gateway broken")
	gw := &recordingGateway{err: gwErr}
	ow := &recordingOverrideWriter{}
	loop, pool, reg, _ := newConfirmTestLoop(t, gw, ow, true)
	if err := loop.Run(context.Background(), "sess-1", "sub-1", toolUseInitial(), corellm.GenerationRequest{}); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if len(pool.calls) != 0 {
		t.Fatalf("pool.Call ran %d times, want 0 (gateway error blocks)", len(pool.calls))
	}
	msgs := reg.requests[0].Messages
	toolMsg := msgs[len(msgs)-1]
	var envelope map[string]any
	_ = json.Unmarshal(toolMsg.Content[0].ToolData, &envelope)
	if envelope["is_error"] != true {
		t.Fatalf("is_error = %v, want true", envelope["is_error"])
	}
}

// TestConfirm_AlwaysAllowOverrideWriteFailureStillDispatches verifies
// that an override-writer error does NOT abort the call: the per-call
// allow is honored, only the persistence is dropped (logged).
func TestConfirm_AlwaysAllowOverrideWriteFailureStillDispatches(t *testing.T) {
	gw := &recordingGateway{decisions: []ConfirmDecision{ConfirmAlwaysAllow}}
	ow := &recordingOverrideWriter{err: errors.New("disk full")}
	loop, pool, _, _ := newConfirmTestLoop(t, gw, ow, true)
	if err := loop.Run(context.Background(), "sess-1", "sub-1", toolUseInitial(), corellm.GenerationRequest{}); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if len(pool.calls) != 1 {
		t.Fatalf("pool.Call ran %d times, want 1 (write fails but call dispatches)", len(pool.calls))
	}
}

// TestConfirm_RequestPayloadCarriesContext verifies the gateway
// receives a ConfirmRequest with the session, sub, server, tool, and
// tool_use ID populated — so the rpc layer can correlate to the
// frontend and back.
func TestConfirm_RequestPayloadCarriesContext(t *testing.T) {
	gw := &recordingGateway{decisions: []ConfirmDecision{ConfirmAllow}}
	loop, _, _, _ := newConfirmTestLoop(t, gw, nil, true)

	if err := loop.Run(context.Background(), "sess-X", "sub-Y", toolUseInitial(), corellm.GenerationRequest{}); err != nil {
		t.Fatalf("Run: %v", err)
	}
	got := gw.snapshot()
	if len(got) != 1 {
		t.Fatalf("requests = %d, want 1", len(got))
	}
	r := got[0]
	if r.SessionID != "sess-X" {
		t.Errorf("SessionID = %q, want sess-X", r.SessionID)
	}
	if r.ParentSubID != "sub-Y" {
		t.Errorf("ParentSubID = %q, want sub-Y", r.ParentSubID)
	}
	if r.Server != "svc" {
		t.Errorf("Server = %q, want svc", r.Server)
	}
	if r.Tool != "tool" {
		t.Errorf("Tool = %q, want tool", r.Tool)
	}
	if r.ToolUseID != "t-1" {
		t.Errorf("ToolUseID = %q, want t-1", r.ToolUseID)
	}
	if r.RequestID == "" {
		t.Errorf("RequestID is empty; want non-empty correlation key")
	}
}

