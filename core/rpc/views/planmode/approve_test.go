package planmode_test

import (
	"context"
	"sync"
	"testing"

	coreart "github.com/sigil-tech/kaneaz-harness/core/artifacts"
	"github.com/sigil-tech/kaneaz-harness/core/autonomy"
	"github.com/sigil-tech/kaneaz-harness/core/tools/planmode"
	planmodeview "github.com/sigil-tech/kaneaz-harness/core/rpc/views/planmode"
)

// ─── fakes ─────────────────────────────────────────────────────────────────

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
	if l, ok := f.layers[sessionID]; ok {
		return l, nil
	}
	return autonomy.DefaultLayer(), nil
}

func (f *fakePosture) SaveAutonomyProfile(_ context.Context, sessionID string, layer autonomy.Layer) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.layers[sessionID] = layer
	return nil
}

func (f *fakePosture) snapshot(sessionID string) autonomy.Layer {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.layers[sessionID]
}

type fakeEmitter struct {
	mu     sync.Mutex
	events []emitRecord
}

type emitRecord struct {
	sessionID string
	event     string
	payload   map[string]any
}

func (e *fakeEmitter) Emit(_ context.Context, sessionID, event string, payload map[string]any) error {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.events = append(e.events, emitRecord{sessionID: sessionID, event: event, payload: payload})
	return nil
}

func (e *fakeEmitter) snapshot() []emitRecord {
	e.mu.Lock()
	defer e.mu.Unlock()
	out := make([]emitRecord, len(e.events))
	copy(out, e.events)
	return out
}

type fakeUpdater struct {
	mu       sync.Mutex
	versions []updateRecord
}

type updateRecord struct {
	artifactID string
	content    string
}

func (u *fakeUpdater) WriteVersion(_ context.Context, artifactID string, bytes []byte, _ string, _, _ *string) (coreart.ArtifactVersion, error) {
	u.mu.Lock()
	defer u.mu.Unlock()
	u.versions = append(u.versions, updateRecord{artifactID: artifactID, content: string(bytes)})
	return coreart.ArtifactVersion{ArtifactID: artifactID}, nil
}

func (u *fakeUpdater) snapshot() []updateRecord {
	u.mu.Lock()
	defer u.mu.Unlock()
	out := make([]updateRecord, len(u.versions))
	copy(out, u.versions)
	return out
}

// enterPlanMode is a test helper that puts a session into plan_mode
// by directly manipulating the fakePosture.
func enterPlanMode(posture *fakePosture, sessionID string) {
	ctx := context.Background()
	current, _ := posture.LoadAutonomyProfile(ctx, sessionID)
	pm := autonomy.PostureModePlanMode
	// Snapshot prior layer.
	enter := planmode.NewEnterTool(planmode.EnterOptions{
		Posture: posture,
		SessionResolver: func(_ context.Context) string { return sessionID },
	})
	_, _ = enter.Call(ctx, nil)
	_ = current // unused after enter
	_ = pm
}

// ─── Approve tests ───────────────────────────────────────────────────────────

func TestApprove_RestoresLayerAndEmitsEvent(t *testing.T) {
	t.Parallel()
	posture := newFakePosture()
	emitter := &fakeEmitter{}
	api := planmodeview.NewAPI(posture, emitter, nil)

	enterPlanMode(posture, "sess-approve")

	resp, err := api.Approve(context.Background(), planmodeview.ApproveRequest{
		SessionID: "sess-approve",
		PlanID:    "plan-001",
	})
	if err != nil {
		t.Fatalf("Approve error: %v", err)
	}
	if !resp.Approved {
		t.Error("Approve should return approved:true")
	}

	// PostureMode should be cleared.
	layer := posture.snapshot("sess-approve")
	if layer.PostureMode != nil {
		t.Errorf("PostureMode should be nil after approve, got %v", layer.PostureMode)
	}

	// Event should be emitted.
	events := emitter.snapshot()
	if len(events) != 1 {
		t.Fatalf("emitter events = %d, want 1", len(events))
	}
	if events[0].event != planmodeview.EventPlanModeChanged {
		t.Errorf("event = %q, want %q", events[0].event, planmodeview.EventPlanModeChanged)
	}
	if outcome := events[0].payload["outcome"]; outcome != "approved" {
		t.Errorf("outcome = %v, want approved", outcome)
	}
}

// ─── Discard tests ───────────────────────────────────────────────────────────

func TestDiscard_RestoresLayerAndEmitsEvent(t *testing.T) {
	t.Parallel()
	posture := newFakePosture()
	emitter := &fakeEmitter{}
	api := planmodeview.NewAPI(posture, emitter, nil)

	enterPlanMode(posture, "sess-discard")

	resp, err := api.Discard(context.Background(), planmodeview.DiscardRequest{
		SessionID: "sess-discard",
		PlanID:    "plan-002",
	})
	if err != nil {
		t.Fatalf("Discard error: %v", err)
	}
	if resp.Approved {
		t.Error("Discard should return approved:false")
	}
	if resp.Reason != "discarded" {
		t.Errorf("reason = %q, want discarded", resp.Reason)
	}

	layer := posture.snapshot("sess-discard")
	if layer.PostureMode != nil {
		t.Errorf("PostureMode should be nil after discard, got %v", layer.PostureMode)
	}

	events := emitter.snapshot()
	if len(events) != 1 {
		t.Fatalf("events = %d, want 1", len(events))
	}
	if outcome := events[0].payload["outcome"]; outcome != "discarded" {
		t.Errorf("outcome = %v, want discarded", outcome)
	}
}

// ─── Edit tests ──────────────────────────────────────────────────────────────

func TestEdit_UpdatesArtifactAndApproves(t *testing.T) {
	t.Parallel()
	posture := newFakePosture()
	emitter := &fakeEmitter{}
	updater := &fakeUpdater{}
	api := planmodeview.NewAPI(posture, emitter, updater)

	enterPlanMode(posture, "sess-edit")

	editedPlan := "# Edited Plan\n\nUpdated content."
	resp, err := api.Edit(context.Background(), planmodeview.EditRequest{
		SessionID:  "sess-edit",
		PlanID:     "plan-003",
		EditedPlan: editedPlan,
	})
	if err != nil {
		t.Fatalf("Edit error: %v", err)
	}
	if !resp.Approved {
		t.Error("Edit should return approved:true")
	}

	// Artifact should be updated.
	versions := updater.snapshot()
	if len(versions) != 1 {
		t.Fatalf("updater calls = %d, want 1", len(versions))
	}
	if versions[0].content != editedPlan {
		t.Errorf("updated content mismatch")
	}

	// PostureMode cleared.
	layer := posture.snapshot("sess-edit")
	if layer.PostureMode != nil {
		t.Error("PostureMode should be nil after edit+approve")
	}

	events := emitter.snapshot()
	if len(events) != 1 {
		t.Fatalf("events = %d, want 1", len(events))
	}
	if outcome := events[0].payload["outcome"]; outcome != "edited_and_approved" {
		t.Errorf("outcome = %v, want edited_and_approved", outcome)
	}
}

// ─── Error paths ─────────────────────────────────────────────────────────────

func TestApprove_EmptySessionID(t *testing.T) {
	t.Parallel()
	posture := newFakePosture()
	api := planmodeview.NewAPI(posture, nil, nil)
	if _, err := api.Approve(context.Background(), planmodeview.ApproveRequest{}); err == nil {
		t.Error("Approve with empty session_id should return an error")
	}
}

func TestDiscard_EmptySessionID(t *testing.T) {
	t.Parallel()
	posture := newFakePosture()
	api := planmodeview.NewAPI(posture, nil, nil)
	if _, err := api.Discard(context.Background(), planmodeview.DiscardRequest{}); err == nil {
		t.Error("Discard with empty session_id should return an error")
	}
}

func TestEdit_EmptySessionID(t *testing.T) {
	t.Parallel()
	posture := newFakePosture()
	api := planmodeview.NewAPI(posture, nil, nil)
	if _, err := api.Edit(context.Background(), planmodeview.EditRequest{PlanID: "p"}); err == nil {
		t.Error("Edit with empty session_id should return an error")
	}
}

func TestNewAPI_NilPosturePanics(t *testing.T) {
	defer func() {
		if r := recover(); r == nil {
			t.Error("NewAPI(nil posture) should panic")
		}
	}()
	planmodeview.NewAPI(nil, nil, nil)
}
