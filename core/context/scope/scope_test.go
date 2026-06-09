package scope

import (
	"testing"

	pack "github.com/kameas-ai/kenaz-harness/core/context/pack"
)

func TestAllow_NoScope_AppliesEverywhere(t *testing.T) {
	e := pack.ContextEntry{Name: "term"}
	if !Allow(e, "", "") {
		t.Errorf("unrestricted entry should apply with no workflow/agent")
	}
	if !Allow(e, "wfX", "agentY") {
		t.Errorf("unrestricted entry should apply for any (wf, agent)")
	}
}

func TestAllow_WorkflowRestricted(t *testing.T) {
	e := pack.ContextEntry{
		Name:  "rollup",
		Scope: pack.Scope{Workflows: []string{"quarterly-rollup"}},
	}
	if !Allow(e, "quarterly-rollup", "") {
		t.Errorf("entry should apply for matching workflow")
	}
	if Allow(e, "weekly-rollup", "") {
		t.Errorf("entry should not apply for other workflow")
	}
	if Allow(e, "", "") {
		t.Errorf("workflow-restricted entry should not match empty workflow")
	}
}

func TestAllow_AgentRestricted(t *testing.T) {
	e := pack.ContextEntry{
		Name:  "x",
		Scope: pack.Scope{Agents: []string{"analyst"}},
	}
	if !Allow(e, "any", "analyst") {
		t.Errorf("agent match expected")
	}
	if Allow(e, "any", "engineer") {
		t.Errorf("non-matching agent should be filtered")
	}
}

func TestAllow_BothMustMatch(t *testing.T) {
	e := pack.ContextEntry{
		Name: "x",
		Scope: pack.Scope{
			Workflows: []string{"wf1"},
			Agents:    []string{"a1"},
		},
	}
	if !Allow(e, "wf1", "a1") {
		t.Errorf("both matching: should apply")
	}
	if Allow(e, "wf2", "a1") {
		t.Errorf("workflow mismatch: must not apply")
	}
	if Allow(e, "wf1", "a2") {
		t.Errorf("agent mismatch: must not apply")
	}
}

func TestFilter_PreservesInputOrder(t *testing.T) {
	in := []pack.ContextEntry{
		{Name: "a"},
		{Name: "b", Scope: pack.Scope{Workflows: []string{"keep"}}},
		{Name: "c", Scope: pack.Scope{Workflows: []string{"drop"}}},
		{Name: "d"},
	}
	got := Filter(in, "keep", "")
	want := []string{"a", "b", "d"}
	if len(got) != len(want) {
		t.Fatalf("len = %d, want %d", len(got), len(want))
	}
	for i, e := range got {
		if e.Name != want[i] {
			t.Errorf("got[%d] = %q, want %q", i, e.Name, want[i])
		}
	}
}
