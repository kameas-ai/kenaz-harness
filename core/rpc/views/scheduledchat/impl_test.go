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
