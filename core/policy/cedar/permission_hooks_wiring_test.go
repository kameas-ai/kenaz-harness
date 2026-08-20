package cedar_test

// trust-surfaces-that-fire-01PMZ202 WP10 / UNIT-9, AC-07.
//
// These tests exercise the exact ordering hazard core/rpc/api.go resolves
// with a backfill (a.promptRegistry is built long before a.hookRunner
// exists): a PermissionRunnerAdapter is constructed with a nil Runner,
// passed BY POINTER into WithPermissionHookRunner, and only later does
// the Runner field get set. If cedar ever captured the interface value
// by copy instead of reading through the pointer on every call, this
// pattern would silently keep denying/no-opping forever. They also
// prove permission_denied — a fire site that did not exist anywhere in
// the tree before this WP — actually fires on every Deny-producing exit
// of RequestInteractive this WP wired: an explicit user Deny and a
// timeout. (Queue-overflow is wired at the same call site with the same
// shape but is not separately tested here — PromptQueueCap concurrency
// makes it the slowest/flakiest of the three to drive deterministically.)
//
// This file is `package cedar_test` (external), not `package cedar`,
// specifically so it can import the REAL core/hooks.PermissionRunnerAdapter
// — core/hooks imports core/policy/cedar to satisfy the seam, so an
// INTERNAL cedar test file importing core/hooks back is a compile-time
// import cycle (verified: `go vet` rejects it). The external test
// package has no such restriction: cedar_test -> cedar and
// cedar_test -> hooks -> cedar both point away from cedar, so there is
// no cycle. No fake implements cedar.PermissionHookRunner anywhere in
// this file — every assertion drives the actual production adapter type.

import (
	"context"
	"testing"
	"time"

	"github.com/kameas-ai/kenaz-harness/core/hooks"
	"github.com/kameas-ai/kenaz-harness/core/policy/cedar"
)

// recordingDispatcher is a minimal PromptDispatcher that signals on
// every Dispatch call, so a test can wait for the request to be
// enqueued before resolving it.
type recordingDispatcher struct {
	emitted chan struct{}
}

func newRecordingDispatcher() *recordingDispatcher {
	return &recordingDispatcher{emitted: make(chan struct{}, 16)}
}

func (d *recordingDispatcher) Dispatch(_ context.Context, _ string, _ cedar.PendingRequest) {
	select {
	case d.emitted <- struct{}{}:
	default:
	}
}

func newRealHookRunner(t *testing.T, event string, builtinID string, fn hooks.GenericFireBuiltin) *hooks.Runner {
	t.Helper()
	builtins := hooks.NewBuiltinRegistry()
	builtins.RegisterGenericFire(builtinID, fn, hooks.BuiltinDescriptor{ID: builtinID, Name: builtinID, Events: []string{event}})
	registry, err := hooks.NewRegistry("")
	if err != nil {
		t.Fatalf("hooks.NewRegistry: %v", err)
	}
	if err := registry.Add(hooks.Hook{
		ID: "h-" + builtinID, Name: builtinID, Event: event,
		Kind: hooks.KindBuiltin, Enabled: true, Builtin: builtinID,
	}); err != nil {
		t.Fatalf("registry.Add: %v", err)
	}
	return hooks.NewRunner(hooks.Config{Registry: registry, Builtins: builtins})
}

// TestRegistry_PermissionHookRunner_BackfillPattern_HonoursLateWiredDeny
// mirrors core/rpc/api.go's construction order exactly: the adapter is
// built with Runner == nil, handed to WithPermissionHookRunner by
// pointer, and ONLY THEN does its Runner field get set. A real
// permission_request hook returning permission_decision:"deny" must
// deny — proving cedar reads r.permHooks through the pointer on every
// call rather than snapshotting a nil-Runner adapter at construction.
func TestRegistry_PermissionHookRunner_BackfillPattern_HonoursLateWiredDeny(t *testing.T) {
	t.Parallel()

	runner := newRealHookRunner(t, hooks.EventPermissionRequest, "wp10-deny-request",
		func(_ context.Context, _ string, _ any, _ map[string]any) (hooks.HookOutput, error) {
			return hooks.HookOutput{PermissionDecision: "deny", PermissionDecisionReason: "wp10-planted-deny"}, nil
		},
	)

	// Step 1 (mirrors api.go ~line 1331): construct the REAL production
	// adapter type with a nil Runner and wire it into the registry
	// immediately.
	adapter := &hooks.PermissionRunnerAdapter{}
	reg := cedar.NewRegistry(cedar.WithPermissionHookRunner(adapter))

	// Step 2 (mirrors api.go ~line 1670, ~300 lines later): backfill the
	// Runner field in place, after hookRunner is constructed.
	adapter.Runner = runner

	res, err := reg.RequestInteractive(context.Background(), cedar.PromptSurface{
		SessionID: "s-wp10",
		Bash:      &cedar.BashPromptSurface{Pattern: "rm -rf /"},
	})
	if err != nil {
		t.Fatalf("RequestInteractive: %v", err)
	}
	if res.Decision != cedar.DecisionDeny {
		t.Fatalf("Decision = %q, want %q; res=%+v", res.Decision, cedar.DecisionDeny, res)
	}
	if res.Reason != "hook_deny: wp10-planted-deny" {
		t.Errorf("Reason = %q, want %q", res.Reason, "hook_deny: wp10-planted-deny")
	}
}

// TestRegistry_PermissionHookRunner_NilRunnerIsSafeNoOp confirms the
// production adapter's own nil-guard makes an interactive prompt safe
// BEFORE the backfill happens (the narrow window between
// a.promptRegistry's construction and a.hookRunner being set) — it must
// fall through to the normal (non-hook) prompt flow, not panic or hang.
func TestRegistry_PermissionHookRunner_NilRunnerIsSafeNoOp(t *testing.T) {
	t.Parallel()

	adapter := &hooks.PermissionRunnerAdapter{} // Runner never backfilled
	disp := newRecordingDispatcher()
	reg := cedar.NewRegistry(cedar.WithDispatcher(disp), cedar.WithPermissionHookRunner(adapter), cedar.WithTimeout(50*time.Millisecond))

	res, err := reg.RequestInteractive(context.Background(), cedar.PromptSurface{
		SessionID: "s-nil", Bash: &cedar.BashPromptSurface{Pattern: "ls"},
	})
	if err != nil {
		t.Fatalf("RequestInteractive: %v", err)
	}
	// No hook opinion (nil Runner) -> falls through to the normal prompt
	// flow -> times out (no resolver in this test) -> Deny/"timeout".
	if res.Decision != cedar.DecisionDeny || res.Reason != "timeout" {
		t.Errorf("res = %+v, want Deny/timeout (nil-Runner hook must no-op, not short-circuit)", res)
	}
}

// TestRegistry_FirePermissionDenied_OnExplicitUserDeny proves the NEW
// fire site this WP added (Registry.fire()) actually dispatches
// permission_denied when a user resolves a pending request as Deny —
// this call site did not exist anywhere in the tree before this WP;
// FirePermissionDenied had zero non-test callers.
func TestRegistry_FirePermissionDenied_OnExplicitUserDeny(t *testing.T) {
	t.Parallel()

	deniedCh := make(chan hooks.PermissionDeniedEvent, 1)
	runner := newRealHookRunner(t, hooks.EventPermissionDenied, "wp10-record-denied",
		func(_ context.Context, _ string, payload any, _ map[string]any) (hooks.HookOutput, error) {
			if ev, ok := payload.(hooks.PermissionDeniedEvent); ok {
				deniedCh <- ev
			}
			return hooks.HookOutput{}, nil
		},
	)
	adapter := &hooks.PermissionRunnerAdapter{Runner: runner}
	disp := newRecordingDispatcher()
	reg := cedar.NewRegistry(cedar.WithDispatcher(disp), cedar.WithPermissionHookRunner(adapter), cedar.WithTimeout(2*time.Second))

	resCh := make(chan cedar.Resolution, 1)
	go func() {
		res, _ := reg.RequestInteractive(context.Background(), cedar.PromptSurface{
			SessionID: "s-explicit-deny", Bash: &cedar.BashPromptSurface{Pattern: "rm -rf /"},
		})
		resCh <- res
	}()

	// Wait for the request to be dispatched, then resolve it as Deny.
	select {
	case <-disp.emitted:
	case <-time.After(2 * time.Second):
		t.Fatal("request never dispatched")
	}
	pending := reg.ListPending()
	if len(pending) != 1 {
		t.Fatalf("ListPending() = %d entries, want 1", len(pending))
	}
	if err := reg.Resolve(pending[0].RequestID, cedar.DecisionDeny); err != nil {
		t.Fatalf("Resolve: %v", err)
	}

	select {
	case res := <-resCh:
		if res.Decision != cedar.DecisionDeny {
			t.Errorf("Decision = %q, want %q", res.Decision, cedar.DecisionDeny)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("RequestInteractive never returned")
	}

	select {
	case ev := <-deniedCh:
		if ev.SessionID != "s-explicit-deny" {
			t.Errorf("permission_denied fired with session_id = %q, want %q", ev.SessionID, "s-explicit-deny")
		}
	case <-time.After(2 * time.Second):
		t.Fatal("permission_denied never fired on an explicit user Deny — the fire() call site this WP added did not run")
	}
}

// TestRegistry_FirePermissionDenied_OnTimeout proves the timeout exit
// path — the OTHER caller that funnels through Registry.fire() —
// also fires permission_denied, without a test needing to wait out a
// real 5-minute production timeout (WithTimeout shortens it).
func TestRegistry_FirePermissionDenied_OnTimeout(t *testing.T) {
	t.Parallel()

	deniedCh := make(chan hooks.PermissionDeniedEvent, 1)
	runner := newRealHookRunner(t, hooks.EventPermissionDenied, "wp10-record-denied-timeout",
		func(_ context.Context, _ string, payload any, _ map[string]any) (hooks.HookOutput, error) {
			if ev, ok := payload.(hooks.PermissionDeniedEvent); ok {
				deniedCh <- ev
			}
			return hooks.HookOutput{}, nil
		},
	)
	adapter := &hooks.PermissionRunnerAdapter{Runner: runner}
	reg := cedar.NewRegistry(cedar.WithPermissionHookRunner(adapter), cedar.WithTimeout(50*time.Millisecond))

	res, err := reg.RequestInteractive(context.Background(), cedar.PromptSurface{
		SessionID: "s-timeout", Bash: &cedar.BashPromptSurface{Pattern: "ls"},
	})
	if err != nil {
		t.Fatalf("RequestInteractive: %v", err)
	}
	if res.Decision != cedar.DecisionDeny || res.Reason != "timeout" {
		t.Fatalf("res = %+v, want Deny/timeout", res)
	}

	select {
	case ev := <-deniedCh:
		if ev.SessionID != "s-timeout" {
			t.Errorf("permission_denied fired with session_id = %q, want %q", ev.SessionID, "s-timeout")
		}
		if ev.Reason != "timeout" {
			t.Errorf("permission_denied Reason = %q, want %q", ev.Reason, "timeout")
		}
	case <-time.After(2 * time.Second):
		t.Fatal("permission_denied never fired on timeout")
	}
}
