package workflows

import (
	"reflect"
	"testing"

	corewf "github.com/kameas-ai/kenaz-harness/core/workflows"
)

// The wire Step is a SUBSET of corewf.Step, and the subset is load-bearing
// in both directions: projectWorkflow decides what the editor can see, and
// unprojectWorkflow decides what a structured save can persist. A field
// dropped from either projection is silent — the editor renders a graph
// with no edges, or a save quietly rewrites a workflow without them.
//
// visual-graph-authoring-01PMUX01 WP06 added InputsFrom (the DAG itself,
// previously invisible over the bridge) plus Method/URL/Mode (which the
// pre-canvas editor already let you EDIT and already discarded). None of
// it had a Go test: deleting `InputsFrom` from projectWorkflow left the
// whole package green.

func twoStepWorkflow() corewf.Workflow {
	return corewf.Workflow{
		ID:          "wf",
		Name:        "Fetch then parse",
		Description: "two steps, one dependency",
		Version:     3,
		Inputs: []corewf.Input{
			{Name: "topic", Kind: corewf.InputKind("string"), Required: true, Default: "x"},
		},
		Steps: []corewf.Step{
			{
				Name:   "fetch",
				Kind:   corewf.StepKind("http_request"),
				Method: "POST",
				URL:    "https://example.test/api",
			},
			{
				Name:       "parse",
				Kind:       corewf.StepKind("web_scrape"),
				InputsFrom: []string{"fetch"},
				URL:        "https://example.test/page",
				Mode:       "css",
			},
		},
	}
}

// TestProjectWorkflow_CarriesTheDAGAndPerKindFields pins the fields the
// wire Step declares. Deleting any one of them from projectWorkflow fails
// here rather than showing up as an editor that draws a graph with no
// edges.
func TestProjectWorkflow_CarriesTheDAGAndPerKindFields(t *testing.T) {
	got := projectWorkflow(twoStepWorkflow())

	if len(got.Steps) != 2 {
		t.Fatalf("steps = %d, want 2", len(got.Steps))
	}

	fetch, parse := got.Steps[0], got.Steps[1]

	// InputsFrom is the DAG. Without it every workflow projects as a set
	// of unconnected nodes, which is exactly the bug this WP fixed.
	if !reflect.DeepEqual(parse.InputsFrom, []string{"fetch"}) {
		t.Errorf("parse.InputsFrom = %#v, want [fetch]", parse.InputsFrom)
	}
	if len(fetch.InputsFrom) != 0 {
		t.Errorf("fetch.InputsFrom = %#v, want empty", fetch.InputsFrom)
	}

	if fetch.Method != "POST" {
		t.Errorf("fetch.Method = %q, want POST", fetch.Method)
	}
	if fetch.URL != "https://example.test/api" {
		t.Errorf("fetch.URL = %q, want the source URL", fetch.URL)
	}
	if parse.Mode != "css" {
		t.Errorf("parse.Mode = %q, want css", parse.Mode)
	}
	if parse.URL != "https://example.test/page" {
		t.Errorf("parse.URL = %q, want the source URL", parse.URL)
	}
}

// TestWorkflowProjection_RoundTrips is the property that matters for the
// canvas editor: what it loads is what it can save back. project →
// unproject → project must be a fixed point over the wire's own field set,
// or every canvas save silently rewrites the workflow.
func TestWorkflowProjection_RoundTrips(t *testing.T) {
	first := projectWorkflow(twoStepWorkflow())
	second := projectWorkflow(unprojectWorkflow(first))

	if !reflect.DeepEqual(first, second) {
		t.Fatalf("projection is not a fixed point:\n first  = %#v\n second = %#v", first, second)
	}
}

// TestUnprojectWorkflow_RestoresTheWireFields pins the write half
// separately: a field can survive projectWorkflow and still be dropped on
// the way back, which is the shape of the bug the pre-canvas editor had
// with Method/URL/Mode — it displayed them and discarded every edit.
func TestUnprojectWorkflow_RestoresTheWireFields(t *testing.T) {
	got := unprojectWorkflow(projectWorkflow(twoStepWorkflow()))

	if len(got.Steps) != 2 {
		t.Fatalf("steps = %d, want 2", len(got.Steps))
	}
	fetch, parse := got.Steps[0], got.Steps[1]

	if !reflect.DeepEqual(parse.InputsFrom, []string{"fetch"}) {
		t.Errorf("parse.InputsFrom = %#v, want [fetch]", parse.InputsFrom)
	}
	if fetch.Method != "POST" || fetch.URL != "https://example.test/api" {
		t.Errorf("fetch method/url = %q/%q, want POST/https://example.test/api",
			fetch.Method, fetch.URL)
	}
	if parse.Mode != "css" || parse.URL != "https://example.test/page" {
		t.Errorf("parse mode/url = %q/%q, want css/https://example.test/page",
			parse.Mode, parse.URL)
	}
	if got.ID != "wf" || got.Version != 3 {
		t.Errorf("id/version = %q/%d, want wf/3", got.ID, got.Version)
	}
	if len(got.Inputs) != 1 || got.Inputs[0].Name != "topic" {
		t.Errorf("inputs = %#v, want one named topic", got.Inputs)
	}
}

// TestUnprojectWorkflow_DropsWhatTheWireCannotCarry states the KNOWN
// lossiness rather than leaving it to be discovered.
//
// The wire Step carries nine fields; corewf.Step has ~45. Everything else
// is destroyed by a structured save. The frontend warns about exactly this
// (`UNREPRESENTED_FIELDS_BY_KIND` in workflowAdapter.ts), and this test is
// the Go-side statement of the same fact — so if someone widens the wire,
// this test fails and the warning shrinks with it, deliberately.
func TestUnprojectWorkflow_DropsWhatTheWireCannotCarry(t *testing.T) {
	src := corewf.Workflow{
		ID: "wf", Name: "n", Version: 1,
		Steps: []corewf.Step{{
			Name:      "run",
			Kind:      corewf.StepKind("shell"),
			Cmd:       "ls",
			Args:      []string{"-la"},
			Cwd:       "/tmp",
			Env:       map[string]string{"A": "b"},
			TimeoutMS: 5000,
		}},
	}
	got := unprojectWorkflow(projectWorkflow(src)).Steps[0]

	if got.Cmd != "ls" || !reflect.DeepEqual(got.Args, []string{"-la"}) {
		t.Errorf("cmd/args did not survive: %q %#v", got.Cmd, got.Args)
	}
	// Documented loss, not an accident:
	if got.Cwd != "" {
		t.Errorf("Cwd = %q; the wire Step has no cwd field — if it does now, "+
			"shrink UNREPRESENTED_FIELDS_BY_KIND in workflowAdapter.ts too", got.Cwd)
	}
	if got.Env != nil {
		t.Errorf("Env = %#v; the wire Step has no env field — see the note above", got.Env)
	}
	if got.TimeoutMS != 0 {
		t.Errorf("TimeoutMS = %d; the wire Step has no timeoutMs field — see the note above",
			got.TimeoutMS)
	}
}
