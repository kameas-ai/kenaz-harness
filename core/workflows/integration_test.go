package workflows_test

// integration_test.go — end-to-end coverage for mission
// workflows-01KQ8TDG WP11.
//
// These tests drive the workflows RPC view through the real engine,
// the real SQLite Store, and a captured-event audit emitter. They
// assert:
//
//   1. Save a 3-step workflow → Run → step events fire + completion +
//      audit events are emitted.
//   2. Run the same workflow twice with rerun_policy=skip → second run
//      hits the cache (no fresh step events).
//   3. Delete a workflow → workflow.deleted audit fires.
//   4. Save a shell-bearing workflow in cedar strict mode → denied at
//      the gate; no audit emitted (TODO marker if cedar isn't easily
//      stitched in — but it is, see setup helpers below).

import (
	"context"
	"encoding/json"
	"sync"
	"testing"

	"github.com/kameas-ai/kenaz-harness/core/context/audit"
	cedarpkg "github.com/kameas-ai/kenaz-harness/core/policy/cedar"
	"github.com/kameas-ai/kenaz-harness/core/storage"
	storagesqlite "github.com/kameas-ai/kenaz-harness/core/storage/sqlite"
	"github.com/kameas-ai/kenaz-harness/core/workflows"
	rpcworkflows "github.com/kameas-ai/kenaz-harness/core/rpc/views/workflows"

	_ "modernc.org/sqlite"
)

// captureEmitter records every audit.Event the workflows layer emits.
// The integration tests pivot on the recorded kinds + payload ids so
// the privacy-CI invariant (no payload field carries step inputs /
// outputs / prompt bytes) is exercised by inspection.
type captureEmitter struct {
	mu     sync.Mutex
	events []audit.Event
}

func (c *captureEmitter) Emit(_ context.Context, e audit.Event) error {
	c.mu.Lock()
	c.events = append(c.events, e)
	c.mu.Unlock()
	return nil
}

func (c *captureEmitter) snapshot() []audit.Event {
	c.mu.Lock()
	defer c.mu.Unlock()
	out := make([]audit.Event, len(c.events))
	copy(out, c.events)
	return out
}

// captureProgress records every progress event the API publishes onto
// the broker. The integration tests use it as an oracle for "did the
// engine actually dispatch the steps fresh, or did it serve a cached
// run".
type captureProgress struct {
	mu     sync.Mutex
	events []workflows.ProgressEvent
}

func (c *captureProgress) Publish(_ string, payload any) {
	if ev, ok := payload.(workflows.ProgressEvent); ok {
		c.mu.Lock()
		c.events = append(c.events, ev)
		c.mu.Unlock()
	}
}

func (c *captureProgress) snapshot() []workflows.ProgressEvent {
	c.mu.Lock()
	defer c.mu.Unlock()
	out := make([]workflows.ProgressEvent, len(c.events))
	copy(out, c.events)
	return out
}

// integrationFixture bundles the moving pieces a WP11 integration test
// needs: a real SQLite-backed Store, an Engine wired with a memory
// rerun cache, a cedar engine in either permissive or strict mode, an
// audit capture, and the API itself.
type integrationFixture struct {
	api      *rpcworkflows.API
	store    workflows.Store
	cache    *workflows.MemoryCache
	progress *captureProgress
	audit    *captureEmitter
}

func newIntegrationFixture(t *testing.T, cedarMode string) *integrationFixture {
	t.Helper()
	dir := t.TempDir()
	db, err := storagesqlite.Open(storage.Config{
		DataDir:          dir,
		EncryptionStatus: storage.EncryptionStatusDisabledWithDiskEncryption,
	})
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	t.Cleanup(func() { _ = db.Close(context.Background()) })

	store := workflows.NewSQLiteStore(db)
	cache := workflows.NewMemoryCache()

	engine := workflows.NewEngine()
	engine.Cache = cache

	// Build a real cedar engine from the embedded bundles only — the
	// strict / permissive distinction lives in the context attribute we
	// pass through CedarMode, not in the policy file selection.
	ce, err := cedarpkg.NewEngine(cedarpkg.Options{
		IncludeEmbedded: true,
		LoadFromDisk:    false,
	})
	if err != nil {
		t.Fatalf("cedar.NewEngine: %v", err)
	}

	prog := &captureProgress{}
	em := &captureEmitter{}

	api := rpcworkflows.New(rpcworkflows.Config{
		Engine:    engine,
		Publisher: prog,
		Store:     store,
		Cedar:     ce,
		CedarMode: cedarMode,
		Audit:     em,
	})

	return &integrationFixture{
		api:      api,
		store:    store,
		cache:    cache,
		progress: prog,
		audit:    em,
	}
}

// threeStepYAML — three echo-shell steps + a rerun_policy so test 2 can
// flip between fresh and cached runs. No model_turn / http_request
// kinds so the test stays fully hermetic.
const threeStepYAML = `
id: integration-three-step
name: "WP11 integration three-step"
version: 1
rerun_policy: skip
steps:
  - name: alpha
    kind: shell
    cmd: "true"
  - name: beta
    kind: shell
    cmd: "true"
  - name: gamma
    kind: shell
    cmd: "true"
`

// Test 1: Save → Run → assert all 3 step events fire + final completion
// event + audit emissions.
func TestWP11_SaveRunEmitsStepAndCompletionEvents(t *testing.T) {
	t.Parallel()
	f := newIntegrationFixture(t, "permissive")
	ctx := context.Background()

	saveOut, err := f.api.Save(ctx, rpcworkflows.SaveInput{YAML: threeStepYAML})
	if err != nil {
		t.Fatalf("Save: %v", err)
	}

	res, err := f.api.RunWithOptions(ctx, rpcworkflows.RunRequest{ID: saveOut.ID})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if res.Status != "completed" {
		t.Fatalf("status=%q want completed (err=%q)", res.Status, res.Err)
	}
	if len(res.Steps) != 3 {
		t.Fatalf("steps len=%d, want 3", len(res.Steps))
	}

	// Each step should have produced at least one progress event.
	progEvents := f.progress.snapshot()
	if len(progEvents) < 3 {
		t.Fatalf("progress events=%d, want >=3", len(progEvents))
	}

	// Audit assertions: workflow.saved + workflow.executed must both
	// appear; workflow.step_failed must NOT appear (the steps all
	// succeed).
	auditEvents := f.audit.snapshot()
	kinds := kindCounts(auditEvents)
	if kinds[audit.KindWorkflowSaved] != 1 {
		t.Errorf("workflow.saved count=%d want 1", kinds[audit.KindWorkflowSaved])
	}
	if kinds[audit.KindWorkflowExecuted] != 1 {
		t.Errorf("workflow.executed count=%d want 1", kinds[audit.KindWorkflowExecuted])
	}
	if kinds[audit.KindWorkflowStepFailed] != 0 {
		t.Errorf("workflow.step_failed count=%d want 0", kinds[audit.KindWorkflowStepFailed])
	}

	// Privacy invariant: workflow.executed payload carries ids + status
	// + counts only — no step output strings.
	for _, ev := range auditEvents {
		if ev.Kind != audit.KindWorkflowExecuted {
			continue
		}
		var p audit.WorkflowExecutedPayload
		if err := json.Unmarshal(ev.Payload, &p); err != nil {
			t.Fatalf("unmarshal payload: %v", err)
		}
		if p.WorkflowID != saveOut.ID {
			t.Errorf("payload workflow_id=%q want %q", p.WorkflowID, saveOut.ID)
		}
		if p.StepCount != 3 {
			t.Errorf("payload step_count=%d want 3", p.StepCount)
		}
		if p.Status != "completed" {
			t.Errorf("payload status=%q want completed", p.Status)
		}
	}
}

// Test 2: Run same workflow twice with rerun_policy=skip → second run
// hits cache, no fresh step progress events.
func TestWP11_RerunSkipServesFromCache(t *testing.T) {
	t.Parallel()
	f := newIntegrationFixture(t, "permissive")
	ctx := context.Background()

	saveOut, err := f.api.Save(ctx, rpcworkflows.SaveInput{YAML: threeStepYAML})
	if err != nil {
		t.Fatalf("Save: %v", err)
	}

	if _, err := f.api.RunWithOptions(ctx, rpcworkflows.RunRequest{ID: saveOut.ID}); err != nil {
		t.Fatalf("Run #1: %v", err)
	}
	firstCount := len(f.progress.snapshot())
	if firstCount < 3 {
		t.Fatalf("first run progress events=%d, want >=3", firstCount)
	}

	// Second run should serve from the rerun cache. The progress
	// channel SHOULD NOT see new step events because Engine.Run
	// short-circuits via applyCachedRun before dispatching the runners.
	res2, err := f.api.RunWithOptions(ctx, rpcworkflows.RunRequest{ID: saveOut.ID})
	if err != nil {
		t.Fatalf("Run #2: %v", err)
	}
	secondCount := len(f.progress.snapshot())
	if secondCount != firstCount {
		t.Errorf("second run added progress events: before=%d after=%d (cache hit should skip emitter)",
			firstCount, secondCount)
	}
	if res2.Status != "completed" {
		t.Errorf("second run status=%q want completed", res2.Status)
	}
}

// Test 3: Delete a workflow → workflow.deleted audit fires.
func TestWP11_DeleteEmitsAudit(t *testing.T) {
	t.Parallel()
	f := newIntegrationFixture(t, "permissive")
	ctx := context.Background()

	saveOut, err := f.api.Save(ctx, rpcworkflows.SaveInput{YAML: threeStepYAML})
	if err != nil {
		t.Fatalf("Save: %v", err)
	}
	if err := f.api.Delete(ctx, saveOut.ID); err != nil {
		t.Fatalf("Delete: %v", err)
	}

	kinds := kindCounts(f.audit.snapshot())
	if kinds[audit.KindWorkflowDeleted] != 1 {
		t.Fatalf("workflow.deleted count=%d want 1", kinds[audit.KindWorkflowDeleted])
	}
}

// Test 4: Save shell-bearing workflow in cedar strict mode → deny.
// The default workflow policy carries a strict-mode forbid rule that
// triggers when context.step_kinds matches "*shell*". The shell-only
// 3-step YAML satisfies that pattern, so Save MUST return
// ErrCedarDenied and the workflow MUST NOT land in the store.
func TestWP11_StrictModeDeniesShellBearingSave(t *testing.T) {
	t.Parallel()
	f := newIntegrationFixture(t, "strict")
	ctx := context.Background()

	_, err := f.api.Save(ctx, rpcworkflows.SaveInput{YAML: threeStepYAML})
	if err == nil {
		t.Fatalf("Save should have been denied in strict mode")
	}
	// The error MUST wrap ErrCedarDenied so callers can pivot the UI.
	if !errorIs(err, rpcworkflows.ErrCedarDenied) {
		t.Fatalf("err=%v want wrap of ErrCedarDenied", err)
	}

	// No workflow.saved audit should fire on a denied save.
	kinds := kindCounts(f.audit.snapshot())
	if kinds[audit.KindWorkflowSaved] != 0 {
		t.Errorf("workflow.saved count=%d want 0 (save was denied)",
			kinds[audit.KindWorkflowSaved])
	}
}

// errorIs is a tiny shim so the integration test file doesn't need to
// import errors just for one Is call.
func errorIs(err, target error) bool {
	for cur := err; cur != nil; {
		if cur == target {
			return true
		}
		// Walk Unwrap chain via fmt.Errorf("%w: ...") wrapping.
		type unwrapper interface{ Unwrap() error }
		u, ok := cur.(unwrapper)
		if !ok {
			return false
		}
		cur = u.Unwrap()
	}
	return false
}

// kindCounts aggregates a slice of audit.Events by kind for compact
// table-driven assertions.
func kindCounts(events []audit.Event) map[audit.Kind]int {
	out := make(map[audit.Kind]int, len(events))
	for _, e := range events {
		out[e.Kind]++
	}
	return out
}
