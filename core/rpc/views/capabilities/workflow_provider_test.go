package capabilities_test

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/kameas-ai/kenaz-harness/core/install"
	"github.com/kameas-ai/kenaz-harness/core/rpc/views/capabilities"
	workflowsview "github.com/kameas-ai/kenaz-harness/core/rpc/views/workflows"
	"github.com/kameas-ai/kenaz-harness/core/storage"
	storagesqlite "github.com/kameas-ai/kenaz-harness/core/storage/sqlite"
	corewf "github.com/kameas-ai/kenaz-harness/core/workflows"
	wfcatalog "github.com/kameas-ai/kenaz-harness/core/workflows/catalog"
	wfsched "github.com/kameas-ai/kenaz-harness/core/workflows/scheduler"

	_ "modernc.org/sqlite"
)

// recordingScheduler stands in for the cron engine (no goroutines); the
// store is REAL sqlite (WP-PI AC-PI-2: workflow install asserts persistence,
// so it must round-trip through SQL, not an in-memory fake store).
type recordingScheduler struct {
	mu   sync.Mutex
	cron map[string]string
}

func (s *recordingScheduler) Register(_ context.Context, id, cron, _ string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.cron[id] = cron
	return nil
}

func (s *recordingScheduler) Unregister(_ context.Context, id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.cron, id)
	return nil
}

func (s *recordingScheduler) armed(id string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	_, ok := s.cron[id]
	return ok
}

func (s *recordingScheduler) RunNow(context.Context, string) (wfsched.RunSummary, error) {
	return wfsched.RunSummary{}, nil
}
func (s *recordingScheduler) History(context.Context, string, int) ([]wfsched.RunSummary, error) {
	return nil, nil
}
func (s *recordingScheduler) List(context.Context) ([]wfsched.ScheduleEntry, error) { return nil, nil }
func (s *recordingScheduler) Start()                                                {}
func (s *recordingScheduler) Stop()                                                 {}
func (s *recordingScheduler) NextFire(context.Context, string) (time.Time, error) {
	return time.Time{}, nil
}
func (s *recordingScheduler) Tick(time.Time) {}

type workflowFixture struct {
	db    storage.DB
	store corewf.Store
	sched *recordingScheduler
	wf    *workflowsview.API
	cat   *fakeFleetCatalog
	fw    *install.Framework
	pub   *recordingPublisher
}

func newWorkflowsAPI(store corewf.Store, sched wfsched.Scheduler) *workflowsview.API {
	builtins, _ := corewf.LoadBuiltins()
	return workflowsview.New(workflowsview.Config{
		Catalog:         builtins,
		Store:           store,
		Scheduler:       sched,
		WorkflowCatalog: wfcatalog.New(wfcatalog.Config{Store: store, Scheduler: sched}),
	})
}

func newWorkflowFixture(t *testing.T) *workflowFixture {
	t.Helper()
	db, err := storagesqlite.Open(storage.Config{
		DataDir:          t.TempDir(),
		EncryptionStatus: storage.EncryptionStatusDisabledWithDiskEncryption,
	})
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() { _ = db.Close(context.Background()) })
	store := corewf.NewSQLiteStore(db)
	sched := &recordingScheduler{cron: map[string]string{}}
	wf := newWorkflowsAPI(store, sched)
	cat := newFakeFleetCatalog()
	pub := &recordingPublisher{}
	fw := install.New(pub, c2Verifier)
	if err := fw.Register(install.KindWorkflow, capabilities.NewWorkflowProvider(wf, cat)); err != nil {
		t.Fatal(err)
	}
	return &workflowFixture{db: db, store: store, sched: sched, wf: wf, cat: cat, fw: fw, pub: pub}
}

// listedAsInstalled asks the consumer FR-1 names — Workflows_List — and,
// separately, a FRESH workflows view over the same sqlite store (what the
// next app start sees).
func (f *workflowFixture) listedAsInstalled(t *testing.T, id string) (live, afterRestart bool) {
	t.Helper()
	for _, api := range []*workflowsview.API{f.wf, newWorkflowsAPI(f.store, f.sched)} {
		sums, err := api.List(context.Background())
		if err != nil {
			t.Fatalf("Workflows_List: %v", err)
		}
		found := false
		for _, s := range sums {
			if s.ID == id && s.Source == "user" {
				found = true
			}
		}
		if api == f.wf {
			live = found
		} else {
			afterRestart = found
		}
	}
	return live, afterRestart
}

func (f *workflowFixture) row(t *testing.T, id string) install.Item {
	t.Helper()
	l, err := f.fw.List(context.Background(), install.Filter{Kind: install.KindWorkflow})
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	for _, it := range l.Items {
		if it.ID == id {
			return it
		}
	}
	t.Fatalf("no workflow row %q", id)
	return install.Item{}
}

const teamDigestYAML = `
id: team-digest
name: "Team digest"
version: 1
schedule: "0 9 * * 1"
timezone: "UTC"
steps:
  - name: a
    kind: shell
    cmd: "echo"
    args: ["hello"]
`

// P-3 (workflow): a shipped template (the former Workflows › Catalog) and a
// fleet workflow payload both install into the workflows store — listed by
// Workflows_List now and after a restart — arm their cron schedule, paint
// the row from that state, and drop out on uninstall. Named for
// scripts/ci/check-install-provider-coverage.sh.
func TestInstallProvider_Workflow_ConsumerSeesInstall(t *testing.T) {
	f := newWorkflowFixture(t)
	ctx := context.Background()

	// ── shipped template ──
	const builtin = "daily_ea_briefing"
	if f.row(t, builtin).State.Installed {
		t.Fatal("template reads installed before install")
	}
	res, err := f.fw.Install(ctx, install.Ref{Kind: install.KindWorkflow, ID: builtin}, install.Inputs{})
	if err != nil {
		t.Fatalf("Install(template): %v", err)
	}
	if r, ok := res.Detail.(workflowsview.CatalogInstallResult); !ok || r.WorkflowID != builtin || !r.Scheduled {
		t.Fatalf("template install detail = %#v, want the wfcatalog result with its cron armed", res.Detail)
	}
	if live, restart := f.listedAsInstalled(t, builtin); !live || !restart {
		t.Fatalf("Workflows_List after template install: live=%v afterRestart=%v", live, restart)
	}
	if !f.row(t, builtin).State.Installed || res.Verification.Method != install.VerifyBuiltin {
		t.Fatalf("row = %+v, verification = %+v", f.row(t, builtin), res.Verification)
	}

	// ── fleet workflow payload (FR-2 workflow format: YAML) ──
	f.cat.publish("workflow", capabilities.CatalogEntry{ID: "cat-td", Slug: "team-digest", Version: "1.0.0", Visibility: "team"}, []byte(teamDigestYAML))
	if f.row(t, "cat-td").State.Installed {
		t.Fatal("fleet workflow reads installed before install")
	}
	res, err = f.fw.Install(ctx, install.Ref{Kind: install.KindWorkflow, ID: "cat-td", Version: "1.0.0"}, install.Inputs{})
	if err != nil {
		t.Fatalf("Install(fleet payload): %v", err)
	}
	if res.Verification.Method != install.VerifySignature || res.Verification.Verified {
		t.Fatalf("fleet payload verification = %+v, want signature/unverified (register C-2)", res.Verification)
	}
	if live, restart := f.listedAsInstalled(t, "team-digest"); !live || !restart {
		t.Fatalf("Workflows_List after fleet install: live=%v afterRestart=%v", live, restart)
	}
	if !f.sched.armed("team-digest") {
		t.Fatal("the fleet workflow's schedule was not armed (+cron)")
	}
	if row := f.row(t, "cat-td"); !row.State.Installed || row.Source != install.SourceTeamCatalog {
		t.Fatalf("fleet row = %+v", row)
	}

	// ── uninstall both ──
	for _, id := range []string{builtin, "cat-td"} {
		if err := f.fw.Uninstall(ctx, install.KindWorkflow, id); err != nil {
			t.Fatalf("Uninstall(%s): %v", id, err)
		}
	}
	for _, wfID := range []string{builtin, "team-digest"} {
		if live, restart := f.listedAsInstalled(t, wfID); live || restart {
			t.Fatalf("%s still listed after uninstall: live=%v afterRestart=%v", wfID, live, restart)
		}
		if f.sched.armed(wfID) {
			t.Fatalf("%s schedule still armed after uninstall", wfID)
		}
	}
	topics, _ := f.pub.snapshot()
	want := []string{install.TopicCapabilityInstalled, install.TopicCapabilityInstalled, install.TopicCapabilityUninstalled, install.TopicCapabilityUninstalled}
	if len(topics) != len(want) {
		t.Fatalf("events = %v, want %v", topics, want)
	}
}

// FR-2: the JSON form the Workflows › Publish dialog sends today (the
// corewf json-tag shape) installs too, keeping its id.
func TestWorkflowProvider_JSONPayloadInstalls(t *testing.T) {
	f := newWorkflowFixture(t)
	payload := []byte(`{"id":"json-flow","name":"JSON flow","version":1,"steps":[{"name":"a","kind":"shell","cmd":"echo","args":["x"]}]}`)
	f.cat.publish("workflow", capabilities.CatalogEntry{ID: "cat-json", Slug: "json-flow", Version: "1.0.0"}, payload)
	if _, err := f.fw.Install(context.Background(), install.Ref{Kind: install.KindWorkflow, ID: "cat-json", Version: "1.0.0"}, install.Inputs{}); err != nil {
		t.Fatalf("Install: %v", err)
	}
	if live, restart := f.listedAsInstalled(t, "json-flow"); !live || !restart {
		t.Fatalf("live=%v afterRestart=%v", live, restart)
	}
}

// P-4-shaped (FR-2): an opaque workflow payload is a named error and leaves
// nothing in the store; a payload whose id is not the advertised slug is
// refused and rolled back.
func TestWorkflowProvider_BadPayloadsAreNamedAndLeaveNothing(t *testing.T) {
	f := newWorkflowFixture(t)
	ctx := context.Background()
	f.cat.publish("workflow", capabilities.CatalogEntry{ID: "cat-bad", Slug: "bad", Version: "1.0.0"}, []byte("\x00not a workflow"))
	_, err := f.fw.Install(ctx, install.Ref{Kind: install.KindWorkflow, ID: "cat-bad", Version: "1.0.0"}, install.Inputs{})
	if !errors.Is(err, workflowsview.ErrWorkflowPayloadMalformed) {
		t.Fatalf("malformed: got %v, want ErrWorkflowPayloadMalformed", err)
	}

	f.cat.publish("workflow", capabilities.CatalogEntry{ID: "cat-mm", Slug: "advertised-id", Version: "1.0.0"}, []byte(teamDigestYAML))
	// List once so the provider learns the advertised slug, as the surface does.
	_ = f.row(t, "cat-mm")
	_, err = f.fw.Install(ctx, install.Ref{Kind: install.KindWorkflow, ID: "cat-mm", Version: "1.0.0"}, install.Inputs{})
	if !errors.Is(err, capabilities.ErrWorkflowIDMismatch) {
		t.Fatalf("mismatch: got %v, want ErrWorkflowIDMismatch", err)
	}
	if live, restart := f.listedAsInstalled(t, "team-digest"); live || restart {
		t.Fatal("a refused (mismatched) payload was left installed")
	}
	sums, err := f.store.List(ctx)
	if err != nil || len(sums) != 0 {
		t.Fatalf("store = %+v, %v — refused payloads must leave no rows", sums, err)
	}
	if _, evs := f.pub.snapshot(); len(evs) != 0 {
		t.Fatalf("refused installs announced: %+v", evs)
	}
}

// P-5 (workflow): the shipped templates list with the fleet catalog
// unreachable; the fleet sources are reason rows.
func TestWorkflowProvider_FleetUnreachable_TemplatesStillList(t *testing.T) {
	f := newWorkflowFixture(t)
	f.cat.mu.Lock()
	f.cat.listErr = errors.New("fleet disabled")
	f.cat.reason = "fleet_disabled"
	f.cat.mu.Unlock()
	l, err := f.fw.List(context.Background(), install.Filter{Kind: install.KindWorkflow})
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	builtins := 0
	for _, it := range l.Items {
		if it.Source == install.SourceBuiltin {
			builtins++
		}
	}
	if builtins == 0 {
		t.Fatal("no shipped templates listed while the fleet catalog is unreachable")
	}
	if len(l.Unavailable) != 2 || l.Unavailable[0].Reason != "fleet_disabled" {
		t.Fatalf("unavailable = %+v", l.Unavailable)
	}
}
