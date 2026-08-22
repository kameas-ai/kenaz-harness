package scheduledchat_test

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	cedargo "github.com/cedar-policy/cedar-go"

	"github.com/kameas-ai/kenaz-harness/core/policy/cedar"
	"github.com/kameas-ai/kenaz-harness/core/rpc/views/scheduledchat"
	"github.com/kameas-ai/kenaz-harness/core/scheduler"
)

// denyAllGate is a cedar.Gate stub that denies every evaluation. Used to
// prove a Cedar denial stays ErrCedarDenied and is not shadowed by the
// dispatcher-unavailable check.
type denyAllGate struct{}

func (denyAllGate) Evaluate(_ context.Context, principal cedargo.EntityUID, action string, resource cedargo.EntityUID, _ map[cedargo.String]cedargo.Value) cedar.Decision {
	return cedar.Decision{
		Outcome:   cedar.Deny,
		Action:    action,
		Principal: principal.String(),
		Resource:  resource.String(),
		Reason:    "denyAllGate: test stub",
	}
}

var _ cedar.Gate = denyAllGate{}

// ── fake store ────────────────────────────────────────────────────────────
//
// WP-PI (persistence integrity, mission model-scheduled-jobs-01PMSJ01):
// this in-memory fake is deliberately NOT backed by real sqlite. Every
// test in this file that uses it (including WP02's TestRunNowNilDispatcher
// and TestRunNowCedarDenialStaysCedarDenial) asserts scheduledchat.API's
// own control flow — dispatcher-nil handling, gate-check ordering, error
// mapping — not SQL encode/decode fidelity or schema behaviour. The real
// persistence layer (scheduler.SQLiteChatStore, core/scheduler/
// chat_store.go) is a separate, thin pass-through with no business logic
// of its own to hide a defect in; core/scheduler/chat_cron_engine_test.go
// and core/rpc/chat_run_dispatcher_test.go are what drive it against real
// sqlite for this mission's WP03/WP04 dispatch-and-persist paths. Per
// CLAUDE.md blind spot #2: this comment exists so a future audit does not
// have to re-derive that this fixture's scope is legitimate before
// re-litigating it.

type fakeStore struct {
	mu      sync.Mutex
	records map[string]scheduler.ChatRunRecord
	history []scheduler.ChatRunHistoryRecord
}

func newFakeStore() *fakeStore {
	return &fakeStore{records: make(map[string]scheduler.ChatRunRecord)}
}

func (f *fakeStore) Create(_ context.Context, r scheduler.ChatRunRecord) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.records[r.ID] = r
	return nil
}

func (f *fakeStore) Update(_ context.Context, r scheduler.ChatRunRecord) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if _, ok := f.records[r.ID]; !ok {
		return scheduler.ErrChatRunNotFound
	}
	existing := f.records[r.ID]
	r.CreatedAt = existing.CreatedAt
	// CreatedBy is immutable post-create — mirrors
	// scheduler.SQLiteChatStore.Update's SQL statement, which does not
	// list the created_by column at all (model-scheduled-jobs-01PMSJ01
	// WP09). Without this line the fake's full-struct overwrite silently
	// clobbers provenance on every Update, which is exactly the "test
	// fixture bypasses the layer under test" shape CLAUDE.md's blind
	// spot #2 warns about — caught by TestUpdateCannotChangeCreatedBy.
	r.CreatedBy = existing.CreatedBy
	f.records[r.ID] = r
	return nil
}

func (f *fakeStore) Delete(_ context.Context, id string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	delete(f.records, id)
	return nil
}

func (f *fakeStore) Get(_ context.Context, id string) (scheduler.ChatRunRecord, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	r, ok := f.records[id]
	if !ok {
		return scheduler.ChatRunRecord{}, scheduler.ErrChatRunNotFound
	}
	return r, nil
}

func (f *fakeStore) List(_ context.Context) ([]scheduler.ChatRunRecord, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make([]scheduler.ChatRunRecord, 0, len(f.records))
	for _, r := range f.records {
		out = append(out, r)
	}
	return out, nil
}

func (f *fakeStore) SetEnabled(_ context.Context, id string, enabled bool) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	r, ok := f.records[id]
	if !ok {
		return scheduler.ErrChatRunNotFound
	}
	r.Enabled = enabled
	f.records[id] = r
	return nil
}

func (f *fakeStore) AppendHistory(_ context.Context, h scheduler.ChatRunHistoryRecord) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.history = append(f.history, h)
	return nil
}

func (f *fakeStore) History(_ context.Context, chatRunID string, limit int) ([]scheduler.ChatRunHistoryRecord, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	var out []scheduler.ChatRunHistoryRecord
	for _, h := range f.history {
		if h.ChatRunID == chatRunID {
			out = append(out, h)
		}
	}
	if limit > 0 && len(out) > limit {
		out = out[:limit]
	}
	return out, nil
}

var _ scheduler.ScheduledChatStore = (*fakeStore)(nil)

// ── fake dispatcher ──────────────────────────────────────────────────────
//
// A real (non-noop) stand-in: it actually reports a "completed" outcome
// because a run genuinely happened in the test's telling, not because a
// nil-dispatcher fallback fabricated one. See WP02 — no production or test
// path may substitute a fabricated "completed" for a missing dispatcher.

type fakeDispatcher struct {
	mu    sync.Mutex
	calls int
}

func (f *fakeDispatcher) DispatchChatRun(_ context.Context, job scheduler.Job, now time.Time) (scheduler.ChatRunHistoryRecord, error) {
	f.mu.Lock()
	f.calls++
	f.mu.Unlock()
	ended := now
	chatRunID := ""
	if job.ChatRun != nil {
		chatRunID = job.ChatRun.ID
	}
	return scheduler.ChatRunHistoryRecord{
		ChatRunID:     chatRunID,
		SessionID:     "sess-fake",
		Status:        "completed",
		StartedAt:     now,
		EndedAt:       &ended,
		OutputSnippet: "fake dispatch ok",
	}, nil
}

var _ scheduler.ChatRunDispatcher = (*fakeDispatcher)(nil)

// ── tests ─────────────────────────────────────────────────────────────────

func TestCreateAndGet(t *testing.T) {
	store := newFakeStore()
	api := scheduledchat.New(scheduledchat.Config{Store: store})

	entry, err := api.Create(context.Background(), scheduledchat.CreateInput{
		Name:           "Daily briefing",
		PromptTemplate: "Summarize today: {{date}}",
		Cron:           "0 9 * * *",
		Enabled:        true,
	})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if entry.ID == "" {
		t.Fatal("Create returned empty ID")
	}
	if entry.Name != "Daily briefing" {
		t.Errorf("Name=%q, want 'Daily briefing'", entry.Name)
	}
	if entry.OutputSink != "banner" {
		t.Errorf("OutputSink=%q, want 'banner' (default)", entry.OutputSink)
	}

	got, err := api.Get(context.Background(), entry.ID)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got.ID != entry.ID {
		t.Errorf("Get ID mismatch: %q vs %q", got.ID, entry.ID)
	}
}

func TestCreateRequiresCron(t *testing.T) {
	api := scheduledchat.New(scheduledchat.Config{Store: newFakeStore()})
	_, err := api.Create(context.Background(), scheduledchat.CreateInput{Name: "X"})
	if err == nil {
		t.Fatal("expected error for missing cron")
	}
	if !errors.Is(err, scheduledchat.ErrInvalidInput) {
		t.Errorf("want ErrInvalidInput, got %v", err)
	}
}

func TestUpdate(t *testing.T) {
	store := newFakeStore()
	api := scheduledchat.New(scheduledchat.Config{Store: store})

	entry, _ := api.Create(context.Background(), scheduledchat.CreateInput{
		Name: "Old name", Cron: "0 9 * * *", Enabled: true,
	})

	updated, err := api.Update(context.Background(), scheduledchat.UpdateInput{
		ID:   entry.ID,
		Name: "New name",
		Cron: "0 10 * * *",
	})
	if err != nil {
		t.Fatalf("Update: %v", err)
	}
	if updated.Name != "New name" {
		t.Errorf("Name=%q, want 'New name'", updated.Name)
	}
}

func TestUpdateNotFound(t *testing.T) {
	api := scheduledchat.New(scheduledchat.Config{Store: newFakeStore()})
	_, err := api.Update(context.Background(), scheduledchat.UpdateInput{
		ID: "nonexistent", Cron: "0 9 * * *",
	})
	if !errors.Is(err, scheduledchat.ErrNotFound) {
		t.Errorf("want ErrNotFound, got %v", err)
	}
}

func TestDelete(t *testing.T) {
	store := newFakeStore()
	api := scheduledchat.New(scheduledchat.Config{Store: store})

	entry, _ := api.Create(context.Background(), scheduledchat.CreateInput{
		Name: "To delete", Cron: "0 9 * * *", Enabled: true,
	})
	if err := api.Delete(context.Background(), entry.ID); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	_, err := api.Get(context.Background(), entry.ID)
	if !errors.Is(err, scheduledchat.ErrNotFound) {
		t.Errorf("after delete, Get should return ErrNotFound, got %v", err)
	}
}

func TestSetEnabled(t *testing.T) {
	store := newFakeStore()
	api := scheduledchat.New(scheduledchat.Config{Store: store})

	entry, _ := api.Create(context.Background(), scheduledchat.CreateInput{
		Name: "Toggle me", Cron: "0 9 * * *", Enabled: true,
	})
	if err := api.SetEnabled(context.Background(), entry.ID, false); err != nil {
		t.Fatalf("SetEnabled: %v", err)
	}
	got, _ := api.Get(context.Background(), entry.ID)
	if got.Enabled {
		t.Error("expected Enabled=false after SetEnabled(false)")
	}
}

func TestRunNow(t *testing.T) {
	store := newFakeStore()
	disp := &fakeDispatcher{}
	api := scheduledchat.New(scheduledchat.Config{
		Store:      store,
		Dispatcher: disp,
	})

	entry, _ := api.Create(context.Background(), scheduledchat.CreateInput{
		Name:           "Run now test",
		PromptTemplate: "Hello {{date}}",
		Cron:           "0 9 * * *",
		Enabled:        true,
	})

	summary, err := api.RunNow(context.Background(), entry.ID)
	if err != nil {
		t.Fatalf("RunNow: %v", err)
	}
	if summary.Status != "completed" {
		t.Errorf("status=%q, want completed", summary.Status)
	}
	if summary.ChatRunID != entry.ID {
		t.Errorf("ChatRunID=%q, want %q", summary.ChatRunID, entry.ID)
	}

	// History should have one record.
	history, err := api.History(context.Background(), entry.ID, 10)
	if err != nil {
		t.Fatalf("History: %v", err)
	}
	if len(history) != 1 {
		t.Errorf("history len=%d, want 1", len(history))
	}
}

// TestRunNowNilDispatcher is AC-002: a nil Dispatcher must return an error
// and append no history row — not a fabricated "completed" outcome.
// Mutation: restore the `d == nil` fallback to NoopChatRunDispatcher (or
// equivalent). This test must fail against that mutation.
func TestRunNowNilDispatcher(t *testing.T) {
	store := newFakeStore()
	api := scheduledchat.New(scheduledchat.Config{
		Store: store,
		// Dispatcher intentionally left nil.
	})

	entry, _ := api.Create(context.Background(), scheduledchat.CreateInput{
		Name:           "No dispatcher wired",
		PromptTemplate: "Hello {{date}}",
		Cron:           "0 9 * * *",
		Enabled:        true,
	})

	_, err := api.RunNow(context.Background(), entry.ID)
	if !errors.Is(err, scheduledchat.ErrDispatcherUnavailable) {
		t.Fatalf("RunNow with nil Dispatcher: want ErrDispatcherUnavailable, got %v", err)
	}

	history, herr := api.History(context.Background(), entry.ID, 10)
	if herr != nil {
		t.Fatalf("History: %v", herr)
	}
	if len(history) != 0 {
		t.Errorf("history len=%d, want 0 (no row for a run that did not happen)", len(history))
	}
}

// TestRunNowCedarDenialStaysCedarDenial ensures a denying Cedar gate is
// still reported as ErrCedarDenied, not shadowed by the dispatcher check —
// a denial must stay a denial, distinguishable from "no dispatcher wired".
func TestRunNowCedarDenialStaysCedarDenial(t *testing.T) {
	store := newFakeStore()
	api := scheduledchat.New(scheduledchat.Config{
		Store:      store,
		Dispatcher: &fakeDispatcher{},
		Cedar:      denyAllGate{},
	})

	// Create goes through the same gate, so seed the row directly through
	// the store instead of via api.Create.
	if err := store.Create(context.Background(), scheduler.ChatRunRecord{
		ID: "seeded", Cron: "0 9 * * *", Enabled: true,
	}); err != nil {
		t.Fatalf("seed store.Create: %v", err)
	}

	_, err := api.RunNow(context.Background(), "seeded")
	if !errors.Is(err, scheduledchat.ErrCedarDenied) {
		t.Fatalf("RunNow under a denying gate: want ErrCedarDenied, got %v", err)
	}
}

func TestRunNowNotFound(t *testing.T) {
	api := scheduledchat.New(scheduledchat.Config{Store: newFakeStore()})
	_, err := api.RunNow(context.Background(), "nonexistent")
	if !errors.Is(err, scheduledchat.ErrNotFound) {
		t.Errorf("want ErrNotFound, got %v", err)
	}
}

func TestStoreUnavailable(t *testing.T) {
	api := scheduledchat.New(scheduledchat.Config{})
	ctx := context.Background()

	if _, err := api.Create(ctx, scheduledchat.CreateInput{Cron: "0 9 * * *"}); !errors.Is(err, scheduledchat.ErrStoreUnavailable) {
		t.Errorf("Create: want ErrStoreUnavailable, got %v", err)
	}
	if list, err := api.List(ctx); err != nil || len(list) != 0 {
		t.Errorf("List with nil store should return empty slice, got %v %v", list, err)
	}
}

func TestHistoryDefaultLimit(t *testing.T) {
	store := newFakeStore()
	api := scheduledchat.New(scheduledchat.Config{Store: store})

	entry, _ := api.Create(context.Background(), scheduledchat.CreateInput{
		Name: "hist", Cron: "0 9 * * *", Enabled: true,
	})

	// Add 3 history rows manually via store.
	for i := 0; i < 3; i++ {
		now := time.Now().UTC()
		_ = store.AppendHistory(context.Background(), scheduler.ChatRunHistoryRecord{
			ID:        "h" + string(rune('0'+i)),
			ChatRunID: entry.ID,
			Status:    "completed",
			StartedAt: now,
		})
	}

	hist, err := api.History(context.Background(), entry.ID, 0)
	if err != nil {
		t.Fatalf("History: %v", err)
	}
	if len(hist) != 3 {
		t.Errorf("got %d rows, want 3", len(hist))
	}
}

// ── WP09: provenance + Cedar context injection ──────────────────────────
//
// AC-011 (FR-005). "Create a schedule through the model-facing path with
// created_by set to 'user' in the request payload; assert the persisted
// row says 'model'" — CreateInput has no created_by field to set, so the
// "payload" half of that scenario is proven by construction: there is
// nothing to smuggle. What these tests assert instead is the shape that
// makes that true: Create always stamps "user", CreateAsModel always
// stamps "model", and neither reads anything from the caller to decide.

func TestCreateStampsCreatedByUser(t *testing.T) {
	store := newFakeStore()
	api := scheduledchat.New(scheduledchat.Config{Store: store})

	entry, err := api.Create(context.Background(), scheduledchat.CreateInput{
		Name: "user schedule", Cron: "0 9 * * *", Enabled: true,
	})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if entry.CreatedBy != "user" {
		t.Errorf("CreatedBy=%q, want %q", entry.CreatedBy, "user")
	}

	// Re-Get to prove it round-trips through the store, not just the
	// in-memory return value.
	got, err := api.Get(context.Background(), entry.ID)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got.CreatedBy != "user" {
		t.Errorf("Get.CreatedBy=%q, want %q", got.CreatedBy, "user")
	}
}

// TestCreateAsModelRequiresToolAllowlist is F1's create-time half (owner
// ruling B-3): a model-created schedule with no declared allowlist must
// not even be creatable, defense-in-depth ahead of the fire-time Cedar
// check in core/policy/cedar.GateScheduledChatExecute.
func TestCreateAsModelRequiresToolAllowlist(t *testing.T) {
	store := newFakeStore()
	api := scheduledchat.New(scheduledchat.Config{Store: store})

	_, err := api.CreateAsModel(context.Background(), scheduledchat.CreateInput{
		Name: "model schedule, no allowlist", Cron: "0 9 * * *", Enabled: true,
	})
	if !errors.Is(err, scheduledchat.ErrInvalidInput) {
		t.Fatalf("CreateAsModel with empty ToolAllowlist: want ErrInvalidInput, got %v", err)
	}

	list, lerr := api.List(context.Background())
	if lerr != nil {
		t.Fatalf("List: %v", lerr)
	}
	if len(list) != 0 {
		t.Errorf("a refused CreateAsModel must not persist a row; got %d", len(list))
	}
}

// TestCreateAsModelStampsCreatedByModel is the paired positive: a
// declared allowlist lets CreateAsModel succeed, and the persisted row
// carries both created_by="model" and the allowlist.
func TestCreateAsModelStampsCreatedByModel(t *testing.T) {
	store := newFakeStore()
	api := scheduledchat.New(scheduledchat.Config{Store: store})

	entry, err := api.CreateAsModel(context.Background(), scheduledchat.CreateInput{
		Name:          "model schedule",
		Cron:          "0 9 * * *",
		Enabled:       true,
		ToolAllowlist: []string{"kenaz__web_fetch"},
	})
	if err != nil {
		t.Fatalf("CreateAsModel: %v", err)
	}
	if entry.CreatedBy != "model" {
		t.Errorf("CreatedBy=%q, want %q", entry.CreatedBy, "model")
	}
	if len(entry.ToolAllowlist) != 1 || entry.ToolAllowlist[0] != "kenaz__web_fetch" {
		t.Errorf("ToolAllowlist=%v, want [kenaz__web_fetch]", entry.ToolAllowlist)
	}
}

// TestUpdateCannotChangeCreatedBy: a row's provenance is immutable
// post-create. Update has no way to smuggle a different created_by in
// (UpdateInput has no such field either), and the store layer's Update
// SQL statement omits the column entirely — this test asserts the
// end-to-end behaviour, not just the SQL shape.
func TestUpdateCannotChangeCreatedBy(t *testing.T) {
	store := newFakeStore()
	api := scheduledchat.New(scheduledchat.Config{Store: store})

	entry, err := api.CreateAsModel(context.Background(), scheduledchat.CreateInput{
		Name: "immutable provenance", Cron: "0 9 * * *", Enabled: true,
		ToolAllowlist: []string{"kenaz__web_fetch"},
	})
	if err != nil {
		t.Fatalf("CreateAsModel: %v", err)
	}
	if entry.CreatedBy != "model" {
		t.Fatalf("precondition failed: CreatedBy=%q, want model", entry.CreatedBy)
	}

	updated, err := api.Update(context.Background(), scheduledchat.UpdateInput{
		ID: entry.ID, Name: "renamed", Cron: "0 10 * * *", Enabled: true,
	})
	if err != nil {
		t.Fatalf("Update: %v", err)
	}
	if updated.CreatedBy != "model" {
		t.Errorf("after Update, CreatedBy=%q, want it to still be %q (immutable)", updated.CreatedBy, "model")
	}
}

// recordingGate is a cedar.Gate stub that records the contextAttrs of
// its most recent Evaluate call, so a test can assert what RunNow
// actually sent to Cedar — not just that some call happened.
type recordingGate struct {
	mu       sync.Mutex
	lastCtx  map[cedargo.String]cedargo.Value
	lastCall int
}

func (g *recordingGate) Evaluate(_ context.Context, _ cedargo.EntityUID, action string, _ cedargo.EntityUID, contextAttrs map[cedargo.String]cedargo.Value) cedar.Decision {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.lastCtx = contextAttrs
	g.lastCall++
	return cedar.Decision{Outcome: cedar.Allow, Action: action, Reason: "recordingGate: test stub"}
}

var _ cedar.Gate = (*recordingGate)(nil)

// TestRunNowInjectsCreatedByAndAllowlistIntoCedarContext proves the
// context attribute actually reaches the Cedar call at the RunNow site
// — not just that GateScheduledChatExecute's own unit tests (in
// core/policy/cedar) build the map correctly in isolation.
func TestRunNowInjectsCreatedByAndAllowlistIntoCedarContext(t *testing.T) {
	store := newFakeStore()
	gate := &recordingGate{}
	api := scheduledchat.New(scheduledchat.Config{
		Store:      store,
		Dispatcher: &fakeDispatcher{},
		Cedar:      gate,
	})

	entry, err := api.CreateAsModel(context.Background(), scheduledchat.CreateInput{
		Name: "context probe", Cron: "0 9 * * *", Enabled: true,
		ToolAllowlist: []string{"kenaz__web_fetch"},
	})
	if err != nil {
		t.Fatalf("CreateAsModel: %v", err)
	}

	if _, err := api.RunNow(context.Background(), entry.ID); err != nil {
		t.Fatalf("RunNow: %v", err)
	}

	gate.mu.Lock()
	defer gate.mu.Unlock()
	if gate.lastCtx == nil {
		t.Fatal("Cedar Evaluate was not called with any context attrs")
	}
	if v, ok := gate.lastCtx[cedargo.String("created_by")]; !ok || v != cedargo.String("model") {
		t.Errorf("context[created_by] = %v (ok=%v), want %q", v, ok, "model")
	}
	if v, ok := gate.lastCtx[cedargo.String("has_tool_allowlist")]; !ok || v != cedargo.Boolean(true) {
		t.Errorf("context[has_tool_allowlist] = %v (ok=%v), want true", v, ok)
	}
}
