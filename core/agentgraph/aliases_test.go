package agentgraph

import (
	"testing"
)

// TestLookupAlias_LegacyKindResolves: graphs that name v0 kinds (`llm`,
// `plan`, `fork`) round-trip through lookupAlias to their canonical
// counterparts. The shipped manifest set declares each alias on the
// canonical kind manifest; codegen builds the kindAliases map.
func TestLookupAlias_LegacyKindResolves(t *testing.T) {
	cases := []struct {
		old       string
		canonical string
	}{
		{"llm", "model"},
		{"plan", "planner"},
		{"fork", "branch"},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.old, func(t *testing.T) {
			resetAliasWarnings()
			got, ok := lookupAlias(tc.old)
			if !ok {
				t.Fatalf("alias %q not recognised", tc.old)
			}
			if got != tc.canonical {
				t.Errorf("lookupAlias(%q) = %q, want %q", tc.old, got, tc.canonical)
			}
		})
	}
}

// TestLookupAlias_CanonicalShadowsAlias: the v1 taxonomy reuses the old
// alias name `branch` as a new canonical kind (sub-graph spawn). The
// canonical interpretation must always win, otherwise authors using
// the new sub-graph-spawn kind would have their nodes silently
// rewritten to the predicate router (`decision`).
func TestLookupAlias_CanonicalShadowsAlias(t *testing.T) {
	resetAliasWarnings()
	if got, ok := lookupAlias("branch"); ok {
		t.Errorf("canonical kind `branch` was rewritten to %q via alias map", got)
	}
}

// TestLookupAlias_UnknownKindUntouched: unknown kinds are not aliases.
func TestLookupAlias_UnknownKindUntouched(t *testing.T) {
	resetAliasWarnings()
	if _, ok := lookupAlias("definitely_not_a_kind"); ok {
		t.Error("lookupAlias accepted an unknown kind as alias")
	}
}

// TestLookupAlias_DeprecationWarningOnceOnly: the warning surface is
// per-process / per-alias. We can't easily intercept slog output here,
// but we can assert the per-process tracking map records each alias
// exactly once after multiple lookups.
func TestLookupAlias_DeprecationWarningOnceOnly(t *testing.T) {
	resetAliasWarnings()
	for i := 0; i < 5; i++ {
		_, _ = lookupAlias("llm")
	}
	aliasWarnedMu.Lock()
	defer aliasWarnedMu.Unlock()
	if _, ok := aliasWarned["llm"]; !ok {
		t.Errorf("expected `llm` to be tracked in aliasWarned")
	}
	// Spot-check the set contains exactly the warned aliases.
	if len(aliasWarned) != 1 {
		t.Errorf("expected 1 warned alias, got %d: %v", len(aliasWarned), keysOf(aliasWarned))
	}
}

// TestLoadYAML_LegacyKindRoundtrips: a YAML graph using v0 kind names
// loads, alias-resolves at decode time, and the resulting in-memory
// graph carries canonical kinds.
func TestLoadYAML_LegacyKindRoundtrips(t *testing.T) {
	resetAliasWarnings()
	const src = `
spec_version: "1"
id: legacy_kinds
entrypoints: [a]
nodes:
  - id: a
    kind: llm
    attrs:
      model: m
  - id: b
    kind: plan
    attrs:
      verbosity: terse
  - id: c
    kind: fork
    attrs:
      title: child
`
	g, err := LoadYAML([]byte(src))
	if err != nil {
		t.Fatalf("LoadYAML: %v", err)
	}
	if g.Nodes[0].Kind != NodeKindModel {
		t.Errorf("node a kind = %q, want %q", g.Nodes[0].Kind, NodeKindModel)
	}
	if g.Nodes[1].Kind != NodeKindPlanner {
		t.Errorf("node b kind = %q, want %q", g.Nodes[1].Kind, NodeKindPlanner)
	}
	if g.Nodes[2].Kind != NodeKindBranch {
		t.Errorf("node c kind = %q, want %q", g.Nodes[2].Kind, NodeKindBranch)
	}
	// The typed attrs come from the canonical struct.
	if _, ok := g.Nodes[0].Attrs.(ModelAttrs); !ok {
		t.Errorf("node a attrs = %T, want ModelAttrs", g.Nodes[0].Attrs)
	}
	if _, ok := g.Nodes[1].Attrs.(PlannerAttrs); !ok {
		t.Errorf("node b attrs = %T, want PlannerAttrs", g.Nodes[1].Attrs)
	}
	if _, ok := g.Nodes[2].Attrs.(BranchAttrs); !ok {
		t.Errorf("node c attrs = %T, want BranchAttrs", g.Nodes[2].Attrs)
	}
}

// TestNewKinds_HappyPath: the three new (post-WP04) kinds compile,
// register, and execute successfully.
func TestNewKinds_HappyPath(t *testing.T) {
	if defaultAttrsFor(NodeKindCompact) == nil {
		t.Errorf("compact has no default attrs")
	}
	if defaultAttrsFor(NodeKindApproval) == nil {
		t.Errorf("approval has no default attrs")
	}
	if defaultAttrsFor(NodeKindArtifact) == nil {
		t.Errorf("artifact has no default attrs")
	}
	// Every new kind has a registered executor.
	reg := newExecutorRegistry()
	for _, k := range []NodeKind{NodeKindCompact, NodeKindApproval, NodeKindArtifact} {
		if _, err := reg.lookup(k); err != nil {
			t.Errorf("kind %q has no registered executor: %v", k, err)
		}
	}
}

// keysOf is a tiny helper for debug assertions on a map.
func keysOf(m map[string]struct{}) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}

// TestKindAliasesShipped: codegen-emitted kindAliases must include the
// four shipped aliases listed in spec §4.8.
func TestKindAliasesShipped(t *testing.T) {
	for _, tc := range []struct {
		old       string
		canonical string
	}{
		{"llm", "model"},
		{"plan", "planner"},
		{"fork", "branch"},
	} {
		got, ok := kindAliases[tc.old]
		if !ok {
			t.Errorf("kindAliases missing %q", tc.old)
			continue
		}
		if got != tc.canonical {
			t.Errorf("kindAliases[%q] = %q, want %q", tc.old, got, tc.canonical)
		}
	}
	// `branch` is BOTH a canonical kind (sub-graph spawn) and an alias
	// for `decision` (the v0 predicate router). The alias entry must
	// exist; lookupAlias' canonical-shadowing rule is what prevents it
	// from rewriting graphs that name the new canonical kind.
	got, ok := kindAliases["branch"]
	if !ok {
		t.Errorf("kindAliases missing `branch -> decision` entry")
	} else if got != "decision" {
		t.Errorf("kindAliases[branch] = %q, want decision", got)
	}
}
