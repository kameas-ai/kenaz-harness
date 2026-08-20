package agentgraph_test

// library_write_validate_test.go — model-authored-graphs-01PMGA01 UNIT-2.
//
// FR-002/FR-003: Manager.saveGraph never called coreag.Validate before
// this unit (manager.go:332-367 parsed, checked the id, checked the
// bundled-shadow reservation, then wrote — verified 2026-08-19 against
// origin/main @ 55029354, spec.md §0.2). The only validation on the save
// path was the frontend's pre-save call to Graph_Validate
// (GraphEditor.vue:392-410); any non-UI caller of Graph_SaveGraph
// persisted an unvalidated graph, and startRun's own validate
// (manager.go:438) surfaced the failure later as "a run that cannot
// start" rather than refusing the save. AC-002/AC-003 pin the fix from
// the real filesystem library path (WithDataDir(t.TempDir())), never a
// fake store — spec §11.1.

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	graphview "github.com/kameas-ai/kenaz-harness/core/rpc/views/agentgraph"
)

func validGraphYAML(id string) string {
	return "spec_version: \"1\"\nid: " + id + "\nentrypoints: [a]\nnodes:\n  - id: a\n    kind: plan\n    attrs:\n      verbosity: terse\n"
}

// TestSaveGraph_RejectsInvalid_NoFileWritten is AC-002's first half: a
// parses-but-invalid draft returns the validator's per-rule issues and
// writes nothing — no final file, and no ".tmp" temp file either (the
// write is WriteFile-then-Rename, manager.go:363-366+, so a rejected
// validation must never even reach that step).
//
// Mutation A (spec AC-002): revert saveGraph to parse-only. This test
// must fail — the reverted code writes the file and returns nil.
func TestSaveGraph_RejectsInvalid_NoFileWritten(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	mgr, err := graphview.NewManager(graphview.WithDataDir(dir))
	if err != nil {
		t.Fatalf("NewManager: %v", err)
	}
	a := graphview.New(mgr)
	ctx := context.Background()

	id := "invalid_draft"
	yaml := sprintfInvalid(id)

	err = a.SaveGraph(ctx, graphview.GraphSpec{ID: id, YAML: yaml})
	if err == nil {
		t.Fatal("SaveGraph accepted an invalid graph")
	}
	var verr *graphview.ValidationFailedError
	if !errors.As(err, &verr) {
		t.Fatalf("SaveGraph error is not *ValidationFailedError: %v (%T)", err, err)
	}
	if len(verr.Issues) == 0 {
		t.Error("ValidationFailedError carries no issues")
	}

	// Real filesystem check, not a returned-struct check (spec §11.1 /
	// §11.2): nothing under the library directory at all.
	libDir := filepath.Join(dir, "agent_graph", "library")
	entries, rerr := os.ReadDir(libDir)
	if rerr == nil {
		for _, e := range entries {
			t.Errorf("library dir contains %q after a rejected save", e.Name())
		}
	} else if !os.IsNotExist(rerr) {
		t.Fatalf("ReadDir(%s): %v", libDir, rerr)
	}

	// No stray .tmp file either — the write-then-rename must never have
	// started.
	tmpPath := filepath.Join(libDir, id+".yaml.tmp")
	if _, terr := os.Stat(tmpPath); terr == nil {
		t.Errorf("stray .tmp file left behind: %s", tmpPath)
	}
}

// TestSaveGraph_RejectsInvalid_DirectAPICall is AC-002's second half —
// "call Graph_SaveGraph directly with the same YAML: it must also be
// refused." Graph_SaveGraph (the Wails binding) forwards to exactly this
// Impl.SaveGraph call with no intermediate validation step of its own,
// so this IS the direct-call path the binding uses.
//
// Mutation B (spec AC-002): move the validation into a tool handler
// instead of saveGraph. Since this mission builds no tool, that
// mutation's failure mode is structural here — Impl.SaveGraph has no
// other caller to have "moved" the check to, so a reversion of the
// saveGraph-level Validate call is exactly what
// TestSaveGraph_RejectsInvalid_NoFileWritten already catches. This test
// pins that Impl.SaveGraph itself — the direct non-editor entrypoint —
// is the thing that refuses, not some UI-layer wrapper around it.
func TestSaveGraph_RejectsInvalid_DirectAPICall(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	mgr, err := graphview.NewManager(graphview.WithDataDir(dir))
	if err != nil {
		t.Fatalf("NewManager: %v", err)
	}
	// Impl wraps a Manager with NO Cedar gate, no session concept, no
	// editor-only pre-check — this is the raw API surface every caller
	// (editor, future tool, served mode) goes through.
	a := graphview.New(mgr)
	ctx := context.Background()

	id := "invalid_direct"
	if err := a.SaveGraph(ctx, graphview.GraphSpec{ID: id, YAML: sprintfInvalid(id)}); err == nil {
		t.Fatal("direct SaveGraph call accepted an invalid graph")
	}
}

// TestListLibrary_MarksBackDoorGraphInvalid is AC-003: write an invalid
// graph straight into the library directory with os.WriteFile, bypassing
// every RPC — the §1.2 back door, reproduced (kenaz__bash / write_file
// can already reach <DataDir>/agent_graph/library today; this mission
// does not close that path, FR-004 only blunts it). listLibrary must
// mark the entry invalid, and startRun must refuse it with the
// validator's issues rather than a bare wrapped error.
//
// Mutation (spec AC-003): revert listLibrary to listing anything that
// parses. This test's Invalid assertion must fail.
func TestListLibrary_MarksBackDoorGraphInvalid(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	mgr, err := graphview.NewManager(graphview.WithDataDir(dir))
	if err != nil {
		t.Fatalf("NewManager: %v", err)
	}
	a := graphview.New(mgr)
	ctx := context.Background()

	id := "backdoor_invalid"
	libDir := filepath.Join(dir, "agent_graph", "library")
	if err := os.MkdirAll(libDir, 0o755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	// The back door: os.WriteFile straight to the library directory,
	// never through Graph_SaveGraph / saveGraph's new Validate call.
	full := filepath.Join(libDir, id+".yaml")
	if err := os.WriteFile(full, []byte(sprintfInvalid(id)), 0o644); err != nil {
		t.Fatalf("WriteFile (back door): %v", err)
	}

	rows, err := a.ListGraphs(ctx, "user")
	if err != nil {
		t.Fatalf("ListGraphs: %v", err)
	}
	var found graphview.GraphInfo
	var ok bool
	for _, r := range rows {
		if r.ID == id {
			found, ok = r, true
			break
		}
	}
	if !ok {
		t.Fatalf("back-door graph %q not listed at all; rows=%+v", id, rows)
	}
	if !found.Invalid {
		t.Errorf("back-door graph %q not marked Invalid; row=%+v", id, found)
	}
	if found.InvalidReason == "" {
		t.Errorf("back-door graph %q has no InvalidReason", id)
	}

	// startRun refuses with the validator's issues, not a bare wrapped
	// error string.
	_, err = a.StartRun(ctx, graphview.StartRunRequest{GraphID: id})
	if err == nil {
		t.Fatal("StartRun succeeded on a back-door invalid graph")
	}
	var verr *graphview.ValidationFailedError
	if !errors.As(err, &verr) {
		t.Fatalf("StartRun error is not *ValidationFailedError: %v (%T)", err, err)
	}
	if len(verr.Issues) == 0 {
		t.Error("StartRun's ValidationFailedError carries no issues")
	}
}

// TestValidGraph_StillSavesListsLoadsRuns is the "without this the unit
// is satisfiable by refusing everything" guard the spec calls out
// explicitly. A perfectly ordinary valid graph must be unaffected by
// UNIT-2's new validate-before-persist step.
func TestValidGraph_StillSavesListsLoadsRuns(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	mgr, err := graphview.NewManager(graphview.WithDataDir(dir))
	if err != nil {
		t.Fatalf("NewManager: %v", err)
	}
	a := graphview.New(mgr)
	ctx := context.Background()

	id := "valid_still_works"
	yaml := validGraphYAML(id)
	if err := a.SaveGraph(ctx, graphview.GraphSpec{ID: id, YAML: yaml}); err != nil {
		t.Fatalf("SaveGraph(valid): %v", err)
	}
	rows, err := a.ListGraphs(ctx, "user")
	if err != nil {
		t.Fatalf("ListGraphs: %v", err)
	}
	var found bool
	for _, r := range rows {
		if r.ID == id {
			found = true
			if r.Invalid {
				t.Errorf("valid graph marked Invalid: %+v", r)
			}
		}
	}
	if !found {
		t.Fatalf("valid graph %q not listed", id)
	}
	spec, err := a.LoadGraph(ctx, id)
	if err != nil {
		t.Fatalf("LoadGraph: %v", err)
	}
	if !strings.Contains(spec.YAML, id) {
		t.Errorf("loaded YAML missing id %q", id)
	}
	if _, err := a.StartRun(ctx, graphview.StartRunRequest{GraphID: id}); err != nil {
		t.Fatalf("StartRun(valid): %v", err)
	}
}

// sprintfInvalid returns a graph that parses cleanly (LoadYAML succeeds)
// but fails coreag.Validate: its sole entrypoint names a node id
// ("missing") that no node declares. Same shape TestValidate's "bad"
// fixture uses in impl_test.go, reused here so a validator behaviour
// change breaks both tests identically rather than drifting apart.
func sprintfInvalid(id string) string {
	return "spec_version: \"1\"\nid: " + id + "\nentrypoints: [missing]\nnodes:\n  - id: a\n    kind: plan\n    attrs:\n      verbosity: standard\n"
}
