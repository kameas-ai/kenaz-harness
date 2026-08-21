package rpc

// Tests for subagent-control-and-background-tasks-01PMZB11 UNIT-3 (the
// background-task writer + real output capture). Drives the tool through
// the EXACT production wiring shape (registerBuiltinTools with a real
// *coretasks.Registry), never a hand-injected fake spawner — per spec.md
// §9 rule 3 / tasks.md UNIT-3 AC-03: "The fixture must not inject its own
// spawner. A test that supplies a fake proves the bash tool calls a
// function; it does not prove production assigns one, and that is the
// entire defect."

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	coretasks "github.com/kameas-ai/kenaz-harness/core/tasks"
	"github.com/kameas-ai/kenaz-harness/core/toolloop"
	corebash "github.com/kameas-ai/kenaz-harness/core/tools/bash"
)

// newProductionShapedBashRegistry builds a *toolloop.BuiltinRegistry the
// same way core/rpc/api.go does in production: registerBuiltinTools with
// a real *coretasks.Registry (no store — in-memory is sufficient to
// prove the wiring; UNIT-2's persistence path is covered separately by
// TestUpgradePath).
func newProductionShapedBashRegistry(t *testing.T) (*toolloop.BuiltinRegistry, *coretasks.Registry) {
	t.Helper()
	taskReg := coretasks.NewRegistry(coretasks.Options{})

	registry := toolloop.NewBuiltinRegistry()
	registerBuiltinTools(
		nil, // core
		registry,
		nil, // bashStore
		nil, // artifactsMgr
		nil, // store
		nil, // cedarEngine
		nil, // promptRegistry
		nil, // elicitAPI
		nil, // slashDispatch
		nil, // exposureIdx
		nil, // budget
		nil, // posture
		taskReg,
	)
	return registry, taskReg
}

// TestBashBackgroundMode_ProductionWiring_RegistersATaskRow is AC-03: a
// live kenaz__bash call with run_in_background:true, dispatched through
// the registry registerBuiltinTools actually produces, must appear via
// Tasks_List.
//
// FALSIFIABILITY (tasks.md UNIT-3 AC-03): deleting the BackgroundSpawn:
// assignment in builtins_wiring.go makes this fail — ran by hand during
// development (temporarily commented out the assignment; this test and
// TestBashBackgroundMode_ProductionWiring_CapturesRealOutput both failed;
// restored the assignment; both passed again). See the mission report
// for the pasted output.
func TestBashBackgroundMode_ProductionWiring_RegistersATaskRow(t *testing.T) {
	registry, taskReg := newProductionShapedBashRegistry(t)
	tool, ok := registry.Lookup(corebash.Name)
	if !ok {
		t.Fatal("kenaz__bash not registered")
	}

	argsJSON, _ := json.Marshal(map[string]any{
		"command":           "echo unit3_capture_probe",
		"run_in_background": true,
		"description":       "UNIT-3 capture probe",
	})
	ctx := context.Background()
	result, err := tool.Call(ctx, argsJSON)
	if err != nil {
		t.Fatalf("Call: %v", err)
	}
	var out map[string]any
	if err := json.Unmarshal(result, &out); err != nil {
		t.Fatalf("unmarshal result: %v", err)
	}
	taskID, _ := out["task_id"].(string)
	if taskID == "" {
		t.Fatalf("no task_id in background-mode result: %v", out)
	}

	rows := taskReg.List()
	var found bool
	for _, r := range rows {
		if r.ID == taskID {
			found = true
			if r.Kind != coretasks.KindBash {
				t.Errorf("task kind = %q, want %q", r.Kind, coretasks.KindBash)
			}
		}
	}
	if !found {
		t.Errorf("Registry.List() (production wiring) does not contain task %s — BackgroundSpawn is not reaching a real registry", taskID)
	}
}

// TestBashBackgroundMode_ProductionWiring_CapturesRealOutput is AC-04:
// the same task's Tail returns the command's actual stdout lines,
// through the real writer-attachment path (id allocated BEFORE
// cmd.Start(), not after).
//
// FALSIFIABILITY (tasks.md UNIT-3 AC-04): reverting the cmd.Stdout
// attachment, OR moving the id allocation back after cmd.Start(), each
// make this fail with zero lines. Both mutations were run by hand
// against this test; see the mission report.
func TestBashBackgroundMode_ProductionWiring_CapturesRealOutput(t *testing.T) {
	registry, taskReg := newProductionShapedBashRegistry(t)
	tool, ok := registry.Lookup(corebash.Name)
	if !ok {
		t.Fatal("kenaz__bash not registered")
	}

	const marker = "unit3_output_capture_marker_9f3a"
	argsJSON, _ := json.Marshal(map[string]any{
		"command":           "echo " + marker,
		"run_in_background": true,
	})
	ctx := context.Background()
	result, err := tool.Call(ctx, argsJSON)
	if err != nil {
		t.Fatalf("Call: %v", err)
	}
	var out map[string]any
	_ = json.Unmarshal(result, &out)
	taskID, _ := out["task_id"].(string)
	if taskID == "" {
		t.Fatal("no task_id")
	}

	deadline := time.Now().Add(3 * time.Second)
	var lines []coretasks.Line
	for time.Now().Before(deadline) {
		lines, _, _ = taskReg.Tail(taskID, 0)
		if len(lines) > 0 {
			break
		}
		time.Sleep(25 * time.Millisecond)
	}
	if len(lines) == 0 {
		t.Fatal("Tail returned zero lines — output capture is not attached (the exact defect i11-unregistered-builtin-tools.txt predicted)")
	}
	var sawMarker bool
	for _, ln := range lines {
		if ln.Stream != "stdout" {
			continue
		}
		if ln.Text == marker {
			sawMarker = true
		}
	}
	if !sawMarker {
		t.Errorf("stdout lines %+v do not contain the echoed marker %q", lines, marker)
	}
}

// TestBashSyncMode_ProductionWiring_NoTaskRow asserts the negative half
// UNIT-3's own text requires: a synchronous kenaz__bash call still
// returns inline output and creates NO task row — "without this the
// unit is satisfiable by routing everything through the background
// path."
func TestBashSyncMode_ProductionWiring_NoTaskRow(t *testing.T) {
	registry, taskReg := newProductionShapedBashRegistry(t)
	tool, ok := registry.Lookup(corebash.Name)
	if !ok {
		t.Fatal("kenaz__bash not registered")
	}

	argsJSON, _ := json.Marshal(map[string]any{
		"command": "echo sync_only",
	})
	ctx := context.Background()
	result, err := tool.Call(ctx, argsJSON)
	if err != nil {
		t.Fatalf("Call: %v", err)
	}
	var out map[string]any
	_ = json.Unmarshal(result, &out)
	if out["task_id"] != nil {
		t.Errorf("synchronous call produced a task_id: %v", out["task_id"])
	}
	if out["stdout"] == nil {
		t.Error("synchronous call did not return inline stdout")
	}

	if rows := taskReg.List(); len(rows) != 0 {
		t.Errorf("Registry.List() after a SYNCHRONOUS call = %d rows, want 0", len(rows))
	}
}
