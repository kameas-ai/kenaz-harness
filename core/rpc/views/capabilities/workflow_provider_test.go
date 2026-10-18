package capabilities_test

import (
	"context"
	"errors"
	"strconv"
	"strings"
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
func (s *recordingScheduler) List(context.Context) ([]wfsched.ScheduleEntry, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	var out []wfsched.ScheduleEntry
	for id, cron := range s.cron {
		out = append(out, wfsched.ScheduleEntry{WorkflowID: id, Cron: cron, Timezone: "UTC", Enabled: true})
	}
	return out, nil
}
func (s *recordingScheduler) Start() {}
func (s *recordingScheduler) Stop()  {}
func (s *recordingScheduler) NextFire(context.Context, string) (time.Time, error) {
	return time.Time{}, nil
}
func (s *recordingScheduler) Tick(time.Time) {}

type workflowFixture struct {
	db    storage.DB
	store corewf.Store
	sched *recordingScheduler
	prov  corewf.ProvenanceStore
	wf    *workflowsview.API
	cat   *fakeFleetCatalog
	fw    *install.Framework
	pub   *recordingPublisher
}

// newWorkflowsAPIWith builds the workflows view + catalog the way rpc.New
// does: one provenance store shared by both. builtins nil → the embedded
// templates.
func newWorkflowsAPIWith(store corewf.Store, sched wfsched.Scheduler, prov corewf.ProvenanceStore, builtins []corewf.Workflow) *workflowsview.API {
	if builtins == nil {
		builtins, _ = corewf.LoadBuiltins()
	}
	return workflowsview.New(workflowsview.Config{
		Catalog:         builtins,
		Store:           store,
		Scheduler:       sched,
		Provenance:      prov,
		WorkflowCatalog: wfcatalog.New(wfcatalog.Config{Store: store, Scheduler: sched, Provenance: prov, Builtins: builtins}),
	})
}

func newWorkflowsAPI(store corewf.Store, sched wfsched.Scheduler) *workflowsview.API {
	return newWorkflowsAPIWith(store, sched, corewf.NewMemoryProvenanceStore(), nil)
}

func newWorkflowFixture(t *testing.T) *workflowFixture {
	t.Helper()
	return newWorkflowFixtureWith(t, nil)
}

func newWorkflowFixtureWith(t *testing.T, builtins []corewf.Workflow) *workflowFixture {
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
	prov := corewf.NewFileProvenanceStore(t.TempDir())
	wf := newWorkflowsAPIWith(store, sched, prov, builtins)
	cat := newFakeFleetCatalog()
	pub := &recordingPublisher{}
	fw := install.New(pub, c2Verifier)
	if err := fw.Register(install.KindWorkflow, capabilities.NewWorkflowProvider(wf, cat)); err != nil {
		t.Fatal(err)
	}
	return &workflowFixture{db: db, store: store, sched: sched, prov: prov, wf: wf, cat: cat, fw: fw, pub: pub}
}

// listedAsInstalled asks the consumer FR-1 names — Workflows_List — and,
// separately, a FRESH workflows view over the same sqlite store (what the
// next app start sees).
func (f *workflowFixture) listedAsInstalled(t *testing.T, id string) (live, afterRestart bool) {
	t.Helper()
	for _, api := range []*workflowsview.API{f.wf, newWorkflowsAPIWith(f.store, f.sched, f.prov, nil)} {
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
	if !errors.Is(err, workflowsview.ErrWorkflowIDMismatch) {
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

// ── review H1/H2: install provenance + collision refusal ───────────────────

func yamlOf(t *testing.T, f *workflowFixture, id string) string {
	t.Helper()
	w, err := f.store.Load(context.Background(), id)
	if err != nil {
		t.Fatalf("Load(%s): %v", id, err)
	}
	return w.YAMLSource()
}

func flowYAML(id, marker string) []byte {
	return []byte("id: " + id + "\nname: \"" + marker + "\"\nversion: 1\nschedule: \"0 9 * * 1\"\ntimezone: \"UTC\"\nsteps:\n  - name: a\n    kind: shell\n    cmd: \"echo\"\n    args: [\"" + marker + "\"]\n")
}

// H1: a fleet payload whose id is a workflow the USER authored is refused
// before anything is written — the user's row is byte-identical afterwards,
// still listed, and no schedule was armed. (Before the fix the payload
// overwrote it, and a slug-mismatch "rollback" could delete it.)
func TestWorkflowProvider_RefusedInstallLeavesUserWorkflowByteIdentical(t *testing.T) {
	f := newWorkflowFixture(t)
	ctx := context.Background()
	if _, err := f.wf.Save(ctx, workflowsview.SaveInput{Workflow: &workflowsview.Workflow{
		ID: "team-digest", Name: "My own digest", Version: 1,
		Steps: []workflowsview.Step{{Name: "mine", Kind: "shell", Cmd: "echo", Args: []string{"mine"}}},
	}}); err != nil {
		t.Fatalf("user Save: %v", err)
	}
	before := yamlOf(t, f, "team-digest")

	f.cat.publish("workflow", capabilities.CatalogEntry{ID: "cat-td", Slug: "team-digest", Version: "1.0.0"}, flowYAML("team-digest", "attacker"))
	_, err := f.fw.Install(ctx, install.Ref{Kind: install.KindWorkflow, ID: "cat-td", Version: "1.0.0"}, install.Inputs{})
	if !errors.Is(err, workflowsview.ErrWorkflowIDCollision) {
		t.Fatalf("got %v, want ErrWorkflowIDCollision", err)
	}
	if after := yamlOf(t, f, "team-digest"); after != before {
		t.Fatalf("the user's workflow changed:\nbefore:\n%s\nafter:\n%s", before, after)
	}
	if f.sched.armed("team-digest") {
		t.Fatal("a refused install armed a schedule")
	}
	if _, evs := f.pub.snapshot(); len(evs) != 0 {
		t.Fatalf("a refused install announced: %+v", evs)
	}
}

// H2: a fleet payload carrying a shipped template's id is refused — it can
// neither persist nor (via persisted-copy-wins) replace the template with
// its own steps and schedule.
func TestWorkflowProvider_BuiltinIDPayloadRefused(t *testing.T) {
	f := newWorkflowFixture(t)
	ctx := context.Background()
	f.cat.publish("workflow", capabilities.CatalogEntry{ID: "cat-evil", Slug: "daily_ea_briefing", Version: "1.0.0"}, flowYAML("daily_ea_briefing", "attacker"))
	_, err := f.fw.Install(ctx, install.Ref{Kind: install.KindWorkflow, ID: "cat-evil", Version: "1.0.0"}, install.Inputs{})
	if !errors.Is(err, workflowsview.ErrWorkflowIDCollision) {
		t.Fatalf("got %v, want ErrWorkflowIDCollision", err)
	}
	if _, lerr := f.store.Load(ctx, "daily_ea_briefing"); !errors.Is(lerr, corewf.ErrWorkflowNotFound) {
		t.Fatalf("the template id was persisted by a fleet payload: %v", lerr)
	}
	if f.sched.armed("daily_ea_briefing") {
		t.Fatal("the attacker's schedule was armed")
	}
}

// H1: catalog item B may not overwrite the workflow catalog item A installed.
func TestWorkflowProvider_CrossItemCollisionRefused(t *testing.T) {
	f := newWorkflowFixture(t)
	ctx := context.Background()
	f.cat.publish("workflow", capabilities.CatalogEntry{ID: "cat-a", Slug: "shared-flow", Version: "1.0.0"}, flowYAML("shared-flow", "from-a"))
	if _, err := f.fw.Install(ctx, install.Ref{Kind: install.KindWorkflow, ID: "cat-a", Version: "1.0.0"}, install.Inputs{}); err != nil {
		t.Fatalf("install A: %v", err)
	}
	before := yamlOf(t, f, "shared-flow")
	f.cat.publish("workflow", capabilities.CatalogEntry{ID: "cat-b", Slug: "shared-flow", Version: "1.0.0"}, flowYAML("shared-flow", "from-b"))
	_, err := f.fw.Install(ctx, install.Ref{Kind: install.KindWorkflow, ID: "cat-b", Version: "1.0.0"}, install.Inputs{})
	if !errors.Is(err, workflowsview.ErrWorkflowIDCollision) {
		t.Fatalf("got %v, want ErrWorkflowIDCollision", err)
	}
	if after := yamlOf(t, f, "shared-flow"); after != before {
		t.Fatal("catalog item B overwrote catalog item A's workflow")
	}
}

// H1: a catalog item re-installing (a newer version of) its OWN workflow
// updates it.
func TestWorkflowProvider_OwnItemReinstallUpdates(t *testing.T) {
	f := newWorkflowFixture(t)
	ctx := context.Background()
	f.cat.publish("workflow", capabilities.CatalogEntry{ID: "cat-a", Slug: "own-flow", Version: "1.0.0"}, flowYAML("own-flow", "v1"))
	if _, err := f.fw.Install(ctx, install.Ref{Kind: install.KindWorkflow, ID: "cat-a", Version: "1.0.0"}, install.Inputs{}); err != nil {
		t.Fatalf("install v1: %v", err)
	}
	f.cat.publish("workflow", capabilities.CatalogEntry{ID: "cat-a", Slug: "own-flow", Version: "2.0.0"}, flowYAML("own-flow", "v2"))
	if _, err := f.fw.Install(ctx, install.Ref{Kind: install.KindWorkflow, ID: "cat-a", Version: "2.0.0"}, install.Inputs{}); err != nil {
		t.Fatalf("reinstall v2: %v", err)
	}
	if y := yamlOf(t, f, "own-flow"); !strings.Contains(y, "v2") || strings.Contains(y, "\"v1\"") {
		t.Fatalf("own reinstall did not update the body:\n%s", y)
	}
	p, ok, err := f.prov.Get("own-flow")
	if err != nil || !ok || p.CatalogID != "cat-a" || p.Version != "2.0.0" {
		t.Fatalf("provenance = %+v, %v, %v", p, ok, err)
	}
}

// ── review H4: template updates ────────────────────────────────────────────

func templateFixture(t *testing.T, version int, marker string) corewf.Workflow {
	t.Helper()
	w, err := corewf.LoadYAML([]byte("id: tpl_digest\nname: \"Template digest\"\nversion: " + strconv.Itoa(version) +
		"\nschedule: \"0 7 * * *\"\ntimezone: \"UTC\"\nsteps:\n  - name: a\n    kind: shell\n    cmd: \"echo\"\n    args: [\"" + marker + "\"]\n"))
	if err != nil {
		t.Fatalf("fixture: %v", err)
	}
	return w
}

// H4: install a template, ship a newer version, see UpdateAvailable, Update
// → the new body lands and the user's schedule state is kept.
func TestWorkflowProvider_TemplateUpdatePreservesSchedule(t *testing.T) {
	for _, tc := range []struct {
		name      string
		userCron  string // "" = the user cleared the schedule
		wantArmed bool
	}{
		{name: "user rescheduled", userCron: "30 6 * * 1-5", wantArmed: true},
		{name: "user cleared the schedule", userCron: "", wantArmed: false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := newWorkflowFixtureWith(t, []corewf.Workflow{templateFixture(t, 1, "body-v1")})
			ctx := context.Background()
			if _, err := f.fw.Install(ctx, install.Ref{Kind: install.KindWorkflow, ID: "tpl_digest"}, install.Inputs{}); err != nil {
				t.Fatalf("install: %v", err)
			}
			if tc.userCron == "" {
				_ = f.sched.Unregister(ctx, "tpl_digest")
			} else {
				_ = f.sched.Register(ctx, "tpl_digest", tc.userCron, "UTC")
			}

			// Same store, provenance and scheduler; the binary now ships v2.
			wf2 := newWorkflowsAPIWith(f.store, f.sched, f.prov, []corewf.Workflow{templateFixture(t, 2, "body-v2")})
			fw2 := install.New(&recordingPublisher{}, c2Verifier)
			if err := fw2.Register(install.KindWorkflow, capabilities.NewWorkflowProvider(wf2, nil)); err != nil {
				t.Fatal(err)
			}
			it, err := fw2.Detail(ctx, install.KindWorkflow, "tpl_digest")
			if err != nil || !it.State.UpdateAvailable {
				t.Fatalf("row = %+v, %v — want UpdateAvailable after the shipped version moved", it, err)
			}
			if _, err := fw2.Update(ctx, install.KindWorkflow, "tpl_digest"); err != nil {
				t.Fatalf("Update: %v", err)
			}
			if y := yamlOf(t, f, "tpl_digest"); !strings.Contains(y, "body-v2") {
				t.Fatalf("Update did not apply the new template body:\n%s", y)
			}
			f.sched.mu.Lock()
			cron, armed := f.sched.cron["tpl_digest"]
			f.sched.mu.Unlock()
			if armed != tc.wantArmed || (tc.wantArmed && cron != tc.userCron) {
				t.Fatalf("schedule after update: armed=%v cron=%q, want armed=%v cron=%q", armed, cron, tc.wantArmed, tc.userCron)
			}
			if it, _ := fw2.Detail(ctx, install.KindWorkflow, "tpl_digest"); it.State.UpdateAvailable {
				t.Fatal("still UpdateAvailable after Update")
			}
		})
	}
}

// Re-review low 4: a fleet workflow's own-item update keeps the user's
// schedule state, like a template update — a cleared schedule stays
// cleared, a rescheduled one keeps the user's cron.
func TestWorkflowProvider_OwnItemUpdateKeepsScheduleState(t *testing.T) {
	for _, tc := range []struct {
		name     string
		userCron string
	}{{"cleared", ""}, {"rescheduled", "15 8 * * *"}} {
		t.Run(tc.name, func(t *testing.T) {
			f := newWorkflowFixture(t)
			ctx := context.Background()
			f.cat.publish("workflow", capabilities.CatalogEntry{ID: "cat-a", Slug: "own-flow", Version: "1.0.0"}, flowYAML("own-flow", "v1"))
			if _, err := f.fw.Install(ctx, install.Ref{Kind: install.KindWorkflow, ID: "cat-a", Version: "1.0.0"}, install.Inputs{}); err != nil {
				t.Fatal(err)
			}
			if !f.sched.armed("own-flow") {
				t.Fatal("a new install should arm the document's schedule")
			}
			if tc.userCron == "" {
				_ = f.sched.Unregister(ctx, "own-flow")
			} else {
				_ = f.sched.Register(ctx, "own-flow", tc.userCron, "UTC")
			}
			f.cat.publish("workflow", capabilities.CatalogEntry{ID: "cat-a", Slug: "own-flow", Version: "2.0.0"}, flowYAML("own-flow", "v2"))
			if _, err := f.fw.Install(ctx, install.Ref{Kind: install.KindWorkflow, ID: "cat-a", Version: "2.0.0"}, install.Inputs{}); err != nil {
				t.Fatal(err)
			}
			f.sched.mu.Lock()
			cron, armed := f.sched.cron["own-flow"]
			f.sched.mu.Unlock()
			if tc.userCron == "" && armed {
				t.Fatal("the update re-armed a schedule the user cleared")
			}
			if tc.userCron != "" && cron != tc.userCron {
				t.Fatalf("cron after update = %q, want the user's %q", cron, tc.userCron)
			}
		})
	}
}
