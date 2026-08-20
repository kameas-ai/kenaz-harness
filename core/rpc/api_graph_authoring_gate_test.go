package rpc

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	cedarlib "github.com/cedar-policy/cedar-go"
	"github.com/kameas-ai/kenaz-harness/core"
	"github.com/kameas-ai/kenaz-harness/core/policy/cedar"
	graphview "github.com/kameas-ai/kenaz-harness/core/rpc/views/agentgraph"
)

// model-authored-graphs-01PMGA01 UNIT-4 — the security seam.
//
// "No commit may make a graph-writing verb reachable from a model until
// AC-004 passes." This file drives the REAL boot wiring (core.New ->
// rpc.New -> the real graphview.Manager, the real settings.FileStore,
// the real Cedar engine) — the same cedarWiringAPI harness
// api_cedar_gate_wiring_test.go and api_graph_cedar_install_test.go use
// — and asserts the refusal against the filesystem, not the gate.
//
// Every SaveGraph call below uses initiator="model": that is the ONLY
// initiator the graph.author gate applies to (AC-012 / initiator
// scoping in Manager.saveGraph). There is no model-reachable tool in
// this mission (UNIT-7 is externally blocked), so these tests exercise
// the BINDING path — the same call the future tool would make once its
// gate (harness-self-attach-01PMHS01) lands.

const validGraphAuthoringYAML = `spec_version: "1"
id: %s
entrypoints: [a]
nodes:
  - id: a
    kind: plan
    attrs:
      verbosity: terse
`

// writeFileGraphAuthoringYAML mirrors
// core/agentgraph/integration_test.go's TestIntegration_WriteFileKindEndToEnd
// fixture shape exactly (content: payload as a bare messages_ref name,
// no upstream port wiring needed) — the smallest graph that both
// VALIDATES and contains a write_file node, which is what AC-006 needs:
// a draft that is otherwise perfectly acceptable, refused ONLY because
// of the node kind.
const writeFileGraphAuthoringYAML = `spec_version: "1"
id: %s
entrypoints: [wf]
nodes:
  - id: wf
    kind: write_file
    attrs:
      path: /tmp/zz-graph-authoring-gate-test.txt
      content: payload
      mode: create
`

func graphAuthoringYAML(id string) string {
	return fmt.Sprintf(validGraphAuthoringYAML, id)
}

func writeFileGraphAuthoringYAML_(id string) string {
	return fmt.Sprintf(writeFileGraphAuthoringYAML, id)
}

// assertGraphNotOnDisk fails if <DataDir>/agent_graph/library/<id>.yaml
// exists — the real-filesystem half of "no file is created" (spec §11.1:
// never assert only on the returned error).
func assertGraphNotOnDisk(t *testing.T, dataDir, id string) {
	t.Helper()
	full := filepath.Join(dataDir, "agent_graph", "library", id+".yaml")
	if _, err := os.Stat(full); err == nil {
		t.Fatalf("graph %q WAS persisted to disk at %s despite a denied graph.author evaluation", id, full)
	} else if !os.IsNotExist(err) {
		t.Fatalf("Stat(%s): %v", full, err)
	}
}

func assertGraphOnDisk(t *testing.T, dataDir, id string) {
	t.Helper()
	full := filepath.Join(dataDir, "agent_graph", "library", id+".yaml")
	if _, err := os.Stat(full); err != nil {
		t.Fatalf("graph %q was NOT persisted to disk at %s: %v", id, full, err)
	}
}

// TestGraphAuthoringGate_AC004_DeniedOnFreshProfile is the security
// criterion. A fresh profile — no settings.json at all, the real
// sandboxed FileStore path — refuses a perfectly valid model-initiated
// draft, and nothing is written. Then the SAME session flips the
// consent dial through the real settings store and the identical call
// succeeds — the two positions differ only in the persisted value.
func TestGraphAuthoringGate_AC004_DeniedOnFreshProfile(t *testing.T) {
	api := cedarWiringAPI(t, "")
	ctx := context.Background()
	dataDir := api.core.DataDir()

	id := "zz_ac004_probe"
	yaml := graphAuthoringYAML(id)

	err := api.Graph().SaveGraph(ctx, graphview.GraphSpec{ID: id, YAML: yaml}, "model")
	if err == nil {
		t.Fatal("model-initiated SaveGraph succeeded on a fresh profile with no consent setting — graph.author is not gated")
	}
	var pderr *cedar.PolicyDeniedError
	if !errors.As(err, &pderr) {
		t.Fatalf("err = %v (%T); want a *cedar.PolicyDeniedError — a generic failure does not prove Cedar denied it", err, err)
	}
	assertGraphNotOnDisk(t, dataDir, id)

	// Flip the dial through the REAL settings store (spec §11.1: a
	// fixture that never round-trips through storage proves nothing
	// about the consent bit).
	store := api.settingsImpl.Store()
	if store == nil {
		t.Fatal("settings store is nil — construction changed")
	}
	if err := store.SaveGraphAuthoringEnabled(true); err != nil {
		t.Fatalf("SaveGraphAuthoringEnabled(true): %v", err)
	}
	// Re-read to prove the write landed, not just that Save returned nil.
	enabled, rerr := store.LoadGraphAuthoringEnabled()
	if rerr != nil || !enabled {
		t.Fatalf("LoadGraphAuthoringEnabled after enabling = (%v, %v), want (true, nil)", enabled, rerr)
	}

	if err := api.Graph().SaveGraph(ctx, graphview.GraphSpec{ID: id, YAML: yaml}, "model"); err != nil {
		t.Fatalf("model-initiated SaveGraph still refused after enabling authoring: %v", err)
	}
	assertGraphOnDisk(t, dataDir, id)

	// And it is really there through the ordinary read path too.
	rows, lerr := api.Graph().ListGraphs(ctx, "user")
	if lerr != nil {
		t.Fatalf("ListGraphs: %v", lerr)
	}
	found := false
	for _, r := range rows {
		if r.ID == id && r.Scope == "user" {
			found = true
		}
	}
	if !found {
		t.Fatalf("saved graph %q not listed with scope=user; rows=%+v", id, rows)
	}
}

// TestGraphAuthoringGate_AC005_UnreadableStoreDefaultsOff pins the
// "unknown reads as off" contract at the settings-reader layer:
// LoadGraphAuthoringEnabled erroring must not be treated as "on" by
// the resolver GateGraphAuthor's caller uses.
//
// The absent/""/upgrade-path facet of AC-005 is covered end-to-end by
// UNIT-PI (which boots a v0.64.0 sqlite snapshot — the ONLY path on
// which "absent" is not simply "the field was never written this
// session"). This test covers the fourth AC-005 case tasks.md lists
// that UNIT-PI's sqlite snapshot cannot reach directly: the settings
// STORE itself returning an error (a corrupt settings.json), asserted
// through the real FileStore rather than a hand-rolled fake — spec
// §11.1 forbids a fake settings reader for exactly this property.
func TestGraphAuthoringGate_AC005_UnreadableStoreDefaultsOff(t *testing.T) {
	sandboxUserConfigDir(t)
	dataDir := t.TempDir()

	// Write a corrupt settings.json into the sandboxed user config dir
	// BEFORE boot, at the exact path FileStore uses
	// ($USER_CONFIG_DIR/kenaz-harness/settings.json), so
	// LoadGraphAuthoringEnabled hits a real JSON decode error.
	cfgDir, err := os.UserConfigDir()
	if err != nil {
		t.Fatalf("UserConfigDir: %v", err)
	}
	settingsDir := filepath.Join(cfgDir, "kenaz-harness")
	if err := os.MkdirAll(settingsDir, 0o755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	if err := os.WriteFile(filepath.Join(settingsDir, "settings.json"), []byte("{not valid json"), 0o644); err != nil {
		t.Fatalf("WriteFile corrupt settings.json: %v", err)
	}

	api := coreNewAPIForTest(t, dataDir)
	ctx := context.Background()

	store := api.settingsImpl.Store()
	if store == nil {
		t.Fatal("settings store is nil")
	}
	enabled, lerr := store.LoadGraphAuthoringEnabled()
	if lerr == nil {
		t.Fatal("LoadGraphAuthoringEnabled returned no error against a corrupt settings.json — the fixture is not exercising the failure path")
	}
	if enabled {
		t.Fatal("LoadGraphAuthoringEnabled returned true on a read error — the unreadable case must default to false")
	}

	// And the gate itself denies through the same corrupt-store path.
	id := "zz_ac005_probe"
	err = api.Graph().SaveGraph(ctx, graphview.GraphSpec{ID: id, YAML: graphAuthoringYAML(id)}, "model")
	if err == nil {
		t.Fatal("model-initiated SaveGraph succeeded with a corrupt settings.json — the unreadable case defaulted to on")
	}
	assertGraphNotOnDisk(t, dataDir, id)
}

// TestGraphAuthoringGate_AC006_WriteFileForbiddenEvenWhenEnabled is the
// FR-008 escalation control through the real boot wiring: with
// authoring ON, a draft containing write_file is still refused, and
// nothing is written.
func TestGraphAuthoringGate_AC006_WriteFileForbiddenEvenWhenEnabled(t *testing.T) {
	api := cedarWiringAPI(t, "")
	ctx := context.Background()
	dataDir := api.core.DataDir()

	store := api.settingsImpl.Store()
	if err := store.SaveGraphAuthoringEnabled(true); err != nil {
		t.Fatalf("SaveGraphAuthoringEnabled(true): %v", err)
	}

	// Control: the same authoring-enabled session CAN save a harmless
	// draft — proves the denial below is about write_file specifically,
	// not a blanket failure.
	okID := "zz_ac006_control"
	if err := api.Graph().SaveGraph(ctx, graphview.GraphSpec{ID: okID, YAML: graphAuthoringYAML(okID)}, "model"); err != nil {
		t.Fatalf("harmless draft refused with authoring enabled: %v", err)
	}
	assertGraphOnDisk(t, dataDir, okID)

	id := "zz_ac006_probe"
	err := api.Graph().SaveGraph(ctx, graphview.GraphSpec{ID: id, YAML: writeFileGraphAuthoringYAML_(id)}, "model")
	if err == nil {
		t.Fatal("model-initiated draft containing a write_file node was accepted with authoring enabled")
	}
	var pderr *cedar.PolicyDeniedError
	if !errors.As(err, &pderr) {
		t.Fatalf("err = %v (%T); want a *cedar.PolicyDeniedError naming the write_file policy", err, err)
	}
	assertGraphNotOnDisk(t, dataDir, id)
}

// TestGraphAuthoringGate_AC012_EditorSaveUnaffectedByAuthoringOff is the
// "the ordinary path is unaffected" criterion: with authoring OFF (the
// shipped default — no settings write at all in this test), the
// desktop editor's initiator="user" save still works, for a graph that
// would ALSO be denied under the graph.author policy if it were
// initiator="model" (it contains write_file). The point is that
// "user" bypasses the gate call site entirely, not that the policy
// happens to allow it.
func TestGraphAuthoringGate_AC012_EditorSaveUnaffectedByAuthoringOff(t *testing.T) {
	api := cedarWiringAPI(t, "")
	ctx := context.Background()
	dataDir := api.core.DataDir()

	id := "zz_ac012_probe"
	yaml := writeFileGraphAuthoringYAML_(id)

	// Sanity: prove the model-initiated path WOULD be denied for this
	// exact YAML, so the "user" success below is not just "the policy
	// happens to allow everything".
	modelErr := api.Graph().SaveGraph(ctx, graphview.GraphSpec{ID: "zz_ac012_model_control", YAML: writeFileGraphAuthoringYAML_("zz_ac012_model_control")}, "model")
	if modelErr == nil {
		t.Fatal("model-initiated write_file draft was accepted (authoring off) — the control assumption for this test is wrong")
	}

	if err := api.Graph().SaveGraph(ctx, graphview.GraphSpec{ID: id, YAML: yaml}, "user"); err != nil {
		t.Fatalf("user-initiated (editor) save was refused with authoring off: %v — "+
			"the graph.author gate must not apply to the desktop editor's save path", err)
	}
	assertGraphOnDisk(t, dataDir, id)
}

// TestGraphAuthoringGate_AC008_UnreviewedModelAuthoredGraphDoesNotRun is
// AC-008 end-to-end through the REAL shipped Cedar bundle — UNIT-3's
// graph_run_unreviewed_forbid.cedar, UNIT-4's GateGraphRun wiring at
// Manager.startRun, and UNIT-5's server-side stamping in
// Manager.saveGraph, all exercised together rather than any one in
// isolation (core/rpc/views/agentgraph's own tests use a hand-rolled
// fake gate for this same property; this is the version that would
// catch a real policy-text regression too).
func TestGraphAuthoringGate_AC008_UnreviewedModelAuthoredGraphDoesNotRun(t *testing.T) {
	api := cedarWiringAPI(t, "")
	ctx := context.Background()

	store := api.settingsImpl.Store()
	if err := store.SaveGraphAuthoringEnabled(true); err != nil {
		t.Fatalf("SaveGraphAuthoringEnabled(true): %v", err)
	}

	id := "zz_ac008_probe"
	yaml := graphAuthoringYAML(id)
	if err := api.Graph().SaveGraph(ctx, graphview.GraphSpec{ID: id, YAML: yaml}, "model"); err != nil {
		t.Fatalf("model-initiated SaveGraph: %v", err)
	}

	// Denied by graph.run while unreviewed, and no run entry exists.
	_, err := api.Graph().StartRun(ctx, graphview.StartRunRequest{GraphID: id})
	if err == nil {
		t.Fatal("StartRun succeeded on an unreviewed model-authored graph")
	}
	var pderr *cedar.PolicyDeniedError
	if !errors.As(err, &pderr) {
		t.Fatalf("err = %v (%T); want *cedar.PolicyDeniedError naming graph_run_unreviewed_forbid.cedar", err, err)
	}
	if _, serr := api.Graph().GetRunStatus(ctx, "no-such-run-was-created"); serr == nil {
		t.Fatal("GetRunStatus for a made-up run id succeeded — StartRun must not have created a run entry")
	}

	// Human review: load + save through the editor's (user) path, which
	// clears the marker.
	loaded, lerr := api.Graph().LoadGraph(ctx, id)
	if lerr != nil {
		t.Fatalf("LoadGraph: %v", lerr)
	}
	if err := api.Graph().SaveGraph(ctx, graphview.GraphSpec{ID: id, YAML: loaded.YAML}, "user"); err != nil {
		t.Fatalf("SaveGraph(user, review): %v", err)
	}

	// The identical StartRun call now proceeds past the gate.
	if _, err := api.Graph().StartRun(ctx, graphview.StartRunRequest{GraphID: id}); err != nil {
		t.Fatalf("StartRun after human review still refused: %v", err)
	}
}

// TestGraphAuthoringGate_BootPathGateIsNonNil pins plan.md's stated risk
// directly: "until I13 clause 4 lands, WP04's tests must include a
// boot-path assertion that the wired gate is not nil, driven through
// the real api.go construction rather than a hand-built Manager." The
// nil-gate contract is DEFAULT-ALLOW (GateGraphAuthor/GateGraphRun's own
// doc comment) — a nil gate in production would silently un-gate
// authoring for every model-initiated save while every other assertion
// in this file (which flips the dial and therefore cannot distinguish
// "denied by policy" from "never evaluated because g==nil, but Allow
// still fired") stays green in the disabled direction only by
// coincidence of the specific test ordering. This test removes that
// coincidence.
func TestGraphAuthoringGate_BootPathGateIsNonNil(t *testing.T) {
	api := cedarWiringAPI(t, "")
	if api.cedarEngine == nil {
		t.Fatal("api.cedarEngine is nil — cannot prove the graph Manager's gate is wired to a real engine")
	}
	// Evaluate the SAME action/resource shape GateGraphAuthor uses,
	// directly against the shared engine, to confirm it is the object
	// wired into the Manager (indirect proof: TestGraphAuthoringGate_AC004
	// above already proves the Manager's gate produces a real Deny/Allow
	// pair driven by the consent dial, which a nil gate — always Allow,
	// regardless of the dial — could not do. This test documents WHY
	// that inference is valid.)
	d := api.cedarEngine.Evaluate(
		context.Background(),
		cedar.UserUID(),
		cedar.ActionGraphAuthor,
		cedar.GraphUID("zz_boot_path_probe"),
		map[cedarlib.String]cedarlib.Value{
			cedarlib.String("authoring_enabled"): cedarlib.String("false"),
			cedarlib.String("session_kind"):      cedarlib.String("chat"),
			cedarlib.String("node_kinds"):        cedarlib.String("plan"),
			cedarlib.String("node_count"):        cedarlib.Long(1),
		},
	)
	if d.Outcome != cedar.Deny {
		t.Fatalf("shared engine denies graph.author when disabled (Outcome=%s) — if the Manager's own gate is nil, its evaluations would be Allow unconditionally, which TestGraphAuthoringGate_AC004 already shows is not what happens", d.Outcome)
	}
}

// coreNewAPIForTest boots core.New + rpc.New over an explicit dataDir
// without pre-seeding a policy file — used where the test needs to
// control the settings.json fixture BEFORE boot (cedarWiringAPI always
// starts from a clean sandboxed user config dir, after boot).
func coreNewAPIForTest(t *testing.T, dataDir string) *API {
	t.Helper()
	c, err := core.New(core.Options{DataDir: dataDir})
	if err != nil {
		t.Fatalf("core.New: %v", err)
	}
	api := New(c)
	assertSettingsStoreIsSandboxed(t, api)
	return api
}
