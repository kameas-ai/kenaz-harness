package chat

// autonomy-knobs-live-01PMAG02 WP04 — continueOnError wiring.
//
// The core behaviour (retry-once / adapt semantics, ErrPaused /
// ErrBudgetExceeded staying terminal) is pinned against loopExecutor
// directly in core/agentgraph/continue_on_error_test.go. This pins the
// translation from the resolved autonomy knob into core/agentgraph's
// autonomy-agnostic NodeErrorPolicy enum, which is the seam
// chat_runner.go's StartStream wires onto the Env.

import (
	"testing"

	coreag "github.com/kameas-ai/kenaz-harness/core/agentgraph"
	"github.com/kameas-ai/kenaz-harness/core/autonomy"
)

func TestContinueOnErrorPolicy(t *testing.T) {
	t.Parallel()
	cases := []struct {
		mode autonomy.ErrorMode
		want coreag.NodeErrorPolicy
	}{
		{autonomy.ErrorStop, coreag.NodeErrorPolicyStop},
		{autonomy.ErrorRetryOnce, coreag.NodeErrorPolicyRetryOnce},
		{autonomy.ErrorAdapt, coreag.NodeErrorPolicyAdapt},
		// FR-005: the zero value (what a nil AutonomyKnobs provider
		// resolves to) must map to Stop, today's behaviour.
		{autonomy.ErrorMode(""), coreag.NodeErrorPolicyStop},
		// An unrecognised mode degrades to Stop rather than a wrong
		// (potentially run-swallowing) policy.
		{autonomy.ErrorMode("bogus"), coreag.NodeErrorPolicyStop},
	}
	for _, tc := range cases {
		if got := continueOnErrorPolicy(tc.mode); got != tc.want {
			t.Errorf("continueOnErrorPolicy(%q) = %q, want %q", tc.mode, got, tc.want)
		}
	}
}
