package planmode_test

import (
	"context"
	"encoding/json"
	"sync"
	"testing"

	"github.com/sigil-tech/kaneaz-harness/core/artifacts"
	"github.com/sigil-tech/kaneaz-harness/core/autonomy"
	"github.com/sigil-tech/kaneaz-harness/core/tools/planmode"
	"github.com/sigil-tech/kaneaz-harness/core/toolloop"
)

// ─── fakes ─────────────────────────────────────────────────────────────────

// fakePosture is a thread-safe in-memory SessionPostureManager.
type fakePosture struct {
	mu     sync.Mutex
	layers map[string]autonomy.Layer
}

func newFakePosture() *fakePosture {
	return &fakePosture{layers: map[string]autonomy.Layer{}}
}

func (f *fakePosture) LoadAutonomyProfile(_ context.Context, sessionID string) (autonomy.Layer, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	l, ok := f.layers[sessionID]
	if !ok {
		return autonomy.DefaultLayer(), nil
	}
	return l, nil
}

func (f *fakePosture) SaveAutonomyProfile(_ context.Context, sessionID string, layer autonomy.Layer) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.layers[sessionID] = layer
	return nil
}

func (f *fakePosture) snapshot() map[string]autonomy.Layer {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make(map[string]autonomy.Layer, len(f.layers))
	for k, v := range f.layers {
		out[k] = v
	}
	return out
}

// fakeArtifacts records Capture calls.
type fakeArtifacts struct {
	mu        sync.Mutex
	captured  []artifacts.CaptureCandidate
	returnID  string
}

func newFakeArtifacts(returnID string) *fakeArtifacts {
	return &fakeArtifacts{returnID: returnID}
}

func (f *fakeArtifacts) Capture(_ context.Context, candidates []artifacts.CaptureCandidate, _ string) ([]artifacts.Artifact, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.captured = append(f.captured, candidates...)
	out := make([]artifacts.Artifact, len(candidates))
	for i := range out {
		out[i] = artifacts.Artifact{ID: f.returnID, Title: candidates[i].Title, MimeType: candidates[i].MimeType}
	}
	return out, nil
}

func (f *fakeArtifacts) snapshot() []artifacts.CaptureCandidate {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make([]artifacts.CaptureCandidate, len(f.captured))
	copy(out, f.captured)
	return out
}

// fixedSessionResolver returns a resolver that always returns sessionID.
func fixedSessionResolver(sessionID string) func(context.Context) string {
	return func(_ context.Context) string { return sessionID }
}

// ─── EnterTool tests ─────────────────────────────────────────────────────────

func TestEnterTool_SetsPostureMode(t *testing.T) {
	t.Parallel()
	posture := newFakePosture()
	tool := planmode.NewEnterTool(planmode.EnterOptions{
		Posture:         posture,
		SessionResolver: fixedSessionResolver("sess-1"),
	})

	raw, err := tool.Call(toolloop.WithSessionID(context.Background(), "sess-1"), nil)
	if err != nil {
		t.Fatalf("unexpected Go error: %v", err)
	}
	var result map[string]any
	if err := json.Unmarshal(raw, &result); err != nil {
		t.Fatalf("unmarshal result: %v", err)
	}

	if errKind, ok := result["error"]; ok {
		t.Fatalf("tool returned error: %v", errKind)
	}
	if got, _ := result["posture"].(string); got != autonomy.PostureModePlanMode {
		t.Errorf("posture = %q, want %q", got, autonomy.PostureModePlanMode)
	}

	layers := posture.snapshot()
	layer, ok := layers["sess-1"]
	if !ok {
		t.Fatal("session layer not saved")
	}
	if layer.PostureMode == nil || *layer.PostureMode != autonomy.PostureModePlanMode {
		t.Errorf("saved PostureMode = %v, want plan_mode", layer.PostureMode)
	}
}

func TestEnterTool_SnapshotsPrePlanLayer(t *testing.T) {
	t.Parallel()
	posture := newFakePosture()
	// Pre-set a non-default layer.
	bold := autonomy.TierBold
	_ = posture.SaveAutonomyProfile(context.Background(), "sess-snap", autonomy.Layer{Level: &bold})

	tool := planmode.NewEnterTool(planmode.EnterOptions{
		Posture:         posture,
		SessionResolver: fixedSessionResolver("sess-snap"),
	})
	if _, err := tool.Call(context.Background(), nil); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	layers := posture.snapshot()
	layer := layers["sess-snap"]
	if layer.PrePlanLayer == nil {
		t.Fatal("PrePlanLayer not captured")
	}
	// Decode the snapshot and verify it has the prior Level.
	var snap autonomy.Layer
	if err := json.Unmarshal(layer.PrePlanLayer, &snap); err != nil {
		t.Fatalf("decode snapshot: %v", err)
	}
	if snap.Level == nil || *snap.Level != autonomy.TierBold {
		t.Errorf("snapshot.Level = %v, want TierBold", snap.Level)
	}
}

func TestEnterTool_IdempotentReEntry(t *testing.T) {
	t.Parallel()
	posture := newFakePosture()
	tool := planmode.NewEnterTool(planmode.EnterOptions{
		Posture:         posture,
		SessionResolver: fixedSessionResolver("sess-idem"),
	})
	ctx := context.Background()

	// First entry.
	if _, err := tool.Call(ctx, nil); err != nil {
		t.Fatalf("first entry: %v", err)
	}
	// Second entry should succeed with was_already=true.
	raw, err := tool.Call(ctx, nil)
	if err != nil {
		t.Fatalf("second entry Go error: %v", err)
	}
	var result map[string]any
	_ = json.Unmarshal(raw, &result)
	if v, _ := result["was_already_in_plan_mode"].(bool); !v {
		t.Error("second entry should return was_already_in_plan_mode=true")
	}
}

func TestEnterTool_NoSessionID(t *testing.T) {
	t.Parallel()
	posture := newFakePosture()
	tool := planmode.NewEnterTool(planmode.EnterOptions{
		Posture: posture,
		SessionResolver: func(_ context.Context) string { return "" },
	})
	raw, err := tool.Call(context.Background(), nil)
	if err != nil {
		t.Fatalf("unexpected Go error: %v", err)
	}
	var result map[string]any
	_ = json.Unmarshal(raw, &result)
	if errKind, _ := result["error"].(string); errKind != "no_session" {
		t.Errorf("error = %q, want no_session", errKind)
	}
}

// ─── ExitTool tests ──────────────────────────────────────────────────────────

func TestExitTool_PendingApproval(t *testing.T) {
	t.Parallel()
	posture := newFakePosture()
	artsStore := newFakeArtifacts("plan-001")
	enterTool := planmode.NewEnterTool(planmode.EnterOptions{
		Posture:         posture,
		SessionResolver: fixedSessionResolver("sess-exit"),
	})
	exitTool := planmode.NewExitTool(planmode.ExitOptions{
		Posture:         posture,
		Artifacts:       artsStore,
		SessionResolver: fixedSessionResolver("sess-exit"),
	})
	ctx := context.Background()

	// Enter plan mode first.
	if _, err := enterTool.Call(ctx, nil); err != nil {
		t.Fatalf("enter: %v", err)
	}

	planText := "# Plan\n\n1. Do A\n2. Do B\n"
	argsJSON, _ := json.Marshal(map[string]string{"plan": planText})
	raw, err := exitTool.Call(ctx, argsJSON)
	if err != nil {
		t.Fatalf("exit Go error: %v", err)
	}
	var result map[string]any
	_ = json.Unmarshal(raw, &result)

	if errKind, ok := result["error"]; ok {
		t.Fatalf("exit returned error: %v", errKind)
	}
	if v, _ := result["awaiting_user_approval"].(bool); !v {
		t.Error("awaiting_user_approval should be true")
	}
	if _, ok := result["plan_id"]; !ok {
		t.Error("plan_id missing from result")
	}
}

func TestExitTool_NotInPlanMode(t *testing.T) {
	t.Parallel()
	posture := newFakePosture()
	artsStore := newFakeArtifacts("x")
	exitTool := planmode.NewExitTool(planmode.ExitOptions{
		Posture:         posture,
		Artifacts:       artsStore,
		SessionResolver: fixedSessionResolver("sess-notplan"),
	})
	argsJSON, _ := json.Marshal(map[string]string{"plan": "hello"})
	raw, _ := exitTool.Call(context.Background(), argsJSON)
	var result map[string]any
	_ = json.Unmarshal(raw, &result)
	if errKind, _ := result["error"].(string); errKind != "not_in_plan_mode" {
		t.Errorf("error = %q, want not_in_plan_mode", errKind)
	}
}

func TestExitTool_EmptyPlan(t *testing.T) {
	t.Parallel()
	posture := newFakePosture()
	artsStore := newFakeArtifacts("x")
	enterTool := planmode.NewEnterTool(planmode.EnterOptions{
		Posture:         posture,
		SessionResolver: fixedSessionResolver("sess-empty"),
	})
	exitTool := planmode.NewExitTool(planmode.ExitOptions{
		Posture:         posture,
		Artifacts:       artsStore,
		SessionResolver: fixedSessionResolver("sess-empty"),
	})
	ctx := context.Background()
	if _, err := enterTool.Call(ctx, nil); err != nil {
		t.Fatalf("enter: %v", err)
	}
	argsJSON, _ := json.Marshal(map[string]string{"plan": ""})
	raw, _ := exitTool.Call(ctx, argsJSON)
	var result map[string]any
	_ = json.Unmarshal(raw, &result)
	if errKind, _ := result["error"].(string); errKind != "invalid_args" {
		t.Errorf("error = %q, want invalid_args", errKind)
	}
}

// ─── RestorePrePlanLayer tests ──────────────────────────────────────────────

func TestRestorePrePlanLayer(t *testing.T) {
	t.Parallel()
	posture := newFakePosture()
	// Set up session with Bold tier.
	bold := autonomy.TierBold
	_ = posture.SaveAutonomyProfile(context.Background(), "sess-restore", autonomy.Layer{Level: &bold})

	// Enter plan mode to snapshot.
	enterTool := planmode.NewEnterTool(planmode.EnterOptions{
		Posture:         posture,
		SessionResolver: fixedSessionResolver("sess-restore"),
	})
	if _, err := enterTool.Call(context.Background(), nil); err != nil {
		t.Fatalf("enter: %v", err)
	}

	// Restore.
	restored, err := planmode.RestorePrePlanLayer(context.Background(), posture, "sess-restore")
	if err != nil {
		t.Fatalf("restore: %v", err)
	}

	// PostureMode should be cleared.
	if restored.PostureMode != nil {
		t.Errorf("restored.PostureMode = %v, want nil", restored.PostureMode)
	}
	// PrePlanLayer snapshot should be cleared.
	if restored.PrePlanLayer != nil {
		t.Errorf("restored.PrePlanLayer should be nil after restore")
	}
	// Level should be restored.
	if restored.Level == nil || *restored.Level != autonomy.TierBold {
		t.Errorf("restored.Level = %v, want TierBold", restored.Level)
	}

	// Also verify persisted layer is consistent.
	saved, _ := posture.LoadAutonomyProfile(context.Background(), "sess-restore")
	if saved.PostureMode != nil {
		t.Error("persisted layer still has PostureMode set")
	}
}

// ─── BuiltinTool interface compliance ───────────────────────────────────────

func TestEnterTool_BuiltinToolInterface(t *testing.T) {
	posture := newFakePosture()
	tool := planmode.NewEnterTool(planmode.EnterOptions{Posture: posture})
	if tool.Name() != planmode.EnterToolName {
		t.Errorf("Name() = %q, want %q", tool.Name(), planmode.EnterToolName)
	}
	if tool.Description() == "" {
		t.Error("Description() is empty")
	}
	if len(tool.InputSchema()) == 0 {
		t.Error("InputSchema() is empty")
	}
}

func TestExitTool_BuiltinToolInterface(t *testing.T) {
	posture := newFakePosture()
	artsStore := newFakeArtifacts("x")
	tool := planmode.NewExitTool(planmode.ExitOptions{Posture: posture, Artifacts: artsStore})
	if tool.Name() != planmode.ExitToolName {
		t.Errorf("Name() = %q, want %q", tool.Name(), planmode.ExitToolName)
	}
	if tool.Description() == "" {
		t.Error("Description() is empty")
	}
	if len(tool.InputSchema()) == 0 {
		t.Error("InputSchema() is empty")
	}
}
