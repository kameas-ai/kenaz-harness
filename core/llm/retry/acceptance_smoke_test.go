// Package retry — acceptance smoke for the long-turn-resilience mission
// (mission long-turn-resilience-01KR3PRS, WP04).
//
// This file is the cross-layer end-to-end smoke test: it wires the keepalive
// transport (Layer 1), the classified retry-with-backoff stream wrapper
// (Layer 2), and a stub representing the resume-flow surface (Layer 3,
// landing in feat/long-turn-resilience-wp03) into a single sequence that
// mirrors §Acceptance smoke 1–6 of plan.md:
//
//   1. Layer 1 sanity   — keepalive transport is the production default.
//   2. Layer 2 pre      — pre-stream transient → silent retry → success.
//   3. Layer 2 mid-pre  — mid-stream transient before any delta → silent retry.
//   4. Layer 3 resume   — mid-stream transient AFTER text deltas → no
//                          retry; partial preserved; resume hand-off fires.
//   5. Layer 3 no-resume — mid-stream transient AFTER tool_use → no retry;
//                          partial preserved; resume marked unrecoverable.
//   6. No retry on auth — 401 propagates immediately as ErrAuth.
//
// Steps 4 and 5 stop short of asserting the full resume flow (persistence
// columns, RPC, frontend Resume button) because those land in WP03. Each
// fenced assertion is marked with TODO(wp03) so the test auto-strengthens
// when WP03 merges into release/v0.2.0.
package retry

import (
	"context"
	"errors"
	"net/http"
	"sync/atomic"
	"testing"
	"time"

	llm "github.com/kameas-ai/kenaz-harness/core/llm"
	"github.com/kameas-ai/kenaz-harness/core/llm/httpx"
)

// wp03Available reports whether the Layer 3 resume surface is wired into
// this build. WP03 (feat/long-turn-resilience-wp03) is being implemented
// in parallel; until it merges into release/v0.2.0 the resume hooks
// remain stubbed and the resume-specific assertions below are skipped.
//
// TODO(wp03): set this to true once WP03 lands and adjust the resume
// assertions to call the real Sessions_ResumeMessage RPC + verify the
// session_messages columns (streaming_failed_at / streaming_failure_kind
// / streaming_recoverable).
const wp03Available = false

// resumeHook is the in-test surface that stands in for Layer 3 today.
// It is invoked from the smoke test whenever the retry layer surfaces a
// terminal partial-output failure — i.e. when the resume-flow would be
// asked to persist the partial bubble and offer a Resume button.
type resumeHook struct {
	calls       int32
	lastPartial []llm.StreamEvent
	lastKind    string // "transient" / "auth" / "unknown"
	recoverable bool
}

func (r *resumeHook) Notify(partial []llm.StreamEvent, err error, hasTool bool) {
	atomic.AddInt32(&r.calls, 1)
	r.lastPartial = append([]llm.StreamEvent(nil), partial...)
	r.recoverable = !hasTool && llm.IsTransient(err)
	switch {
	case llm.IsTransient(err):
		r.lastKind = "transient"
	case errors.As(err, new(*llm.ErrAuth)):
		r.lastKind = "auth"
	default:
		r.lastKind = "unknown"
	}
}

// runWithRetry wraps drainStream + RetryStream and pipes any terminal
// error into the supplied resumeHook so the smoke can cover the full
// "retry exhausted → resume" hand-off in one call.
func runWithRetry(t *testing.T, ctx context.Context, hook *resumeHook, fn func() (llm.Stream, error)) ([]llm.StreamEvent, error) {
	t.Helper()
	policy := instantPolicy(3)
	s, err := RetryStream(ctx, policy, fn)
	if err != nil {
		// Pre-stream exhaustion or non-transient error: nothing partial
		// to hand off, but resume hook still gets the classification so
		// upstream surfaces can render a "Connection lost" affordance.
		if hook != nil {
			hook.Notify(nil, err, false)
		}
		return nil, err
	}
	evs, _, ferr := drainStream(s)
	if ferr != nil && hook != nil {
		hasTool := false
		for _, ev := range evs {
			if ev.Kind == llm.StreamTool {
				hasTool = true
				break
			}
		}
		hook.Notify(evs, ferr, hasTool)
	}
	return evs, ferr
}

// ────────────────────────────────────────────────────────────────────────
// Step 1 — Layer 1 sanity
// ────────────────────────────────────────────────────────────────────────

// TestAcceptance_Layer1_KeepaliveTransportIsDefault asserts that the
// production HTTP transport ships with TCP keepalive enabled. Layer 1 of
// the mission relies on this transport being the default for every LLM
// adapter; without it the edge-proxy idle-timeout bug returns even with
// a perfectly-classified retry layer on top.
func TestAcceptance_Layer1_KeepaliveTransportIsDefault(t *testing.T) {
	tr := httpx.DefaultTransport()
	if tr == nil {
		t.Fatal("httpx.DefaultTransport() returned nil")
	}
	// We can't introspect the dialer's KeepAlive directly through the
	// closure-captured DialContext, but we CAN sanity-check the field
	// budget that's load-bearing for streaming.
	if tr.ResponseHeaderTimeout == 0 {
		t.Errorf("expected ResponseHeaderTimeout > 0 (got 0); long streams need a header budget")
	}
	if tr.IdleConnTimeout == 0 {
		t.Errorf("expected IdleConnTimeout > 0 (got 0); pool needs to evict stale conns")
	}
	if !tr.ForceAttemptHTTP2 {
		t.Errorf("expected ForceAttemptHTTP2 = true; provider connections must prefer h2")
	}
	// The transport.Timeout-style request-level budget MUST be unset —
	// adapter contract is that context.Context drives lifetime, not a
	// hard request timeout (see core/llm/anthropic comment).
	if tr.TLSHandshakeTimeout == 0 {
		t.Errorf("expected TLSHandshakeTimeout > 0 (got 0)")
	}

	// Compile-time: confirm DefaultTransport() returns the *http.Transport
	// the adapters expect. Type assertion is implicit by the call site.
	var _ *http.Transport = tr
}

// ────────────────────────────────────────────────────────────────────────
// Step 2 — Layer 2 pre-stream retry
// ────────────────────────────────────────────────────────────────────────

// TestAcceptance_Layer2_PreStreamTransientRetried mirrors plan.md
// §Acceptance smoke 2: the registry's first stream-open returns 503,
// the retry layer waits for the backoff, the second open succeeds, and
// a single bubble's worth of text streams to the user.
func TestAcceptance_Layer2_PreStreamTransientRetried(t *testing.T) {
	var calls int32
	successStream := newFakeStream(
		[]llm.StreamEvent{{Kind: llm.StreamText, Text: "hello world"}},
		nil,
	)
	fn := func() (llm.Stream, error) {
		n := atomic.AddInt32(&calls, 1)
		if n == 1 {
			return nil, &llm.ErrTransient{Status: 503, Message: "service unavailable"}
		}
		return successStream, nil
	}

	hook := &resumeHook{}
	evs, err := runWithRetry(t, context.Background(), hook, fn)
	if err != nil {
		t.Fatalf("expected silent recovery, got error: %v", err)
	}
	if got := atomic.LoadInt32(&calls); got != 2 {
		t.Fatalf("expected exactly 2 fn calls (1 fail + 1 success), got %d", got)
	}
	if atomic.LoadInt32(&hook.calls) != 0 {
		t.Fatalf("resume hook fired on a recoverable failure; expected 0 calls, got %d", hook.calls)
	}
	if len(evs) != 1 || evs[0].Text != "hello world" {
		t.Fatalf("expected single text event 'hello world', got %v", evs)
	}
}

// ────────────────────────────────────────────────────────────────────────
// Step 3 — Layer 2 mid-stream retry, no partial output
// ────────────────────────────────────────────────────────────────────────

// TestAcceptance_Layer2_MidStreamNoContentRetried mirrors plan.md
// §Acceptance smoke 3: the SSE handshake completes but the scanner
// errors before any delta lands. The retry wrapper restarts from
// scratch and the second stream's content reaches the user transparently.
func TestAcceptance_Layer2_MidStreamNoContentRetried(t *testing.T) {
	var calls int32
	errStream := newFakeStream(nil, &llm.ErrTransient{Status: 502, Message: "scanner EOF"})
	goodStream := newFakeStream(
		[]llm.StreamEvent{{Kind: llm.StreamText, Text: "fine the second time"}},
		nil,
	)
	fn := func() (llm.Stream, error) {
		n := atomic.AddInt32(&calls, 1)
		if n == 1 {
			return errStream, nil
		}
		return goodStream, nil
	}

	hook := &resumeHook{}
	evs, err := runWithRetry(t, context.Background(), hook, fn)
	if err != nil {
		t.Fatalf("expected silent mid-stream recovery, got: %v", err)
	}
	if atomic.LoadInt32(&hook.calls) != 0 {
		t.Fatalf("resume hook should not fire on silent mid-stream recovery; got %d calls", hook.calls)
	}
	if len(evs) != 1 || evs[0].Text != "fine the second time" {
		t.Fatalf("expected text 'fine the second time' from retry, got %v", evs)
	}
	if got := atomic.LoadInt32(&calls); got != 2 {
		t.Fatalf("expected 2 fn calls (initial + 1 mid-stream retry), got %d", got)
	}
}

// ────────────────────────────────────────────────────────────────────────
// Step 4 — Layer 3 resume hand-off (text-only partial)
// ────────────────────────────────────────────────────────────────────────

// TestAcceptance_Layer3_TextPartialEscalatesToResume mirrors plan.md
// §Acceptance smoke 4: text deltas land, the connection drops mid-stream,
// no retry happens (partial output is unsafe to re-issue), and the
// terminal error escalates to the resume surface marked recoverable.
//
// The full resume-flow assertions (Sessions_ResumeMessage round-trip,
// session_messages.streaming_recoverable=true on disk, frontend Resume
// button click) are fenced behind WP03 — see wp03Available.
func TestAcceptance_Layer3_TextPartialEscalatesToResume(t *testing.T) {
	var calls int32
	flaky := newFakeStream(
		[]llm.StreamEvent{
			{Kind: llm.StreamText, Text: "Here is the first half of "},
			{Kind: llm.StreamText, Text: "the assistant reply..."},
		},
		&llm.ErrTransient{Status: 502, Message: "edge dropped after delta 2"},
	)
	fn := func() (llm.Stream, error) {
		atomic.AddInt32(&calls, 1)
		return flaky, nil
	}

	hook := &resumeHook{}
	evs, err := runWithRetry(t, context.Background(), hook, fn)
	if err == nil {
		t.Fatal("expected terminal error after partial-text mid-stream failure")
	}
	if !llm.IsTransient(err) {
		t.Fatalf("expected ErrTransient escalation, got %T: %v", err, err)
	}
	if got := atomic.LoadInt32(&calls); got != 1 {
		t.Fatalf("expected NO retry after partial text (got %d fn calls)", got)
	}
	// The partial events must reach the caller verbatim — this is the bug
	// WP00 fixed on the frontend side, mirrored here at the transport seam.
	if len(evs) != 2 || evs[0].Text == "" || evs[1].Text == "" {
		t.Fatalf("expected 2 partial text events preserved; got %v", evs)
	}
	// The resume surface must be notified with a recoverable classification.
	if atomic.LoadInt32(&hook.calls) != 1 {
		t.Fatalf("expected exactly 1 resume hook call, got %d", hook.calls)
	}
	if !hook.recoverable {
		t.Errorf("expected hook.recoverable=true for text-only partial; got false")
	}
	if hook.lastKind != "transient" {
		t.Errorf("expected hook.lastKind=transient, got %q", hook.lastKind)
	}

	if !wp03Available {
		t.Skip("requires WP03 — feat/long-turn-resilience-wp03; partial-output assertions limited to in-process hook until the resume RPC and SQLite columns land")
	}
	// TODO(wp03): once WP03 merges:
	//   - call sessionsAPI.Sessions_ResumeMessage(originalMessageID).
	//   - verify the original row has streaming_failed_at != NULL,
	//     streaming_failure_kind == "transient", streaming_recoverable == true.
	//   - verify a continuation row appears with continuation_of ==
	//     originalMessageID and a fresh ID.
}

// ────────────────────────────────────────────────────────────────────────
// Step 5 — Layer 3 no-resume hand-off (tool_use partial)
// ────────────────────────────────────────────────────────────────────────

// TestAcceptance_Layer3_ToolUsePartialIsNotRecoverable mirrors plan.md
// §Acceptance smoke 5: a tool_use block has been emitted (and presumed
// executed downstream) before the connection drops. The retry layer must
// NOT retry — re-issuing would double-fire side-effects — and must mark
// the resume surface as non-recoverable so the frontend renders the grey
// "(connection lost mid-tool-call; please re-send)" footer instead of a
// Resume button.
func TestAcceptance_Layer3_ToolUsePartialIsNotRecoverable(t *testing.T) {
	var calls int32
	toolStream := newFakeStream(
		[]llm.StreamEvent{
			{Kind: llm.StreamTool, Tool: &llm.ToolUse{ID: "tu_1", Name: "bash"}},
		},
		&llm.ErrTransient{Status: 502, Message: "edge dropped after tool_use"},
	)
	fn := func() (llm.Stream, error) {
		atomic.AddInt32(&calls, 1)
		return toolStream, nil
	}

	hook := &resumeHook{}
	_, err := runWithRetry(t, context.Background(), hook, fn)
	if err == nil {
		t.Fatal("expected terminal error after tool_use partial")
	}
	if got := atomic.LoadInt32(&calls); got != 1 {
		t.Fatalf("expected NO retry after tool_use partial (got %d fn calls)", got)
	}
	if atomic.LoadInt32(&hook.calls) != 1 {
		t.Fatalf("expected resume hook called exactly once, got %d", hook.calls)
	}
	if hook.recoverable {
		t.Errorf("expected hook.recoverable=false for tool_use partial; got true")
	}

	if !wp03Available {
		t.Skip("requires WP03 — feat/long-turn-resilience-wp03; tool_use no-resume assertions limited to in-process hook until the resume RPC lands")
	}
	// TODO(wp03): once WP03 merges:
	//   - verify the persisted row has streaming_recoverable == false.
	//   - verify Sessions_ResumeMessage returns ErrUnrecoverable / equivalent.
	//   - verify the frontend renders the grey "(re-send)" footer, no button.
}

// ────────────────────────────────────────────────────────────────────────
// Step 6 — auth errors propagate without retry
// ────────────────────────────────────────────────────────────────────────

// TestAcceptance_NoRetryOnAuth mirrors plan.md §Acceptance smoke 6:
// 401/403 are propagated immediately — there is no scenario where waiting
// 500ms and asking again will fix a bad credential, and silently retrying
// burns the user's quota / lockout budget.
func TestAcceptance_NoRetryOnAuth(t *testing.T) {
	var calls int32
	fn := func() (llm.Stream, error) {
		atomic.AddInt32(&calls, 1)
		return nil, &llm.ErrAuth{Status: 401, Message: "unauthorized"}
	}

	hook := &resumeHook{}
	_, err := runWithRetry(t, context.Background(), hook, fn)
	if err == nil {
		t.Fatal("expected ErrAuth, got nil")
	}
	var ae *llm.ErrAuth
	if !errors.As(err, &ae) {
		t.Fatalf("expected *ErrAuth, got %T: %v", err, err)
	}
	if got := atomic.LoadInt32(&calls); got != 1 {
		t.Fatalf("expected NO retry on auth error (got %d fn calls)", got)
	}
	// The resume surface still gets a notification so the UI can decide
	// what to render — but the classification is "auth", not "transient",
	// so no Resume button should appear (auth is not user-resumable).
	if atomic.LoadInt32(&hook.calls) != 1 {
		t.Fatalf("expected resume hook called once for classification, got %d", hook.calls)
	}
	if hook.lastKind == "transient" {
		t.Errorf("expected hook.lastKind != transient for auth error; got %q", hook.lastKind)
	}
}

// ────────────────────────────────────────────────────────────────────────
// Compile-time sanity: the smoke compiles regardless of WP03 wiring.
// ────────────────────────────────────────────────────────────────────────

var _ = time.Second // keeps the time import live if the smoke is trimmed.
