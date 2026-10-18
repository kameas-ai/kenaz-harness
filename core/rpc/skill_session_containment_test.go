package rpc

// skill_session_containment_test.go — the security review's H1 probe,
// kept as a permanent pin (model-harness-toolset-01MHTS001 WP02 review).
//
// kenaz__skill used to take session_id from the MODEL's arguments, and
// slashcmd.Dispatch.Run re-stamped the tool-dispatch ctx with it,
// overriding the real session: from a run contained to [kenaz__skill] a
// forged or foreign id reached kenaz__sleep (and a forged onboarding id
// would have passed the session-kind arm for harness_write_*). Driven
// through the production rpc.New wiring: the real slash dispatch, the
// real slash tool dispatcher and the real merged resolver.

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	slashview "github.com/kameas-ai/kenaz-harness/core/rpc/views/slashcmd"
	"github.com/kameas-ai/kenaz-harness/core/toolloop"
	coreskilltool "github.com/kameas-ai/kenaz-harness/core/tools/skill"
)

func TestSkillTool_SessionIsCtxDerived_ContainmentHolds(t *testing.T) {
	_, api := bootAPIWithCore(t, t.TempDir(), "")
	ctx := context.Background()
	contained, err := api.Sessions().Create(ctx, "Scheduled: probe")
	if err != nil {
		t.Fatal(err)
	}
	foreign, err := api.Sessions().Create(ctx, "interactive")
	if err != nil {
		t.Fatal(err)
	}
	if err := api.Slash().UserSave(ctx, slashview.UserCommandWire{
		Name: "probe-off-list", Scope: "global", Kind: "tool",
		Description: "probe", ModelInvokable: true,
		Tool: "kenaz__sleep", ToolArgsTemplate: "x",
	}); err != nil {
		t.Fatal(err)
	}
	api.scheduledRunContainment.Contain(contained.ID, "cr-probe", []string{"kenaz__skill"})

	tool := coreskilltool.New(coreskilltool.Options{Dispatch: api.slashDispatch})
	// ctx exactly as kernelToolAdapter.dispatch hands a builtin.
	callCtx := toolloop.WithSessionID(ctx, contained.ID)

	for name, sid := range map[string]string{"forged id": "bogus-not-a-session", "real foreign id": foreign.ID} {
		out, _ := tool.Call(callCtx, json.RawMessage(`{"name":"probe-off-list","session_id":"`+sid+`"}`))
		if !strings.Contains(string(out), `"kind":"invalid_args"`) {
			t.Errorf("%s: session_id argument not refused (escape path open): %s", name, out)
		}
	}

	// Honest call: the session comes from ctx, so the off-list kenaz__sleep
	// the skill dispatches is denied by the run's containment.
	honest, _ := tool.Call(callCtx, json.RawMessage(`{"name":"probe-off-list"}`))
	if !strings.Contains(string(honest), "scheduled run") {
		t.Errorf("honest ctx-derived call not contained: %s", honest)
	}

	// And with kenaz__sleep on the list, the same honest call is allowed.
	allowed, err := api.Sessions().Create(ctx, "Scheduled: allowed")
	if err != nil {
		t.Fatal(err)
	}
	api.scheduledRunContainment.Contain(allowed.ID, "cr-allowed", []string{"kenaz__skill", "kenaz__sleep"})
	ok, _ := tool.Call(toolloop.WithSessionID(ctx, allowed.ID), json.RawMessage(`{"name":"probe-off-list"}`))
	// It reaches kenaz__sleep itself (which rejects the probe's "x" args —
	// that is the tool answering, not a permission refusal).
	if strings.Contains(string(ok), "denied") || !strings.Contains(string(ok), "sleep args") {
		t.Errorf("on-list honest call did not reach kenaz__sleep: %s", ok)
	}
}
