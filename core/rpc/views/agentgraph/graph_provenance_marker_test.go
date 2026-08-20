package agentgraph_test

// graph_provenance_marker_test.go — model-authored-graphs-01PMGA01
// UNIT-5.
//
// FR-009: the model-authored marker is stamped by the server and
// cannot be forged or omitted. FR-010's backend half: an unreviewed
// model-authored graph does not run; a human clearing the marker by
// saving from the editor lets it run.
//
// All assertions read the persisted FILE on disk (spec §11.1) — never
// a returned struct — because AC-007's whole point is that the
// SUBMITTED value is discarded, not merely that some in-memory Graph
// happens to carry the right field afterward.

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	cedarlib "github.com/cedar-policy/cedar-go"
	"github.com/kameas-ai/kenaz-harness/core/policy/cedar"
	graphview "github.com/kameas-ai/kenaz-harness/core/rpc/views/agentgraph"
)

func readLibraryFile(t *testing.T, dir, id string) string {
	t.Helper()
	full := filepath.Join(dir, "agent_graph", "library", id+".yaml")
	data, err := os.ReadFile(full)
	if err != nil {
		t.Fatalf("ReadFile(%s): %v", full, err)
	}
	return string(data)
}

// TestSaveGraph_ModelInitiator_StampsProvenance is AC-007: three
// submitted values for spec_provenance — absent, empty string, and
// "library_fallback" — all persist as "model_authored" for a
// model-initiated save. The submitted value is never passed through.
func TestSaveGraph_ModelInitiator_StampsProvenance(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name  string
		extra string // extra YAML line to inject, or "" for absent
	}{
		{name: "absent", extra: ""},
		{name: "empty_string", extra: "spec_provenance: \"\"\n"},
		{name: "library_fallback", extra: "spec_provenance: library_fallback\n"},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			dir := t.TempDir()
			mgr, err := graphview.NewManager(graphview.WithDataDir(dir))
			if err != nil {
				t.Fatalf("NewManager: %v", err)
			}
			a := graphview.New(mgr)
			ctx := context.Background()

			id := "zz_stamp_" + tc.name
			yaml := "spec_version: \"1\"\nid: " + id + "\n" + tc.extra +
				"entrypoints: [a]\nnodes:\n  - id: a\n    kind: plan\n    attrs:\n      verbosity: terse\n"

			if err := a.SaveGraph(ctx, graphview.GraphSpec{ID: id, YAML: yaml}, "model"); err != nil {
				t.Fatalf("SaveGraph(model): %v", err)
			}

			onDisk := readLibraryFile(t, dir, id)
			if !strings.Contains(onDisk, "spec_provenance: model_authored") {
				t.Errorf("case %s: persisted file does not carry the stamped marker; got:\n%s", tc.name, onDisk)
			}
			// Note: the graph id itself is "zz_stamp_library_fallback"
			// for the library_fallback case, so a bare
			// strings.Contains(onDisk, "library_fallback") would false-
			// positive on the id: line. Assert the FIELD specifically.
			if strings.Contains(onDisk, "spec_provenance: library_fallback") {
				t.Errorf("case %s: persisted file still carries the submitted library_fallback value — the stamp did not overwrite it", tc.name)
			}
		})
	}
}

// TestSaveGraph_UserInitiator_ClearsProvenance is UNIT-5's other half:
// loading a model_authored graph and saving it back through the
// initiator="user" path (the editor's save) must persist WITHOUT the
// marker — the human review being recorded.
func TestSaveGraph_UserInitiator_ClearsProvenance(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	mgr, err := graphview.NewManager(graphview.WithDataDir(dir))
	if err != nil {
		t.Fatalf("NewManager: %v", err)
	}
	a := graphview.New(mgr)
	ctx := context.Background()

	id := "zz_clear_provenance"
	yaml := "spec_version: \"1\"\nid: " + id + "\nentrypoints: [a]\nnodes:\n  - id: a\n    kind: plan\n    attrs:\n      verbosity: terse\n"

	// Model-initiated save stamps it.
	if err := a.SaveGraph(ctx, graphview.GraphSpec{ID: id, YAML: yaml}, "model"); err != nil {
		t.Fatalf("SaveGraph(model): %v", err)
	}
	stamped := readLibraryFile(t, dir, id)
	if !strings.Contains(stamped, "spec_provenance: model_authored") {
		t.Fatalf("setup: model save did not stamp the marker; got:\n%s", stamped)
	}

	// Load it back (as the editor would) and save through the user path.
	loaded, lerr := a.LoadGraph(ctx, id)
	if lerr != nil {
		t.Fatalf("LoadGraph: %v", lerr)
	}
	if err := a.SaveGraph(ctx, graphview.GraphSpec{ID: id, YAML: loaded.YAML}, "user"); err != nil {
		t.Fatalf("SaveGraph(user): %v", err)
	}

	cleared := readLibraryFile(t, dir, id)
	if strings.Contains(cleared, "model_authored") {
		t.Errorf("user-initiated save did not clear the marker; got:\n%s", cleared)
	}
}

// TestSaveGraph_UserInitiator_CannotForgeProvenance closes the other
// forgery direction implied by FR-009: a "user" save is not a loophole
// for setting spec_provenance to model_authored either — the field
// only ever comes FROM the server's own stamp, never from submitted
// text, on either initiator.
func TestSaveGraph_UserInitiator_CannotForgeProvenance(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	mgr, err := graphview.NewManager(graphview.WithDataDir(dir))
	if err != nil {
		t.Fatalf("NewManager: %v", err)
	}
	a := graphview.New(mgr)
	ctx := context.Background()

	id := "zz_user_forge_attempt"
	yaml := "spec_version: \"1\"\nid: " + id + "\nspec_provenance: model_authored\nentrypoints: [a]\nnodes:\n  - id: a\n    kind: plan\n    attrs:\n      verbosity: terse\n"

	if err := a.SaveGraph(ctx, graphview.GraphSpec{ID: id, YAML: yaml}, "user"); err != nil {
		t.Fatalf("SaveGraph(user): %v", err)
	}
	onDisk := readLibraryFile(t, dir, id)
	if strings.Contains(onDisk, "model_authored") {
		t.Errorf("a user-initiated save persisted a self-declared model_authored marker; got:\n%s", onDisk)
	}
}

// TestStartRun_ModelAuthored_DeniedUntilReviewed is AC-008's backend
// half, at the Manager level with a real (deny-when-model-authored)
// gate: startRun refuses a model_authored graph, and after the user
// path clears the marker, the identical startRun call proceeds past
// the gate. The real boot-wiring equivalent (with the actual shipped
// Cedar policy) lives in core/rpc's Cedar-wiring test suite; this test
// pins the Manager-level plumbing in isolation.
func TestStartRun_ModelAuthored_DeniedUntilReviewed(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	mgr, err := graphview.NewManager(
		graphview.WithDataDir(dir),
		graphview.WithGraphCedarGate(unreviewedForbidGate{}),
	)
	if err != nil {
		t.Fatalf("NewManager: %v", err)
	}
	a := graphview.New(mgr)
	ctx := context.Background()

	id := "zz_run_unreviewed"
	yaml := "spec_version: \"1\"\nid: " + id + "\nentrypoints: [t]\nnodes:\n  - id: t\n    kind: transform\n    attrs:\n      name: concat\n      params:\n        sep: \"-\"\n"

	if err := a.SaveGraph(ctx, graphview.GraphSpec{ID: id, YAML: yaml}, "model"); err != nil {
		t.Fatalf("SaveGraph(model): %v", err)
	}

	_, err = a.StartRun(ctx, graphview.StartRunRequest{GraphID: id})
	if err == nil {
		t.Fatal("StartRun succeeded on an unreviewed model_authored graph")
	}
	var pderr *cedar.PolicyDeniedError
	if !errors.As(err, &pderr) {
		t.Fatalf("err = %v (%T); want *cedar.PolicyDeniedError", err, err)
	}

	// Human review: load + save through the user path, which clears
	// the marker.
	loaded, lerr := a.LoadGraph(ctx, id)
	if lerr != nil {
		t.Fatalf("LoadGraph: %v", lerr)
	}
	if err := a.SaveGraph(ctx, graphview.GraphSpec{ID: id, YAML: loaded.YAML}, "user"); err != nil {
		t.Fatalf("SaveGraph(user): %v", err)
	}

	if _, err := a.StartRun(ctx, graphview.StartRunRequest{GraphID: id}); err != nil {
		t.Fatalf("StartRun after human review still refused: %v", err)
	}
}

// unreviewedForbidGate is a minimal cedar.Gate mirroring the shipped
// graph_run_unreviewed_forbid.cedar policy's own logic: deny graph.run
// when context.spec_provenance == "model_authored", allow otherwise.
type unreviewedForbidGate struct{}

func (unreviewedForbidGate) Evaluate(_ context.Context, _ cedarlib.EntityUID, action string, _ cedarlib.EntityUID, attrs map[cedarlib.String]cedarlib.Value) cedar.Decision {
	if action != cedar.ActionGraphRun {
		return cedar.Decision{Outcome: cedar.Allow, Action: action}
	}
	if v, ok := attrs[cedarlib.String("spec_provenance")]; ok {
		if s, ok := v.(cedarlib.String); ok && string(s) == "model_authored" {
			return cedar.Decision{Outcome: cedar.Deny, Action: action, Reason: "test: unreviewed model_authored graph"}
		}
	}
	return cedar.Decision{Outcome: cedar.Allow, Action: action}
}
