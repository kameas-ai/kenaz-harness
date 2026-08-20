package rpc

import (
	"context"
	"strings"
	"testing"

	cedarlib "github.com/cedar-policy/cedar-go"
	"github.com/kameas-ai/kenaz-harness/core/policy/cedar"
)

// harness-self-attach-01PMHS01 UNIT-2 — EmbeddedCedar reaches the live
// engine.
//
// THE DEFECT THIS PINS. harnessmcp.EmbeddedCedar (three shipped Cedar
// snippets: harness_read_default.cedar, harness_write_onboarding.cedar,
// harness_write_forbid.cedar) and cedar.Engine.LoadHarnessSnippets both
// existed and were unit-tested BEFORE this unit — but nothing at boot
// ever called LoadHarnessSnippets with the embedded snippets. Every
// ActionUseTool evaluation for a harness-self tool therefore fell
// through to NotApplicable, which this engine's default-allow posture
// (DefaultDeny: false) maps straight to Allow. A test that only asserts
// "onboarding writes are allowed" cannot distinguish "installed and
// correct" from "nothing installed, default-allow is doing the work" —
// see tasks.md UNIT-2's own warning about exactly this shape of false
// positive. Both directions are asserted below for that reason.
//
// This drives the REAL boot wiring (cedarWiringAPI -> core.New ->
// rpc.New), not a hand-built Engine, so it exercises the api.go call
// site UNIT-2 adds, not just LoadHarnessSnippets in isolation (that is
// core/policy/cedar/engine_test.go's job, and was already covered
// before this unit).
func TestHarnessCedarSnippets_InstalledAtBoot(t *testing.T) {
	api := cedarWiringAPI(t, "") // no user policy file; the embedded harness-self snippets must still install

	if api.cedarEngine == nil {
		t.Fatal("api.cedarEngine is nil — cedarWiringAPI no longer builds a real DataDir-backed engine; this test needs a live engine to mean anything")
	}

	toolUID := cedar.PermissionToolUID("harness-self__harness_write_set_setting")
	evalWithKind := func(kind string) cedar.Decision {
		return api.cedarEngine.Evaluate(
			context.Background(),
			cedar.UserUID(),
			cedar.ActionUseTool,
			toolUID,
			map[cedarlib.String]cedarlib.Value{
				cedarlib.String(cedar.CtxKeySessionKind): cedarlib.String(kind),
			},
		)
	}

	// Mutation: skip the LoadHarnessSnippets call in New(). Both
	// assertions below must fail — a version that only checked the
	// "onboarding" side would pass vacuously against an engine with
	// nothing installed (NotApplicable -> Allow by default).
	if d := evalWithKind("chat"); d.Outcome != cedar.Deny {
		t.Fatalf("chat session harness_write_set_setting: want Deny, got %s (reason=%s, matched=%s) — harness_write_forbid.cedar did not reach the engine",
			d.Outcome, d.Reason, d.MatchedPolicy)
	}
	if d := evalWithKind("onboarding"); d.Outcome != cedar.Allow {
		t.Fatalf("onboarding session harness_write_set_setting: want Allow, got %s (reason=%s) — harness_write_onboarding.cedar did not reach the engine",
			d.Outcome, d.Reason)
	}

	// Mutation: install only harness_write_onboarding.cedar (drop the
	// other two). The chat-Deny assertion above already covers this —
	// re-stated here via ListPolicies so a reviewer can see the
	// "install only one file" mutation tasks.md UNIT-2 calls out fails
	// on the count, not just on behaviour.
	files := api.cedarEngine.ListPolicies()
	installed := 0
	for _, f := range files {
		if strings.HasPrefix(f.Name, "harness_") && strings.HasSuffix(f.Name, ".cedar") {
			if !f.Embedded {
				t.Errorf("ListPolicies: %s Embedded=false, want true", f.Name)
			}
			if !f.ParseOK {
				t.Errorf("ListPolicies: %s ParseOK=false (err=%s)", f.Name, f.ParseErr)
			}
			installed++
		}
	}
	if installed != 3 {
		t.Fatalf("expected exactly 3 embedded harness-self policy files listed after boot, got %d (%+v)", installed, files)
	}
}

// TestHarnessCedarSnippets_ReadTool_AlwaysAllowed pins the read-side
// default: harness_read_* must be reachable from a plain chat session,
// not just onboarding. Exercises the same boot path so a regression that
// narrows harness_read_default.cedar's match (or drops it) is caught
// independently of the write-side assertions above.
func TestHarnessCedarSnippets_ReadTool_AlwaysAllowed(t *testing.T) {
	api := cedarWiringAPI(t, "")
	if api.cedarEngine == nil {
		t.Fatal("api.cedarEngine is nil")
	}

	d := api.cedarEngine.Evaluate(
		context.Background(),
		cedar.UserUID(),
		cedar.ActionUseTool,
		cedar.PermissionToolUID("harness-self__harness_read_get_status"),
		map[cedarlib.String]cedarlib.Value{
			cedarlib.String(cedar.CtxKeySessionKind): cedarlib.String("chat"),
		},
	)
	if d.Outcome == cedar.Deny {
		t.Fatalf("chat session harness_read_get_status: want not-Deny, got Deny (reason=%s, matched=%s)", d.Reason, d.MatchedPolicy)
	}
}
