package agentgraph_test

// cedar_policy_test.go — model-authored-graphs-01PMGA01 UNIT-3.
//
// Exercises the four shipped graph-authoring Cedar policies against a
// real cedar.Engine, the same way core/policy/cedar's own
// TestLoadHarnessSnippets_* suite exercises the harness-self bundle:
// construct an engine with no embedded default and no disk load, call
// LoadHarnessSnippets with graphview.CedarSnippets()'s real embedded
// bytes, then Evaluate. Nothing here calls GateGraphAuthor / GateGraphRun
// — those callers do not exist until UNIT-4 (spec.md: "nothing evaluates
// them yet, so nothing changes and nothing lies" is UNIT-3's own
// atomicity claim) — this file proves the POLICY TEXT behaves correctly
// once evaluated directly, independent of the caller.

import (
	"context"
	"testing"

	cedarlib "github.com/cedar-policy/cedar-go"
	"github.com/kameas-ai/kenaz-harness/core/policy/cedar"
	graphview "github.com/kameas-ai/kenaz-harness/core/rpc/views/agentgraph"
)

func newGraphPolicyEngine(t *testing.T) *cedar.Engine {
	t.Helper()
	e, err := cedar.NewEngine(cedar.Options{IncludeEmbedded: false, LoadFromDisk: false})
	if err != nil {
		t.Fatalf("NewEngine: %v", err)
	}
	snippets, err := graphview.CedarSnippets()
	if err != nil {
		t.Fatalf("CedarSnippets: %v", err)
	}
	if len(snippets) != 4 {
		t.Fatalf("CedarSnippets returned %d files, want 4: %v", len(snippets), snippetNames(snippets))
	}
	if err := e.LoadHarnessSnippets(snippets); err != nil {
		t.Fatalf("LoadHarnessSnippets: %v", err)
	}
	return e
}

func snippetNames(m map[string][]byte) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}

func evalGraphAuthor(e *cedar.Engine, authoringEnabled, nodeKinds string) cedar.Decision {
	return e.Evaluate(
		context.Background(),
		cedar.UserUID(),
		cedar.ActionGraphAuthor,
		cedar.GraphUID("x"),
		map[cedarlib.String]cedarlib.Value{
			cedarlib.String("authoring_enabled"): cedarlib.String(authoringEnabled),
			cedarlib.String("session_kind"):      cedarlib.String("chat"),
			cedarlib.String("node_kinds"):        cedarlib.String(nodeKinds),
			cedarlib.String("node_count"):        cedarlib.Long(1),
		},
	)
}

func evalGraphRun(e *cedar.Engine, specProvenance string) cedar.Decision {
	return e.Evaluate(
		context.Background(),
		cedar.UserUID(),
		cedar.ActionGraphRun,
		cedar.GraphUID("x"),
		map[cedarlib.String]cedarlib.Value{
			cedarlib.String("spec_provenance"): cedarlib.String(specProvenance),
			cedarlib.String("session_kind"):    cedarlib.String("chat"),
			cedarlib.String("initiator"):       cedarlib.String("user"),
		},
	)
}

// TestGraphAuthorPolicy_DeniedWhenDisabled is AC-004's policy half:
// authoring_enabled absent/false denies graph.author even for an
// otherwise harmless draft. Mutation (spec AC-004 B): skip the install
// entirely — every evaluation degrades to NotApplicable, which this
// test's Deny assertion would then fail to observe, catching the skip.
func TestGraphAuthorPolicy_DeniedWhenDisabled(t *testing.T) {
	t.Parallel()
	e := newGraphPolicyEngine(t)
	d := evalGraphAuthor(e, "false", "plan,model")
	if d.Outcome != cedar.Deny {
		t.Fatalf("authoring disabled: want Deny, got %s (reason=%s matched=%s)", d.Outcome, d.Reason, d.MatchedPolicy)
	}
}

// TestGraphAuthorPolicy_AllowedWhenEnabled is AC-004's positive half:
// authoring_enabled == "true" with a harmless node-kind set permits.
// This is the direction graph_author_permit.cedar exists for — without
// an explicit permit, the engine's default-allow-on-NotApplicable
// posture would let this pass even if the whole snippet set failed to
// install, masking exactly the failure this test exists to catch.
func TestGraphAuthorPolicy_AllowedWhenEnabled(t *testing.T) {
	t.Parallel()
	e := newGraphPolicyEngine(t)
	d := evalGraphAuthor(e, "true", "plan,model")
	if d.Outcome != cedar.Allow {
		t.Fatalf("authoring enabled, no write_file: want Allow, got %s (reason=%s matched=%s)", d.Outcome, d.Reason, d.MatchedPolicy)
	}
	if d.MatchedPolicy == "" {
		t.Error("Allow with no MatchedPolicy — the permit rule did not fire, this Allow came from NotApplicable default-allow instead")
	}
}

// TestGraphAuthorPolicy_WriteFileForbiddenEvenWhenEnabled is AC-006: the
// FR-008 escalation control. A draft containing write_file is denied
// regardless of the consent dial.
//
// Mutation A (spec AC-006): stop passing node_kinds in context — covered
// by the caller (UNIT-4), not this policy-level test. Mutation B: remove
// the write_file clause from the shipped policy — this test IS that
// mutation's regression guard; deleting graph_author_no_write_file.cedar
// (or its `like` clause) turns this Deny into an Allow.
func TestGraphAuthorPolicy_WriteFileForbiddenEvenWhenEnabled(t *testing.T) {
	t.Parallel()
	e := newGraphPolicyEngine(t)
	d := evalGraphAuthor(e, "true", "plan,model,write_file")
	if d.Outcome != cedar.Deny {
		t.Fatalf("write_file in node_kinds: want Deny even with authoring enabled, got %s (reason=%s matched=%s)", d.Outcome, d.Reason, d.MatchedPolicy)
	}
}

// TestGraphRunPolicy_DeniedWhenModelAuthored is the policy half of
// AC-008 / FR-007's human-review interlock: a graph whose provenance is
// "model_authored" is denied a run.
func TestGraphRunPolicy_DeniedWhenModelAuthored(t *testing.T) {
	t.Parallel()
	e := newGraphPolicyEngine(t)
	d := evalGraphRun(e, "model_authored")
	if d.Outcome != cedar.Deny {
		t.Fatalf("model_authored provenance: want Deny, got %s (reason=%s matched=%s)", d.Outcome, d.Reason, d.MatchedPolicy)
	}
}

// TestGraphRunPolicy_AllowedWhenReviewed asserts the OTHER direction —
// spec.md §7 rule 1 ("assert the refusal, not the gate") implies its
// mirror: a test that only ever exercises Deny cannot tell "the policy
// discriminates on provenance" from "the policy denies graph.run
// unconditionally". An empty / non-model-authored provenance must not
// be denied by this policy (NotApplicable, since no permit ships for
// graph.run — UNIT-4's nil-gate default-allow contract covers the
// no-engine-wired case; this asserts the shipped forbid does not
// over-fire).
func TestGraphRunPolicy_AllowedWhenReviewed(t *testing.T) {
	t.Parallel()
	e := newGraphPolicyEngine(t)
	for _, prov := range []string{"", "library_fallback"} {
		d := evalGraphRun(e, prov)
		if d.Outcome == cedar.Deny {
			t.Errorf("spec_provenance=%q: graph_run_unreviewed_forbid.cedar over-fired; got Deny (matched=%s)", prov, d.MatchedPolicy)
		}
	}
}

// TestGraphPolicies_InstallCount pins the four-file expectation the
// boot-time install (core/rpc/api.go) asserts loudly. A count drift here
// means the boot log's "cedar.graph_snippets_count_unexpected" fires in
// production.
func TestGraphPolicies_InstallCount(t *testing.T) {
	t.Parallel()
	snippets, err := graphview.CedarSnippets()
	if err != nil {
		t.Fatalf("CedarSnippets: %v", err)
	}
	want := []string{
		"graph_author_forbid.cedar",
		"graph_author_permit.cedar",
		"graph_author_no_write_file.cedar",
		"graph_run_unreviewed_forbid.cedar",
	}
	if len(snippets) != len(want) {
		t.Fatalf("got %d snippets, want %d: %v", len(snippets), len(want), snippetNames(snippets))
	}
	for _, name := range want {
		if _, ok := snippets[name]; !ok {
			t.Errorf("missing expected snippet %q; got %v", name, snippetNames(snippets))
		}
	}
}
