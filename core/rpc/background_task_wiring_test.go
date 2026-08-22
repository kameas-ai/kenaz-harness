package rpc

// Tests for subagent-control-and-background-tasks-01PMZB11 UNIT-3 (the
// background-task writer + real output capture) and UNIT-4 (the
// background_task_complete hook firer). Both drive the tool through the
// EXACT production wiring shape (registerBuiltinTools with a real
// *coretasks.Registry), never a hand-injected fake spawner — per spec.md
// §9 rule 3 / tasks.md UNIT-3 AC-03: "The fixture must not inject its own
// spawner. A test that supplies a fake proves the bash tool calls a
// function; it does not prove production assigns one, and that is the
// entire defect."

import (
	"context"
	"encoding/json"
	"sync"
	"testing"
	"time"

	"github.com/kameas-ai/kenaz-harness/core/hooks"
	coretasks "github.com/kameas-ai/kenaz-harness/core/tasks"
	"github.com/kameas-ai/kenaz-harness/core/toolloop"
	corebash "github.com/kameas-ai/kenaz-harness/core/tools/bash"
	coremonitor "github.com/kameas-ai/kenaz-harness/core/tools/monitor"
)

// fakeHookFirer is a race-safe recorder for hook firing — it stands in
// for the *hooks.Runner UNIT-4 wires through SetHookFirer in production
// (core/rpc/api.go), letting these tests assert firing behaviour
// without booting the full hooks stack. CLAUDE.md "Race-safe test
// fakes": End() fires the hook from a goroutine (`go r.hookFirer(...)`),
// so every read here goes through snapshot().
type fakeHookFirer struct {
	mu    sync.Mutex
	fired []coretasks.BackgroundTaskCompletePayload
}

func (f *fakeHookFirer) fire(_ context.Context, payload coretasks.BackgroundTaskCompletePayload) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.fired = append(f.fired, payload)
}

func (f *fakeHookFirer) snapshot() []coretasks.BackgroundTaskCompletePayload {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make([]coretasks.BackgroundTaskCompletePayload, len(f.fired))
	copy(out, f.fired)
	return out
}

// newProductionShapedBashRegistry builds a *toolloop.BuiltinRegistry the
// same way core/rpc/api.go does in production: registerBuiltinTools with
// a real *coretasks.Registry (no store — in-memory is sufficient to
// prove the wiring; UNIT-2's persistence path is covered separately by
// TestUpgradePath), and a hook firer wired the same way UNIT-4 wires
// a.hookRunner in api.go (via SetHookFirer, post-construction).
func newProductionShapedBashRegistry(t *testing.T) (*toolloop.BuiltinRegistry, *coretasks.Registry, *fakeHookFirer) {
	t.Helper()
	firer := &fakeHookFirer{}
	taskReg := coretasks.NewRegistry(coretasks.Options{})
	taskReg.SetHookFirer(firer.fire)

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
	return registry, taskReg, firer
}

// TestMonitorTool_ProductionWiring_DrainReturnsRealLines is AC-06
// (FR-005): kenaz__monitor, registered through the exact production
// wiring (registerBuiltinTools with a real taskReg — not a hand-built
// monitor.Tool over a fake registry), returns non-empty lines for a
// live task's drain mode. Mutation named in tasks.md UNIT-5 ("revert
// UNIT-3's cmd.Stdout attachment. Must fail.") is exercised
// transitively: TestBashBackgroundMode_ProductionWiring_
// CapturesRealOutput's falsification (UNIT-3 commit) already proves
// that revert empties Registry.Tail, and kenaz__monitor's drain reads
// through the identical Tail call, so the same revert empties this
// too.
func TestMonitorTool_ProductionWiring_DrainReturnsRealLines(t *testing.T) {
	registry, _, _ := newProductionShapedBashRegistry(t)
	bashTool, ok := registry.Lookup(corebash.Name)
	if !ok {
		t.Fatal("kenaz__bash not registered")
	}
	monitorTool, ok := registry.Lookup(coremonitor.ToolName)
	if !ok {
		t.Fatal("kenaz__monitor not registered — AC-06 registration half failed")
	}

	const marker = "unit5_monitor_drain_marker_71ac"
	argsJSON, _ := json.Marshal(map[string]any{
		"command":           "echo " + marker,
		"run_in_background": true,
	})
	ctx := context.Background()
	result, err := bashTool.Call(ctx, argsJSON)
	if err != nil {
		t.Fatalf("bash Call: %v", err)
	}
	var out map[string]any
	_ = json.Unmarshal(result, &out)
	taskID, _ := out["task_id"].(string)
	if taskID == "" {
		t.Fatal("no task_id")
	}

	// Wait for the task to finish producing output, then drain.
	deadline := time.Now().Add(3 * time.Second)
	var monResult map[string]any
	for time.Now().Before(deadline) {
		drainArgs, _ := json.Marshal(map[string]any{"task_id": taskID, "mode": "drain"})
		res, err := monitorTool.Call(ctx, drainArgs)
		if err != nil {
			t.Fatalf("monitor Call: %v", err)
		}
		_ = json.Unmarshal(res, &monResult)
		lines, _ := monResult["lines"].([]any)
		if len(lines) > 0 {
			break
		}
		time.Sleep(25 * time.Millisecond)
	}
	lines, _ := monResult["lines"].([]any)
	if len(lines) == 0 {
		t.Fatalf("kenaz__monitor drain returned zero lines (result=%+v) — registered over an empty ring buffer, the exact defect i11 protected against", monResult)
	}
	var sawMarker bool
	for _, raw := range lines {
		m, _ := raw.(map[string]any)
		if m["stream"] == "stdout" && m["text"] == marker {
			sawMarker = true
		}
	}
	if !sawMarker {
		t.Errorf("drained lines %+v do not contain the echoed marker %q", lines, marker)
	}
}

// TestMonitorTool_ProductionWiring_WatchReturnsOnExit is AC-06's watch-mode
// half: watch with until.exit blocks until the task terminates and
// returns lines produced before exit.
func TestMonitorTool_ProductionWiring_WatchReturnsOnExit(t *testing.T) {
	registry, _, _ := newProductionShapedBashRegistry(t)
	bashTool, ok := registry.Lookup(corebash.Name)
	if !ok {
		t.Fatal("kenaz__bash not registered")
	}
	monitorTool, ok := registry.Lookup(coremonitor.ToolName)
	if !ok {
		t.Fatal("kenaz__monitor not registered")
	}

	const marker = "unit5_monitor_watch_marker_22be"
	argsJSON, _ := json.Marshal(map[string]any{
		"command":           "echo " + marker,
		"run_in_background": true,
	})
	ctx := context.Background()
	result, err := bashTool.Call(ctx, argsJSON)
	if err != nil {
		t.Fatalf("bash Call: %v", err)
	}
	var out map[string]any
	_ = json.Unmarshal(result, &out)
	taskID, _ := out["task_id"].(string)
	if taskID == "" {
		t.Fatal("no task_id")
	}

	watchArgs, _ := json.Marshal(map[string]any{
		"task_id": taskID,
		"mode":    "watch",
		"until":   map[string]any{"exit": true, "timeout_s": 5},
	})
	watchCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	res, err := monitorTool.Call(watchCtx, watchArgs)
	if err != nil {
		t.Fatalf("monitor watch Call: %v", err)
	}
	var monResult map[string]any
	_ = json.Unmarshal(res, &monResult)
	if monResult["matched"] != "exit" {
		t.Errorf("matched = %v, want %q", monResult["matched"], "exit")
	}
	lines, _ := monResult["lines"].([]any)
	var sawMarker bool
	for _, raw := range lines {
		m, _ := raw.(map[string]any)
		if m["stream"] == "stdout" && m["text"] == marker {
			sawMarker = true
		}
	}
	if !sawMarker {
		t.Errorf("watch-mode lines %+v (result=%+v) do not contain the echoed marker %q", lines, monResult, marker)
	}
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
	registry, taskReg, _ := newProductionShapedBashRegistry(t)
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
	registry, taskReg, _ := newProductionShapedBashRegistry(t)
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
	registry, taskReg, _ := newProductionShapedBashRegistry(t)
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

// TestBackgroundTaskComplete_FiresOnceOnTerminal_ZeroWhileRunning is
// AC-05 (FR-004): a background task that reaches a terminal state fires
// background_task_complete exactly once, with TaskID and ExitCode
// populated — and a task that is still running fires it zero times
// (tasks.md UNIT-4: "an implementation that fires on every state change
// passes" without this negative half).
//
// FALSIFIABILITY: this test drives taskReg.SetHookFirer the same way
// UNIT-4 wires it in production; unassigning HookFirer (nil, never
// called) makes the hook never fire, and the "want exactly 1" assertion
// below fails with 0. See the mission report for the ran output.
func TestBackgroundTaskComplete_FiresOnceOnTerminal_ZeroWhileRunning(t *testing.T) {
	registry, _, firer := newProductionShapedBashRegistry(t)
	tool, ok := registry.Lookup(corebash.Name)
	if !ok {
		t.Fatal("kenaz__bash not registered")
	}

	// "sleep" is not in bash's DefaultAllowlist (no CedarEngine wired in
	// this test); python3 is, and this test's registerBuiltinTools call
	// has no way to pass a custom allowlist, so python3 stands in for a
	// slow command.
	//
	// -B is load-bearing, not cosmetic: without it CPython writes
	// __pycache__ bytecode under $HOME/Library/Caches/com.apple.python,
	// which is OUTSIDE this test's t.TempDir(). That made
	// check-tests-are-hermetic.sh fail with a sentinel-tree diff listing
	// nothing but Xcode .pyc files — a real escape, just an unusually
	// boring one. Any future test that shells out to an interpreter
	// needs the same treatment.
	argsJSON, _ := json.Marshal(map[string]any{
		"command":           `python3 -B -c "import time; time.sleep(0.3); print('done_marker')"`,
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

	// While the command is still sleeping, the hook must not have fired.
	time.Sleep(50 * time.Millisecond)
	if got := firer.snapshot(); len(got) != 0 {
		t.Errorf("hook fired %d times while the task is still running, want 0: %+v", len(got), got)
	}

	deadline := time.Now().Add(3 * time.Second)
	var fired []coretasks.BackgroundTaskCompletePayload
	for time.Now().Before(deadline) {
		fired = firer.snapshot()
		if len(fired) > 0 {
			break
		}
		time.Sleep(25 * time.Millisecond)
	}
	if len(fired) != 1 {
		t.Fatalf("hook fired %d times after task terminal, want exactly 1: %+v", len(fired), fired)
	}
	if fired[0].TaskID != taskID {
		t.Errorf("fired payload TaskID = %q, want %q", fired[0].TaskID, taskID)
	}
	if fired[0].ExitCode != 0 {
		t.Errorf("fired payload ExitCode = %d, want 0", fired[0].ExitCode)
	}
}

// TestBackgroundTaskComplete_RealHooksRunner_FiresTheSavedHook is UNIT-4's
// strongest available proof short of booting the full api.New() chassis
// (which requires a real DataDir + on-disk keychain + a large fraction
// of the stack — see TestNewGraphManagerWithDeps_LifecycleHooksReachEnv
// in graph_manager_lifecycle_hooks_test.go for the established pattern
// this test follows): a REAL *hooks.Runner backed by a REAL
// hooks.Registry with a REAL saved background_task_complete hook, wired
// to a REAL *coretasks.Registry through the EXACT one-line adapter
// core/rpc/api.go's New() installs via taskReg.SetHookFirer (the two
// closures are kept byte-for-shape identical on purpose — see the
// comment above the call in api.go). If the saved hook does not fire,
// or fires with the wrong TaskID/ExitCode, this fails.
//
// FALSIFIABILITY: commenting out the `if taskReg != nil && a.hookRunner
// != nil { taskReg.SetHookFirer(...) }` block in core/rpc/api.go (the
// actual production wiring, not this test's copy of it) would leave
// EventBackgroundTaskComplete declared-only again, exactly as it was
// before UNIT-4 — this test's copy of the adapter is what pins the
// shape so a reviewer changing api.go's version also has to change
// this one, or the two silently diverge.
func TestBackgroundTaskComplete_RealHooksRunner_FiresTheSavedHook(t *testing.T) {
	t.Parallel()

	type fired struct {
		mu      sync.Mutex
		outputs []hooks.BackgroundTaskCompleteEvent
	}
	f := &fired{}

	builtins := hooks.NewBuiltinRegistry()
	builtins.RegisterGenericFire("test-record-background-task-complete",
		func(_ context.Context, _ string, payload any, _ map[string]any) (hooks.HookOutput, error) {
			ev, _ := payload.(hooks.BackgroundTaskCompleteEvent)
			f.mu.Lock()
			f.outputs = append(f.outputs, ev)
			f.mu.Unlock()
			return hooks.HookOutput{}, nil
		},
		hooks.BuiltinDescriptor{
			ID:     "test-record-background-task-complete",
			Name:   "test-record-background-task-complete",
			Events: []string{hooks.EventBackgroundTaskComplete},
		},
	)
	registry, err := hooks.NewRegistry("") // in-memory
	if err != nil {
		t.Fatalf("hooks.NewRegistry: %v", err)
	}
	if err := registry.Add(hooks.Hook{
		ID:      "h-unit4-record",
		Name:    "record background_task_complete",
		Event:   hooks.EventBackgroundTaskComplete,
		Kind:    hooks.KindBuiltin,
		Enabled: true,
		Builtin: "test-record-background-task-complete",
	}); err != nil {
		t.Fatalf("registry.Add: %v", err)
	}
	hookRunnerForTasks := hooks.NewRunner(hooks.Config{Registry: registry, Builtins: builtins})

	// This block is the exact shape core/rpc/api.go's New() installs
	// after a.hookRunner is set — see the comment there.
	taskReg := coretasks.NewRegistry(coretasks.Options{})
	taskReg.SetHookFirer(func(ctx context.Context, payload coretasks.BackgroundTaskCompletePayload) {
		_, _ = hookRunnerForTasks.Fire(ctx, hooks.EventBackgroundTaskComplete, hooks.BackgroundTaskCompleteEvent{
			SessionID:  payload.OwnerSessionID,
			TaskID:     payload.TaskID,
			TaskKind:   payload.Kind,
			ExitCode:   payload.ExitCode,
			DurationMs: payload.DurationMs,
			StdoutTail: payload.StdoutTail,
			StderrTail: payload.StderrTail,
			Success:    payload.ExitCode == 0,
		})
	})

	ctx := context.Background()
	taskID, err := taskReg.Register(ctx, coretasks.RegisterOpts{
		Kind: coretasks.KindBash, OwnerSessionID: "sess-unit4", Cmd: "echo hi",
	})
	if err != nil {
		t.Fatalf("Register: %v", err)
	}
	if err := taskReg.End(ctx, taskID, 0); err != nil {
		t.Fatalf("End: %v", err)
	}

	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		f.mu.Lock()
		n := len(f.outputs)
		f.mu.Unlock()
		if n > 0 {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}

	f.mu.Lock()
	defer f.mu.Unlock()
	if len(f.outputs) != 1 {
		t.Fatalf("saved background_task_complete hook fired %d times through a REAL hooks.Runner, want 1", len(f.outputs))
	}
	got := f.outputs[0]
	if got.TaskID != taskID {
		t.Errorf("event.TaskID = %q, want %q", got.TaskID, taskID)
	}
	if got.SessionID != "sess-unit4" {
		t.Errorf("event.SessionID = %q, want %q", got.SessionID, "sess-unit4")
	}
	if !got.Success {
		t.Errorf("event.Success = false for exit code 0")
	}
}

// TestBackgroundTaskCompleteHookFirer_ProductionAdapter_ProducesEvent
// pins the shape of the adapter UNIT-4 wires in core/rpc/api.go: a
// coretasks.BackgroundTaskCompletePayload must translate to
// hooks.BackgroundTaskCompleteEvent field-for-field, or a hook script
// reading TaskID/ExitCode/StdoutTail/StderrTail sees the wrong values.
// This does not boot a real *hooks.Runner (that requires a full chassis
// — covered at the boot-log level by UNIT-1's execution #2 sibling
// checks); it pins the translation this file performs.
func TestBackgroundTaskCompleteHookFirer_ProductionAdapter_ProducesEvent(t *testing.T) {
	payload := coretasks.BackgroundTaskCompletePayload{
		TaskID:         "task-abc",
		Kind:           coretasks.KindBash,
		ExitCode:       7,
		DurationMs:     1234,
		StdoutTail:     "out",
		StderrTail:     "err",
		OwnerSessionID: "sess-1",
	}
	event := hooks.BackgroundTaskCompleteEvent{
		SessionID:  payload.OwnerSessionID,
		TaskID:     payload.TaskID,
		TaskKind:   payload.Kind,
		ExitCode:   payload.ExitCode,
		DurationMs: payload.DurationMs,
		StdoutTail: payload.StdoutTail,
		StderrTail: payload.StderrTail,
		Success:    payload.ExitCode == 0,
	}
	if event.TaskID != "task-abc" || event.SessionID != "sess-1" || event.TaskKind != coretasks.KindBash {
		t.Errorf("translation dropped identity fields: %+v", event)
	}
	if event.Success {
		t.Errorf("Success = true for a non-zero ExitCode (%d)", payload.ExitCode)
	}
}
