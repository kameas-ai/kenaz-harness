package rpc

import (
	"context"
	"strings"
	"testing"

	cedarlib "github.com/cedar-policy/cedar-go"
	"github.com/kameas-ai/kenaz-harness/core/policy/cedar"
)

// model-authored-graphs-01PMGA01 UNIT-3 — graphview.EmbeddedCedar reaches
// the live engine.
//
// Mirrors api_harness_cedar_install_test.go's shape exactly, for the
// same reason: graphview.CedarSnippets() (the four graph-authoring
// policies) and cedar.Engine.LoadHarnessSnippets both exist and are
// unit-tested at the engine level (cedar_policy_test.go in
// core/rpc/views/agentgraph) before this test — the defect this test
// would catch is nothing at boot calling LoadHarnessSnippets with THESE
// snippets, which degrades every graph.author/graph.run evaluation to
// NotApplicable -> Allow under the engine's default-allow posture. Both
// directions (Deny when disabled, Allow when enabled) are asserted so a
// version that only checks one side cannot pass vacuously against an
// engine with nothing installed.
//
// This drives the REAL boot wiring (core.New -> rpc.New), not a
// hand-built Engine — the api.go call site UNIT-3 adds.
func TestGraphCedarSnippets_InstalledAtBoot(t *testing.T) {
	api := cedarWiringAPI(t, "") // no user policy file; the embedded graph snippets must still install

	if api.cedarEngine == nil {
		t.Fatal("api.cedarEngine is nil — cedarWiringAPI no longer builds a real DataDir-backed engine; this test needs a live engine to mean anything")
	}

	evalAuthor := func(enabled string) cedar.Decision {
		return api.cedarEngine.Evaluate(
			context.Background(),
			cedar.UserUID(),
			cedar.ActionGraphAuthor,
			cedar.GraphUID("x"),
			map[cedarlib.String]cedarlib.Value{
				cedarlib.String("authoring_enabled"): cedarlib.String(enabled),
				cedarlib.String("session_kind"):      cedarlib.String("chat"),
				cedarlib.String("node_kinds"):        cedarlib.String("plan,model"),
				cedarlib.String("node_count"):        cedarlib.Long(2),
			},
		)
	}

	// Mutation: skip the graphview.CedarSnippets install block in
	// New(). Both assertions below must fail — a version that only
	// checked the disabled side would pass vacuously (NotApplicable also
	// denies-by-absence in the caller, not at this policy layer) and a
	// version that only checked the enabled side would pass vacuously
	// against an engine with nothing installed (NotApplicable -> Allow
	// by default).
	if d := evalAuthor("false"); d.Outcome != cedar.Deny {
		t.Fatalf("authoring disabled: want Deny, got %s (reason=%s, matched=%s) — graph_author_forbid.cedar did not reach the engine",
			d.Outcome, d.Reason, d.MatchedPolicy)
	}
	if d := evalAuthor("true"); d.Outcome != cedar.Allow {
		t.Fatalf("authoring enabled: want Allow, got %s (reason=%s) — graph_author_permit.cedar did not reach the engine",
			d.Outcome, d.Reason)
	}

	files := api.cedarEngine.ListPolicies()
	installed := 0
	for _, f := range files {
		if strings.HasPrefix(f.Name, "graph_") && strings.HasSuffix(f.Name, ".cedar") {
			if !f.Embedded {
				t.Errorf("ListPolicies: %s Embedded=false, want true", f.Name)
			}
			if !f.ParseOK {
				t.Errorf("ListPolicies: %s ParseOK=false (err=%s)", f.Name, f.ParseErr)
			}
			installed++
		}
	}
	if installed != 4 {
		t.Fatalf("expected exactly 4 embedded graph-authoring policy files listed after boot, got %d (%+v)", installed, files)
	}
}

// TestGraphCedarSnippets_WriteFileForbidden_AtBoot pins the FR-008
// escalation control through the real boot wiring: a draft whose
// node_kinds contains write_file is denied even with authoring enabled.
func TestGraphCedarSnippets_WriteFileForbidden_AtBoot(t *testing.T) {
	api := cedarWiringAPI(t, "")
	if api.cedarEngine == nil {
		t.Fatal("api.cedarEngine is nil")
	}
	d := api.cedarEngine.Evaluate(
		context.Background(),
		cedar.UserUID(),
		cedar.ActionGraphAuthor,
		cedar.GraphUID("x"),
		map[cedarlib.String]cedarlib.Value{
			cedarlib.String("authoring_enabled"): cedarlib.String("true"),
			cedarlib.String("session_kind"):      cedarlib.String("chat"),
			cedarlib.String("node_kinds"):        cedarlib.String("plan,write_file"),
			cedarlib.String("node_count"):        cedarlib.Long(2),
		},
	)
	if d.Outcome != cedar.Deny {
		t.Fatalf("write_file in drafted graph, authoring enabled: want Deny, got %s (reason=%s, matched=%s)", d.Outcome, d.Reason, d.MatchedPolicy)
	}
}

// TestGraphCedarSnippets_RunUnreviewedForbidden_AtBoot pins the FR-007
// human-review interlock's policy half through the real boot wiring.
func TestGraphCedarSnippets_RunUnreviewedForbidden_AtBoot(t *testing.T) {
	api := cedarWiringAPI(t, "")
	if api.cedarEngine == nil {
		t.Fatal("api.cedarEngine is nil")
	}
	d := api.cedarEngine.Evaluate(
		context.Background(),
		cedar.UserUID(),
		cedar.ActionGraphRun,
		cedar.GraphUID("x"),
		map[cedarlib.String]cedarlib.Value{
			cedarlib.String("spec_provenance"): cedarlib.String("model_authored"),
			cedarlib.String("session_kind"):    cedarlib.String("chat"),
			cedarlib.String("initiator"):       cedarlib.String("user"),
		},
	)
	if d.Outcome != cedar.Deny {
		t.Fatalf("model_authored, unreviewed: want Deny, got %s (reason=%s, matched=%s)", d.Outcome, d.Reason, d.MatchedPolicy)
	}
}
