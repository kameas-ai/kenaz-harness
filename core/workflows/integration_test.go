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
//   2. (automation-actually-runs-01PMZ404 UNIT-13 flipped this one) A
//      workflow row carrying a legacy rerun_policy loads fine but the
//      field is scrubbed, so running it twice dispatches fresh both
//      times — it can no longer hit this fixture's wired Engine.Cache.
//      See TestWP11_LegacyRerunPolicyNeverReachesCacheAfterLoad's own
//      doc comment for why, and rerun_test.go for where the cache
//      mechanism itself is still tested.
//   3. Delete a workflow → workflow.deleted audit fires.
//   4. Save a shell-bearing workflow in cedar strict mode → denied at
//      the gate; no audit emitted (TODO marker if cedar isn't easily
//      stitched in — but it is, see setup helpers below).

import (
	"context"
	"encoding/json"
	"sync"
	"testing"
	"time"

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
//
// The API now publishes rpcworkflows.FrontendProgressEvent (the
// phase-discriminated envelope the frontend expects) rather than the
// flat workflows.ProgressEvent. captureProgress accepts the new type
// so TestWP11_SaveRunEmitsStepAndCompletionEvents / TestWP11_RerunSkip
// can still assert "events were emitted" without caring about shape.
type captureProgress struct {
	mu     sync.Mutex
	events []rpcworkflows.FrontendProgressEvent
}

func (c *captureProgress) Publish(_ string, payload any) {
	if ev, ok := payload.(rpcworkflows.FrontendProgressEvent); ok {
		c.mu.Lock()
		c.events = append(c.events, ev)
		c.mu.Unlock()
	}
}

func (c *captureProgress) snapshot() []rpcworkflows.FrontendProgressEvent {
	c.mu.Lock()
	defer c.mu.Unlock()
	out := make([]rpcworkflows.FrontendProgressEvent, len(c.events))
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
	db       storage.DB
	cache    *workflows.MemoryCache
	progress *captureProgress
	audit    *captureEmitter
}

func newIntegrationFixture(t *testing.T, cedarMode string) *integrationFixture {
	t.Helper()
	return newIntegrationFixtureSeeded(t, cedarMode, nil)
}

// newIntegrationFixtureSeeded is newIntegrationFixture plus an optional
// seed hook run against the raw DB after Open but before
// rpcworkflows.New constructs the API. rpcworkflows.New hydrates its
// in-memory byID catalog from cfg.Store.List/Load exactly once, at
// construction (core/rpc/views/workflows/impl.go New) — a row inserted
// AFTER that point (e.g. via a direct SQL bypass of Store.Save, as
// TestWP11_RerunSkipServesFromCache needs post-UNIT-13, since Save no
// longer accepts a non-empty rerun_policy) would otherwise never
// appear in the catalog RunWithOptions resolves against, and Run would
// fail with ErrWorkflowNotFound despite the row being on disk. seed
// may be nil.
func newIntegrationFixtureSeeded(t *testing.T, cedarMode string, seed func(ctx context.Context, db storage.DB)) *integrationFixture {
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

	if seed != nil {
		seed(context.Background(), db)
	}

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
		db:       db,
		cache:    cache,
		progress: prog,
		audit:    em,
	}
}

// threeStepYAML — three echo-shell steps. No model_turn / http_request
// kinds so the test stays fully hermetic.
//
// This no longer declares rerun_policy (automation-actually-runs-
// 01PMZ404 UNIT-13, A-10): Store.Save now refuses a non-empty
// rerun_policy outright, so a workflow authored through the normal
// Save() RPC — which is what every test using this fixture except
// TestWP11_RerunSkipServesFromCache does — can no longer declare one.
// TestWP11_RerunSkipServesFromCache still needs the dial to exercise
// the Engine.Cache seam this unit deliberately preserves; it inserts
// its own copy directly via SQL (bypassing Store.Save) to simulate a
// workflow written before this build narrowed the schema — see that
// test for why.
const threeStepYAML = `
id: integration-three-step
name: "WP11 integration three-step"
version: 1
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

// Test 2 (behaviour changed by automation-actually-runs-01PMZ404
// UNIT-13, A-10): a workflow row carrying a legacy rerun_policy —
// the shape a previous release's lenient Save could still write, or a
// hand-edited row — loads successfully (UNIT-13's load-side
// tolerance) but the field comes back SCRUBBED (storage.go's Load,
// "drop the value"). This test's name and assertion used to be the
// opposite (RerunSkipServesFromCache): before UNIT-13, a workflow
// declaring rerun_policy=skip really did serve its second run from
// this fixture's wired Engine.Cache. That is no longer reachable
// through the store-backed RPC path, by design — Store.Load always
// clears RerunPolicy before Engine.Run ever sees the workflow, so
// e.g. Cache != nil && wf.RerunPolicy != "" (runtime.go) can never be
// true for anything that came through Store, regardless of whether a
// future release wires Engine.Cache in production. That is not a gap:
// A-10 says plainly that "narrowing the schema stops the product
// lying about the dial; it does not stop the cost — every workflow
// run re-executes and re-bills," and this is what makes that true
// even for a row that still carries the old value on disk. The
// underlying cache mechanism itself — the seam A-10 says must stay —
// is exercised directly against the Engine, independent of Store, by
// rerun_test.go's TestEngine_RerunPolicy_SkipReturnsCached.
func TestWP11_LegacyRerunPolicyNeverReachesCacheAfterLoad(t *testing.T) {
	t.Parallel()
	const id = "integration-three-step-rerun-skip"
	legacyYAML := `
id: ` + id + `
name: "WP11 integration three-step (legacy rerun_policy)"
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
	// Seeded BEFORE rpcworkflows.New runs, so the row is present in the
	// byID catalog New hydrates at construction — see
	// newIntegrationFixtureSeeded's doc for why a post-construction
	// insert would silently miss it.
	f := newIntegrationFixtureSeeded(t, "permissive", func(ctx context.Context, db storage.DB) {
		now := time.Now().UTC().UnixNano()
		if err := db.WriteTx(ctx, func(tx storage.WriteTx) error {
			_, err := tx.Exec(ctx,
				`INSERT INTO workflows (id, name, description, yaml_source, version, hash, created_at, updated_at)
				 VALUES (?, ?, '', ?, 1, 'wp11-rerun-skip-probe-hash', ?, ?)`,
				id, "WP11 integration three-step (legacy rerun_policy)", legacyYAML, now, now,
			)
			return err
		}); err != nil {
			t.Fatalf("insert legacy rerun_policy=skip workflow row directly (Store.Save now refuses it — UNIT-13, A-10): %v", err)
		}
	})
	ctx := context.Background()

	if _, err := f.api.RunWithOptions(ctx, rpcworkflows.RunRequest{ID: id}); err != nil {
		t.Fatalf("Run #1: %v", err)
	}
	firstCount := len(f.progress.snapshot())
	if firstCount < 3 {
		t.Fatalf("first run progress events=%d, want >=3", firstCount)
	}

	// Second run must NOT serve from the rerun cache — the loaded
	// workflow's RerunPolicy was already scrubbed to "" by Store.Load,
	// so Engine.Run's cache-consult guard (Cache != nil &&
	// wf.RerunPolicy != "") is false and every step dispatches fresh
	// again, the same as the first run.
	res2, err := f.api.RunWithOptions(ctx, rpcworkflows.RunRequest{ID: id})
	if err != nil {
		t.Fatalf("Run #2: %v", err)
	}
	secondCount := len(f.progress.snapshot())
	newEvents := secondCount - firstCount
	if newEvents < firstCount {
		t.Errorf("second run added only %d new progress events (before=%d after=%d), want a full fresh dispatch (roughly firstCount more) — "+
			"looks like the cache short-circuited, which UNIT-13's load-side scrub should make impossible",
			newEvents, firstCount, secondCount)
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
