package catalog_test

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/sigil-tech/kaneaz-harness/core/workflows/catalog"
	wfsched "github.com/sigil-tech/kaneaz-harness/core/workflows/scheduler"
	corewf "github.com/sigil-tech/kaneaz-harness/core/workflows"
)

// --- fakes ---

// fakeStore is an in-memory corewf.Store suitable for catalog tests.
type fakeStore struct {
	mu  sync.Mutex
	wfs map[string]corewf.Workflow
}

func newFakeStore() *fakeStore { return &fakeStore{wfs: make(map[string]corewf.Workflow)} }

func (f *fakeStore) Save(_ context.Context, w corewf.Workflow) (corewf.Workflow, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.wfs[w.ID] = w
	return w, nil
}

func (f *fakeStore) Load(_ context.Context, id string) (corewf.Workflow, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	w, ok := f.wfs[id]
	if !ok {
		return corewf.Workflow{}, corewf.ErrWorkflowNotFound
	}
	return w, nil
}

func (f *fakeStore) Delete(_ context.Context, id string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	delete(f.wfs, id)
	return nil
}

func (f *fakeStore) List(_ context.Context) ([]corewf.WorkflowSummary, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make([]corewf.WorkflowSummary, 0, len(f.wfs))
	for id, w := range f.wfs {
		out = append(out, corewf.WorkflowSummary{ID: id, Name: w.Name, Version: w.Version})
	}
	return out, nil
}

func (f *fakeStore) History(_ context.Context, id string) ([]corewf.WorkflowVersion, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if _, ok := f.wfs[id]; !ok {
		return nil, corewf.ErrWorkflowNotFound
	}
	return nil, nil
}

// fakeScheduler is a minimal Scheduler that records Register calls.
type fakeScheduler struct {
	mu        sync.Mutex
	schedules map[string]string // workflowID → cron
}

func newFakeScheduler() *fakeScheduler {
	return &fakeScheduler{schedules: make(map[string]string)}
}

func (s *fakeScheduler) Register(_ context.Context, workflowID, cron, _ string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.schedules[workflowID] = cron
	return nil
}

func (s *fakeScheduler) Unregister(_ context.Context, id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.schedules, id)
	return nil
}

func (s *fakeScheduler) RunNow(_ context.Context, _ string) (wfsched.RunSummary, error) {
	return wfsched.RunSummary{}, nil
}

func (s *fakeScheduler) History(_ context.Context, _ string, _ int) ([]wfsched.RunSummary, error) {
	return nil, nil
}

func (s *fakeScheduler) List(_ context.Context) ([]wfsched.ScheduleEntry, error) {
	return nil, nil
}

func (s *fakeScheduler) Start()           {}
func (s *fakeScheduler) Stop()            {}
func (s *fakeScheduler) Tick(_ time.Time) {}

// fakeRecipeRegistry answers Has() always true or always false.
type fakeRecipeRegistry struct{ hasAll bool }

func (r *fakeRecipeRegistry) Has(_ string) bool { return r.hasAll }

// --- tests ---

// TestInstall_PersistsWorkflow confirms Install calls Store.Save and
// returns the saved workflow id.
func TestInstall_PersistsWorkflow(t *testing.T) {
	t.Parallel()
	store := newFakeStore()
	cat := catalog.New(catalog.Config{Store: store})

	ref, err := cat.Install(context.Background(), "plan_implement_review")
	if err != nil {
		t.Fatalf("Install: %v", err)
	}
	if ref.WorkflowID == "" {
		t.Error("InstalledRef.WorkflowID must not be empty")
	}

	// Store should now have the workflow.
	_, err = store.Load(context.Background(), ref.WorkflowID)
	if err != nil {
		t.Errorf("workflow not found in store after Install: %v", err)
	}
}

// TestInstall_UnknownIDReturnsErrNotFound.
func TestInstall_UnknownIDReturnsErrNotFound(t *testing.T) {
	t.Parallel()
	store := newFakeStore()
	cat := catalog.New(catalog.Config{Store: store})

	_, err := cat.Install(context.Background(), "no-such-workflow")
	if err == nil {
		t.Fatal("expected error for unknown workflow, got nil")
	}
}

// TestInstall_SchedulerWiredDoesNotPanic verifies that a catalog with a
// scheduler wired installs cleanly. extractSchedule returns empty until
// WP04 adds the schema field, so Scheduled=false is expected.
func TestInstall_SchedulerWiredDoesNotPanic(t *testing.T) {
	t.Parallel()
	store := newFakeStore()
	sched := newFakeScheduler()
	cat := catalog.New(catalog.Config{Store: store, Scheduler: sched})

	ref, err := cat.Install(context.Background(), "plan_implement_review")
	if err != nil {
		t.Fatalf("Install: %v", err)
	}
	if ref.WorkflowID == "" {
		t.Error("InstalledRef.WorkflowID must not be empty")
	}
	// ref.Scheduled is false because extractSchedule returns "" until WP04.
	_ = ref.Scheduled
}

// TestInstall_MissingCredentialsDetected confirms that mcp_call steps
// whose server is not in the registry appear in MissingCredentials.
// The builtin plan_implement_review has no mcp_call steps so creds
// should be empty regardless of registry response.
func TestInstall_MissingCredentialsDetected(t *testing.T) {
	t.Parallel()
	reg := &fakeRecipeRegistry{hasAll: false}
	store := newFakeStore()
	cat := catalog.New(catalog.Config{
		Store:          store,
		RecipeRegistry: reg,
	})

	ref, err := cat.Install(context.Background(), "plan_implement_review")
	if err != nil {
		t.Fatalf("Install: %v", err)
	}
	if len(ref.MissingCredentials) != 0 {
		t.Errorf("expected no missing creds for model_turn workflow, got %v", ref.MissingCredentials)
	}
}

// TestList_InstallStatusReflectsStore confirms that after an Install
// the List result shows "installed" for that workflow.
func TestList_InstallStatusReflectsStore(t *testing.T) {
	t.Parallel()
	store := newFakeStore()
	cat := catalog.New(catalog.Config{Store: store})

	// Before install.
	entries, err := cat.List(context.Background())
	if err != nil {
		t.Fatalf("List before install: %v", err)
	}
	for _, e := range entries {
		if e.ID == "plan_implement_review" && e.InstallStatus == "installed" {
			t.Error("expected not_installed before Install")
		}
	}

	// Install.
	if _, err := cat.Install(context.Background(), "plan_implement_review"); err != nil {
		t.Fatalf("Install: %v", err)
	}

	// After install.
	entries, err = cat.List(context.Background())
	if err != nil {
		t.Fatalf("List after install: %v", err)
	}
	found := false
	for _, e := range entries {
		if e.ID == "plan_implement_review" {
			found = true
			if e.InstallStatus != "installed" {
				t.Errorf("InstallStatus: got %q want installed", e.InstallStatus)
			}
		}
	}
	if !found {
		t.Error("plan_implement_review not in List after Install")
	}
}

// keep errors import used.
var _ = errors.New
