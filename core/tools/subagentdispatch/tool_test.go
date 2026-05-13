package subagentdispatch_test

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/sigil-tech/kaneaz-harness/core/agentgraph"
	"github.com/sigil-tech/kaneaz-harness/core/tools/subagentdispatch"
)

// TestInputSchema verifies the input schema is valid JSON.
func TestInputSchema(t *testing.T) {
	t.Parallel()
	tool := subagentdispatch.New(subagentdispatch.Options{})
	schema := tool.InputSchema()
	if !json.Valid(schema) {
		t.Errorf("InputSchema is not valid JSON: %s", schema)
	}
	var m map[string]any
	if err := json.Unmarshal(schema, &m); err != nil {
		t.Errorf("InputSchema unmarshal: %v", err)
	}
}

// TestToolName verifies the tool name constant.
func TestToolName(t *testing.T) {
	t.Parallel()
	tool := subagentdispatch.New(subagentdispatch.Options{})
	if got := tool.Name(); got != subagentdispatch.ToolName {
		t.Errorf("Name()=%q, want %q", got, subagentdispatch.ToolName)
	}
}

// TestUnknownProfile verifies an unknown profile returns {error:"unknown_profile"}.
func TestUnknownProfile(t *testing.T) {
	t.Parallel()
	seam := agentgraph.NewFakeBranchSeam()
	tool := subagentdispatch.New(subagentdispatch.Options{
		DataDir: t.TempDir(), // empty data dir — only bundled profiles
		Seam:    seam,
	})
	args := json.RawMessage(`{"profile":"no-such-profile","prompt":"do something"}`)
	raw, err := tool.Call(context.Background(), args)
	if err != nil {
		t.Fatalf("Call: unexpected Go error: %v", err)
	}
	var result map[string]any
	if err := json.Unmarshal(raw, &result); err != nil {
		t.Fatalf("unmarshal result: %v", err)
	}
	if got, _ := result["error"].(string); got != "unknown_profile" {
		t.Errorf("error=%q, want %q", got, "unknown_profile")
	}
	// The available list should contain the bundled profiles.
	avail, _ := result["available"].([]interface{})
	if len(avail) == 0 {
		t.Error("available list should include bundled profile IDs")
	}
}

// TestMissingProfile verifies missing profile field returns an error.
func TestMissingProfile(t *testing.T) {
	t.Parallel()
	tool := subagentdispatch.New(subagentdispatch.Options{DataDir: t.TempDir()})
	raw, err := tool.Call(context.Background(), json.RawMessage(`{"prompt":"do something"}`))
	if err != nil {
		t.Fatalf("unexpected Go error: %v", err)
	}
	var result map[string]any
	_ = json.Unmarshal(raw, &result)
	if got, _ := result["error"].(string); got != "missing_profile" {
		t.Errorf("error=%q, want missing_profile", got)
	}
}

// TestMissingPrompt verifies missing prompt field returns an error.
func TestMissingPrompt(t *testing.T) {
	t.Parallel()
	tool := subagentdispatch.New(subagentdispatch.Options{DataDir: t.TempDir()})
	raw, err := tool.Call(context.Background(), json.RawMessage(`{"profile":"explore"}`))
	if err != nil {
		t.Fatalf("unexpected Go error: %v", err)
	}
	var result map[string]any
	_ = json.Unmarshal(raw, &result)
	if got, _ := result["error"].(string); got != "missing_prompt" {
		t.Errorf("error=%q, want missing_prompt", got)
	}
}

// TestNilSeamError verifies that a nil BranchSeam returns a clean error result.
func TestNilSeamError(t *testing.T) {
	t.Parallel()
	tool := subagentdispatch.New(subagentdispatch.Options{DataDir: t.TempDir()})
	args := json.RawMessage(`{"profile":"explore","prompt":"find something"}`)
	raw, err := tool.Call(context.Background(), args)
	if err != nil {
		t.Fatalf("unexpected Go error: %v", err)
	}
	var result map[string]any
	_ = json.Unmarshal(raw, &result)
	if got, _ := result["error"].(string); got != "seam_not_configured" {
		t.Errorf("error=%q, want seam_not_configured", got)
	}
}

// TestSyncDispatch verifies the sync path (run_in_background=false) returns a
// complete result with merge_summary from the fake seam's canned tail.
func TestSyncDispatch(t *testing.T) {
	t.Parallel()
	seam := agentgraph.NewFakeBranchSeam()
	seam.CompleteAfter = time.Millisecond

	tool := subagentdispatch.New(subagentdispatch.Options{
		DataDir: t.TempDir(),
		Seam:    seam,
	})
	args := json.RawMessage(`{"profile":"explore","prompt":"find all usages","run_in_background":false}`)
	raw, err := tool.Call(context.Background(), args)
	if err != nil {
		t.Fatalf("Call: unexpected Go error: %v", err)
	}
	var result map[string]any
	if err := json.Unmarshal(raw, &result); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if got, _ := result["status"].(string); got != "complete" {
		t.Errorf("status=%q, want complete", got)
	}
	if _, ok := result["branch_id"]; !ok {
		t.Error("expected branch_id in result")
	}
	// The fake seam fork should have been called once.
	if len(seam.Forks) != 1 {
		t.Errorf("seam.Forks len=%d, want 1", len(seam.Forks))
	}
}

// TestAsyncDispatch verifies the async path (run_in_background=true) returns
// status "running" immediately without waiting for the child.
func TestAsyncDispatch(t *testing.T) {
	t.Parallel()
	seam := agentgraph.NewFakeBranchSeam()
	seam.CompleteAfter = 10 * time.Second // would time out if called

	tool := subagentdispatch.New(subagentdispatch.Options{
		DataDir: t.TempDir(),
		Seam:    seam,
	})
	args := json.RawMessage(`{"profile":"explore","prompt":"find usages","run_in_background":true}`)

	start := time.Now()
	raw, err := tool.Call(context.Background(), args)
	elapsed := time.Since(start)
	if err != nil {
		t.Fatalf("Call: unexpected Go error: %v", err)
	}
	if elapsed > 500*time.Millisecond {
		t.Errorf("async dispatch took %v, expected < 500ms", elapsed)
	}

	var result map[string]any
	if err := json.Unmarshal(raw, &result); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if got, _ := result["status"].(string); got != "running" {
		t.Errorf("status=%q, want running", got)
	}
}

// TestWorktreeIsolationRejected verifies worktree isolation returns a clear error.
func TestWorktreeIsolationRejected(t *testing.T) {
	t.Parallel()
	seam := agentgraph.NewFakeBranchSeam()
	tool := subagentdispatch.New(subagentdispatch.Options{
		DataDir: t.TempDir(),
		Seam:    seam,
	})
	args := json.RawMessage(`{"profile":"explore","prompt":"test","isolation":"worktree"}`)
	raw, err := tool.Call(context.Background(), args)
	if err != nil {
		t.Fatalf("unexpected Go error: %v", err)
	}
	var result map[string]any
	_ = json.Unmarshal(raw, &result)
	if got, _ := result["error"].(string); got != "worktree_not_supported" {
		t.Errorf("error=%q, want worktree_not_supported", got)
	}
}
