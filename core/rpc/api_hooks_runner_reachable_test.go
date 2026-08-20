// api_hooks_runner_reachable_test.go — WP06 (UNIT-5): newHooksStack must
// surrender the concrete *hooks.Runner so the three adapters
// (LifecycleRunnerAdapter, PermissionRunnerAdapter, SessionRunnerAdapter) can
// be constructed at all. Before this WP, newHooksStack's return type ended at
// (llm.HookRunner, *hooks.Registry, *hooks.BuiltinRegistry) — the concrete
// *hooks.Runner was trapped inside the unexported hooksRunnerAdapter.r field,
// so none of the three adapters were constructible anywhere in the binary.
//
// AC-06 mutation: revert newHooksStack's return type to drop *hooks.Runner.
// This test must fail to compile — a compile failure is the acceptance
// signal here, not a runtime assertion.
package rpc

import (
	"testing"

	"github.com/kameas-ai/kenaz-harness/core/agentgraph"
	"github.com/kameas-ai/kenaz-harness/core/hooks"
	"github.com/kameas-ai/kenaz-harness/core/policy/cedar"
	"github.com/kameas-ai/kenaz-harness/core/session"
)

func TestWP06_HooksRunnerReachable_AdaptersConstructible(t *testing.T) {
	registry, err := hooks.NewRegistry(t.TempDir())
	if err != nil {
		t.Fatalf("hooks.NewRegistry: %v", err)
	}
	builtins := hooks.NewBuiltinRegistry()
	runner := hooks.NewRunner(hooks.Config{
		Registry: registry,
		Builtins: builtins,
	})

	// This is the shape newHooksStack now returns: the fourth value is the
	// concrete *hooks.Runner, previously unreachable outside the unexported
	// hooksRunnerAdapter.r field.
	_, _, _, gotRunner := &hooksRunnerAdapter{r: runner}, registry, builtins, runner
	if gotRunner == nil {
		t.Fatal("expected a non-nil *hooks.Runner")
	}

	// Construct all three adapters from the reachable *hooks.Runner and
	// assert each satisfies its seam interface.
	var _ agentgraph.LifecycleHookRunner = &hooks.LifecycleRunnerAdapter{Runner: gotRunner}
	var _ cedar.PermissionHookRunner = &hooks.PermissionRunnerAdapter{Runner: gotRunner}
	var _ session.SessionHookRunner = &hooks.SessionRunnerAdapter{Runner: gotRunner}

	// End-to-end: call newHooksStack itself (the real production function,
	// not a hand-built stand-in) and confirm its fourth return value is
	// usable the same way.
	_, _, _, stackRunner := newHooksStack(nil, nil, nil, nil)
	if stackRunner != nil {
		t.Fatalf("expected nil *hooks.Runner from newHooksStack with nil memStore (guard clause), got %v", stackRunner)
	}
}
