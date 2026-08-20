package chat

// harness-self-attach-01PMHS01 UNIT-4, AC-016 — a regression pin, not a
// new behaviour. kernelToolAdapter.dispatch's `default:` arm already
// refuses an unrecognised or empty permission verdict ("a configuration
// error on the permission surface must not read as 'allow'" — see the
// arm's own comment) and toolloop.ToolPolicy is a string type whose zero
// value is "", not auto_allow. UNIT-4 makes the permission surface
// security-load-bearing for every session for the first time (the
// merged Cedar resolver goes live), so this pins the existing fail-safe
// with the exact three shapes B-3 worries about: an empty Resolution, an
// unrecognised policy string, and a resolver error. All three must
// refuse dispatch — never reach the pool.

import (
	"context"
	"errors"
	"testing"

	coreag "github.com/kameas-ai/kenaz-harness/core/agentgraph"
)

// fixedErrPermResolver always fails Resolve. Distinct from
// syncPermResolver (kernel_tool_adapter_confirm_test.go), which always
// succeeds with a fixed verdict.
type fixedErrPermResolver struct {
	err error
}

func (r fixedErrPermResolver) Resolve(_ context.Context, _, server, tool string) (PermVerdict, error) {
	return PermVerdict{Server: server, Tool: tool}, r.err
}

// TestKernelToolAdapter_UnrecognisedVerdictDeniesAtDispatch is AC-016.
//
// Mutation: delete the `default:` arm in dispatch's switch (or delete
// just the two `return ...IsError: true` statements it contains,
// letting the switch fall out of the block and continue to
// json.Marshal/pool.Call). Cases (a) and (b) must fail — verified by
// hand below.
func TestKernelToolAdapter_UnrecognisedVerdictDeniesAtDispatch(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name  string
		perms ToolPermissionResolver
	}{
		{
			name:  "empty Resolution (zero-value verdict)",
			perms: &syncPermResolver{verdict: PermVerdict{}}, // Policy == ""
		},
		{
			name:  "unknown policy string",
			perms: &syncPermResolver{verdict: PermVerdict{Policy: "widen_everything"}},
		},
		{
			name:  "resolver error",
			perms: fixedErrPermResolver{err: errors.New("boom: permission store unavailable")},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			pool := &countingToolPool{server: "harness-self", tool: "harness_write_add_provider"}
			adapter := newKernelToolAdapter(pool, tc.perms, "sess-ac016")

			res, err := adapter.Call(context.Background(), coreag.ToolCall{
				Name: "harness-self__harness_write_add_provider",
				Args: map[string]any{},
			})

			if dispatched := pool.dispatched(); len(dispatched) != 0 {
				t.Fatalf("%s: pool.Call was reached: %v — the permission surface's unhandled case must refuse, not dispatch", tc.name, dispatched)
			}

			// The resolver-error case surfaces as a Go error (dispatch
			// returns before building a ToolResult); the other two
			// surface as an IsError ToolResult. Either way nothing
			// dispatched, which is the assertion that matters — but
			// pin the shape too so a future change that silently
			// swallows the resolver error is visible here.
			if tc.name == "resolver error" {
				if err == nil {
					t.Fatalf("%s: want a non-nil error, got nil (res=%+v)", tc.name, res)
				}
				return
			}
			if err != nil {
				t.Fatalf("%s: unexpected error: %v", tc.name, err)
			}
			if !res.IsError {
				t.Fatalf("%s: want IsError=true, got a successful result: %+v", tc.name, res)
			}
		})
	}
}
