package rpc

// trust-surfaces-that-fire-01PMZ202 WP09 / UNIT-8, AC-06.
//
// Six existing tests (core/agentgraph/tool_invocation_test.go) already
// prove that once env.LifecycleHooks is a live adapter, a pre_tool_use
// hook returning decision:"block" stops the tool call. All six pass
// today with the field unwired in production — spec.md calls that
// "vacuous": they hand-construct an Env literal with LifecycleHooks
// set, bypassing the actual wiring function entirely.
//
// This test exercises the wiring function itself, newGraphManagerWithDeps
// (core/rpc/api.go), the ONE place a.hookRunner is threaded into the
// kernel's Env. It drives a REAL hooks.Runner backed by a REAL
// hooks.Registry with a REAL saved pre_tool_use hook, calls
// newGraphManagerWithDeps exactly as core/rpc/api.go's New() does, and
// asserts the resulting Manager's EnvDefaults() closure — the same
// closure chat_runner.go and manager.go both call on every real run —
// populates env.LifecycleHooks with something that actually honours the
// hook's block decision.
//
// Mutation: delete the `if hookRunner != nil { deps.LifecycleHooks = ... }`
// block in newGraphManagerWithDeps. This test fails with "LifecycleHooks
// not wired" — confirmed by hand during development (temporarily removed
// the block; TestNewGraphManagerWithDeps_LifecycleHooksReachEnv failed
// with that message; restored the block; the test passed again).

import (
	"context"
	"encoding/json"
	"testing"

	coreag "github.com/kameas-ai/kenaz-harness/core/agentgraph"
	"github.com/kameas-ai/kenaz-harness/core/hooks"
)

func TestNewGraphManagerWithDeps_LifecycleHooksReachEnv(t *testing.T) {
	t.Parallel()

	builtins := hooks.NewBuiltinRegistry()
	builtins.RegisterGenericFire("test-block-pre-tool-use",
		func(_ context.Context, _ string, _ any, _ map[string]any) (hooks.HookOutput, error) {
			return hooks.HookOutput{Decision: "block", Reason: "wp09-planted-block"}, nil
		},
		hooks.BuiltinDescriptor{ID: "test-block-pre-tool-use", Name: "test-block-pre-tool-use", Events: []string{hooks.EventPreToolUse}},
	)

	registry, err := hooks.NewRegistry("") // in-memory
	if err != nil {
		t.Fatalf("hooks.NewRegistry: %v", err)
	}
	if err := registry.Add(hooks.Hook{
		ID:      "h-wp09-block",
		Name:    "block everything",
		Event:   hooks.EventPreToolUse,
		Kind:    hooks.KindBuiltin,
		Enabled: true,
		Builtin: "test-block-pre-tool-use",
	}); err != nil {
		t.Fatalf("registry.Add: %v", err)
	}

	runner := hooks.NewRunner(hooks.Config{Registry: registry, Builtins: builtins})

	// Exercise the real production wiring function with a nil Core (test
	// chassis) — every other dependency defaults exactly as
	// newGraphManager()'s nil-Core path already does; only hookRunner is
	// new.
	mgr, _ := newGraphManagerWithDeps(nil, nil, nil, nil, nil, nil, nil, nil, runner)
	if mgr == nil {
		t.Fatal("newGraphManagerWithDeps returned a nil Manager")
	}

	envDefaults := mgr.EnvDefaults()
	if envDefaults == nil {
		t.Fatal("Manager.EnvDefaults() returned nil — cannot observe the wired Env")
	}

	env := &coreag.Env{SessionID: "s-wp09"}
	envDefaults(env)

	if env.LifecycleHooks == nil {
		t.Fatal("env.LifecycleHooks not wired — LifecycleHooks not wired")
	}

	merged, err := env.LifecycleHooks.FirePreToolUse(
		context.Background(), "s-wp09", "kenaz__bash", json.RawMessage(`{}`), "", "",
	)
	if err != nil {
		t.Fatalf("FirePreToolUse: %v", err)
	}
	if !merged.Blocked {
		t.Fatalf("merged.Blocked = false, want true (the saved pre_tool_use hook returns decision:block); merged=%+v", merged)
	}
	if merged.BlockReason != "wp09-planted-block" {
		t.Errorf("merged.BlockReason = %q, want %q", merged.BlockReason, "wp09-planted-block")
	}
}

// TestNewGraphManagerWithDeps_NilHookRunner_LifecycleHooksStaysNil pins
// the pre-WP09 default: a nil hookRunner (the nil-Core / test-chassis
// path at newGraphManager()) leaves env.LifecycleHooks unset, matching
// applyEnvDefaults' documented "no LifecycleHooks means no v2 tool hooks
// fire" fallback rather than panicking or installing a dead adapter.
func TestNewGraphManagerWithDeps_NilHookRunner_LifecycleHooksStaysNil(t *testing.T) {
	t.Parallel()

	mgr, _ := newGraphManagerWithDeps(nil, nil, nil, nil, nil, nil, nil, nil, nil)
	if mgr == nil {
		t.Fatal("newGraphManagerWithDeps returned a nil Manager")
	}
	envDefaults := mgr.EnvDefaults()
	if envDefaults == nil {
		t.Fatal("Manager.EnvDefaults() returned nil")
	}
	env := &coreag.Env{SessionID: "s-nil"}
	envDefaults(env)
	if env.LifecycleHooks != nil {
		t.Fatalf("env.LifecycleHooks = %#v, want nil when no hookRunner was supplied", env.LifecycleHooks)
	}
}
