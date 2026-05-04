package catalog_test

import (
	"testing"

	"github.com/sigil-tech/kaneaz-harness/core/workflows/catalog"
	corewf "github.com/sigil-tech/kaneaz-harness/core/workflows"
)

// TestProjectEntry_NetworkGrantForWebFetch confirms that a workflow
// containing a web_fetch step results in "network" in RequiresCedarGrants.
func TestProjectEntry_NetworkGrantForWebFetch(t *testing.T) {
	t.Parallel()
	w := corewf.Workflow{
		ID:      "fetch-test",
		Name:    "Fetch Test",
		Version: 1,
		Steps: []corewf.Step{
			{Name: "fetch", Kind: corewf.StepKindWebFetch, URL: "https://example.com"},
		},
	}
	e := catalog.ProjectEntry(w)
	hasNetwork := false
	for _, g := range e.RequiresCedarGrants {
		if g == "network" {
			hasNetwork = true
		}
	}
	if !hasNetwork {
		t.Errorf("expected 'network' in RequiresCedarGrants, got %v", e.RequiresCedarGrants)
	}
}

// TestProjectEntry_ShellGrantForShell confirms shell step → "shell" grant.
func TestProjectEntry_ShellGrantForShell(t *testing.T) {
	t.Parallel()
	w := corewf.Workflow{
		ID:      "shell-test",
		Name:    "Shell Test",
		Version: 1,
		Steps: []corewf.Step{
			{Name: "run", Kind: corewf.StepKindShell, Cmd: "echo hi"},
		},
	}
	e := catalog.ProjectEntry(w)
	hasShell := false
	for _, g := range e.RequiresCedarGrants {
		if g == "shell" {
			hasShell = true
		}
	}
	if !hasShell {
		t.Errorf("expected 'shell' in RequiresCedarGrants, got %v", e.RequiresCedarGrants)
	}
}

// TestProjectEntry_NoDuplicateGrants confirms grants are deduped.
func TestProjectEntry_NoDuplicateGrants(t *testing.T) {
	t.Parallel()
	w := corewf.Workflow{
		ID:      "dup-test",
		Name:    "Dup Test",
		Version: 1,
		Steps: []corewf.Step{
			{Name: "f1", Kind: corewf.StepKindWebFetch, URL: "https://a.com"},
			{Name: "f2", Kind: corewf.StepKindWebFetch, URL: "https://b.com"},
		},
	}
	e := catalog.ProjectEntry(w)
	seen := make(map[string]int)
	for _, g := range e.RequiresCedarGrants {
		seen[g]++
	}
	if seen["network"] > 1 {
		t.Errorf("'network' grant duplicated: %v", e.RequiresCedarGrants)
	}
}

// TestProjectEntry_EstimatesPositiveCostForModelTurn confirms that a
// workflow with model_turn steps has a non-zero estimated cost.
func TestProjectEntry_EstimatesPositiveCostForModelTurn(t *testing.T) {
	t.Parallel()
	w := corewf.Workflow{
		ID:      "cost-test",
		Name:    "Cost Test",
		Version: 1,
		Steps: []corewf.Step{
			{Name: "turn", Kind: corewf.StepKindModelTurn, UserPrompt: "do something"},
		},
	}
	e := catalog.ProjectEntry(w)
	if e.EstimatedCostUSD <= 0 {
		t.Errorf("expected positive EstimatedCostUSD for model_turn workflow, got %v", e.EstimatedCostUSD)
	}
}

// TestProjectEntry_ZeroCostForNoModelTurns.
func TestProjectEntry_ZeroCostForNoModelTurns(t *testing.T) {
	t.Parallel()
	w := corewf.Workflow{
		ID:      "nocost-test",
		Name:    "No Cost Test",
		Version: 1,
		Steps: []corewf.Step{
			{Name: "run", Kind: corewf.StepKindShell, Cmd: "echo hi"},
		},
	}
	e := catalog.ProjectEntry(w)
	if e.EstimatedCostUSD != 0 {
		t.Errorf("expected zero EstimatedCostUSD for shell-only workflow, got %v", e.EstimatedCostUSD)
	}
}
