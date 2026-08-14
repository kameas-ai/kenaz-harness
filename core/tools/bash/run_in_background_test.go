package bash_test

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/kameas-ai/kenaz-harness/core/tools/bash"
)

// backgroundRegistry is an in-memory fake for the background spawn/end seam.
type backgroundRegistry struct {
	mu    sync.Mutex
	tasks map[string]bgTask
	ended map[string]int // task_id → exit_code
}

type bgTask struct {
	sessionID   string
	cmd         string
	description string
	pid         int
}

func newBgRegistry() *backgroundRegistry {
	return &backgroundRegistry{
		tasks: make(map[string]bgTask),
		ended: make(map[string]int),
	}
}

func (r *backgroundRegistry) spawn(_ context.Context, sessionID, cmd, description string, pid int) (string, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	id := fmt.Sprintf("task-fake-%d", len(r.tasks)+1)
	r.tasks[id] = bgTask{sessionID: sessionID, cmd: cmd, description: description, pid: pid}
	return id, nil
}

func (r *backgroundRegistry) end(_ context.Context, taskID string, exitCode int) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.ended[taskID] = exitCode
}

func (r *backgroundRegistry) snapshot() (map[string]bgTask, map[string]int) {
	r.mu.Lock()
	defer r.mu.Unlock()
	tasks := make(map[string]bgTask, len(r.tasks))
	for k, v := range r.tasks {
		tasks[k] = v
	}
	ended := make(map[string]int, len(r.ended))
	for k, v := range r.ended {
		ended[k] = v
	}
	return tasks, ended
}

func newBackgroundTool(reg *backgroundRegistry) *bash.Tool {
	return bash.New(bash.Options{
		SandboxRoot:      "/tmp",
		Allowlist:        []string{"sleep", "echo", "sh", "bash"},
		BackgroundSpawn:  reg.spawn,
		BackgroundEnd:    reg.end,
	})
}

func TestRunInBackground_ReturnsFastWithTaskID(t *testing.T) {
	reg := newBgRegistry()
	tool := newBackgroundTool(reg)
	ctx := context.Background()

	argsJSON, _ := json.Marshal(map[string]any{
		"command":           "sleep 5",
		"run_in_background": true,
		"description":       "test sleep",
	})

	start := time.Now()
	result, err := tool.Call(ctx, argsJSON)
	elapsed := time.Since(start)

	if err != nil {
		t.Fatalf("Call: %v", err)
	}
	if elapsed > 500*time.Millisecond {
		t.Errorf("background call took %v; want <500ms", elapsed)
	}

	var out map[string]any
	if err := json.Unmarshal(result, &out); err != nil {
		t.Fatalf("unmarshal result: %v", err)
	}
	if out["status"] != "running" {
		t.Errorf("status = %v; want running", out["status"])
	}
	if out["task_id"] == "" {
		t.Error("task_id is empty")
	}

	tasks, _ := reg.snapshot()
	if len(tasks) == 0 {
		t.Error("no task registered in fake registry")
	}
}

func TestRunInBackground_RegistryEndCalledOnExit(t *testing.T) {
	reg := newBgRegistry()
	tool := newBackgroundTool(reg)
	ctx := context.Background()

	argsJSON, _ := json.Marshal(map[string]any{
		"command":           "echo hello_bg",
		"run_in_background": true,
	})

	result, err := tool.Call(ctx, argsJSON)
	if err != nil {
		t.Fatalf("Call: %v", err)
	}
	var out map[string]any
	_ = json.Unmarshal(result, &out)
	taskID, _ := out["task_id"].(string)
	if taskID == "" {
		t.Fatal("task_id is empty")
	}

	// Wait for the background goroutine to finish and call End.
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		_, ended := reg.snapshot()
		if ec, ok := ended[taskID]; ok {
			if ec != 0 {
				t.Errorf("exit_code = %d; want 0 for 'echo hello_bg'", ec)
			}
			return // success
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Error("backgroundEnd was not called within 3s")
}

func TestRunInBackground_FalseRunsSynchronously(t *testing.T) {
	reg := newBgRegistry()
	tool := newBackgroundTool(reg)
	ctx := context.Background()

	argsJSON, _ := json.Marshal(map[string]any{
		"command":           "echo sync_test",
		"run_in_background": false,
	})

	result, err := tool.Call(ctx, argsJSON)
	if err != nil {
		t.Fatalf("Call: %v", err)
	}

	var out map[string]any
	_ = json.Unmarshal(result, &out)

	// Synchronous call returns stdout, not task_id.
	if out["task_id"] != nil {
		t.Error("synchronous call should not return task_id")
	}
	if out["stdout"] == nil {
		t.Error("synchronous call should return stdout")
	}
}

func TestRunInBackground_NoRegistryFallsBackToSync(t *testing.T) {
	// Tool without BackgroundSpawn: background mode falls back to sync.
	tool := bash.New(bash.Options{
		SandboxRoot: "/tmp",
	})
	ctx := context.Background()

	argsJSON, _ := json.Marshal(map[string]any{
		"command":           "echo fallback",
		"run_in_background": true,
	})

	result, err := tool.Call(ctx, argsJSON)
	if err != nil {
		t.Fatalf("Call: %v", err)
	}

	var out map[string]any
	_ = json.Unmarshal(result, &out)
	// Without BackgroundSpawn, it runs synchronously and returns stdout.
	if out["task_id"] != nil {
		t.Error("without BackgroundSpawn, run_in_background should be ignored")
	}
}

// schemaProperties unmarshals a tool input schema and returns its
// top-level "properties" object.
func schemaProperties(t *testing.T, raw json.RawMessage) map[string]any {
	t.Helper()
	var schema struct {
		Properties map[string]any `json:"properties"`
	}
	if err := json.Unmarshal(raw, &schema); err != nil {
		t.Fatalf("unmarshal input schema: %v", err)
	}
	return schema.Properties
}

// TestInputSchema_OmitsBackgroundKnobWhenSeamUnwired is the unwired-sweep
// pin (2026-08-14). Production wires bash WITHOUT BackgroundSpawn
// (core/rpc/builtins_wiring.go), and Call() silently ignores
// run_in_background in that configuration — so the schema must not offer
// it. Advertising it hands the model a task_id contract nothing honours:
// it believes it backgrounded a long command, and the command actually
// ran to completion inline. Same doctrine as FR-007's
// subagent_dispatch-not-registered-when-seam-nil guard.
func TestInputSchema_OmitsBackgroundKnobWhenSeamUnwired(t *testing.T) {
	tool := bash.New(bash.Options{SandboxRoot: "/tmp"})

	props := schemaProperties(t, tool.InputSchema())
	for _, key := range []string{"run_in_background", "description"} {
		if _, ok := props[key]; ok {
			t.Errorf("input schema advertises %q with no BackgroundSpawn wired; "+
				"Call() ignores it, so the model is being promised a capability the harness cannot perform", key)
		}
	}
	// The foreground knobs must survive the split.
	for _, key := range []string{"command", "working_dir", "timeout_seconds"} {
		if _, ok := props[key]; !ok {
			t.Errorf("input schema lost %q", key)
		}
	}
}

// TestInputSchema_AdvertisesBackgroundKnobWhenSeamWired is the other
// half: once the seam IS wired the knob must reappear, so the split is a
// real conditional and not a permanent deletion.
func TestInputSchema_AdvertisesBackgroundKnobWhenSeamWired(t *testing.T) {
	tool := newBackgroundTool(newBgRegistry())

	props := schemaProperties(t, tool.InputSchema())
	for _, key := range []string{"command", "working_dir", "timeout_seconds", "run_in_background", "description"} {
		if _, ok := props[key]; !ok {
			t.Errorf("input schema missing %q with BackgroundSpawn wired", key)
		}
	}
}
