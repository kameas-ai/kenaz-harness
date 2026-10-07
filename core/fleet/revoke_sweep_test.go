package fleet

// revoke_sweep_test.go — skill-library-01SKLIB01 WP03: the catalog
// revocation sweep (fleet ruling OQ-5 = H4a). The skill store is the real
// file-backed SkillStore (persistence through real files); the catalog list
// is an httptest server speaking fleet's live list shape.

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	contextaudit "github.com/kameas-ai/kenaz-harness/core/context/audit"
	"github.com/kameas-ai/kenaz-harness/core/slashcmd"
)

// sweepListServer serves GET /api/v1/catalog/list with a mutable status and
// body; every request's raw query is recorded (mutex + snapshot).
type sweepListServer struct {
	mu      sync.Mutex
	status  int
	body    string
	queries []string
}

func (s *sweepListServer) set(status int, body string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.status, s.body = status, body
}

func (s *sweepListServer) calls() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]string(nil), s.queries...)
}

func (s *sweepListServer) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	s.mu.Lock()
	s.queries = append(s.queries, r.URL.RawQuery)
	st, body := s.status, s.body
	s.mu.Unlock()
	if r.URL.Path != "/api/v1/catalog/list" {
		w.WriteHeader(http.StatusNotFound)
		return
	}
	if st == 0 {
		st = http.StatusOK
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(st)
	_, _ = w.Write([]byte(body))
}

// recordingAuditEmitter is a race-safe audit fake.
type recordingAuditEmitter struct {
	mu     sync.Mutex
	events []recordedAudit
}

type recordedAudit struct {
	kind    contextaudit.Kind
	payload any
}

func (e *recordingAuditEmitter) EmitFleetEvent(_ context.Context, kind contextaudit.Kind, payload any) error {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.events = append(e.events, recordedAudit{kind, payload})
	return nil
}

func (e *recordingAuditEmitter) snapshot() []recordedAudit {
	e.mu.Lock()
	defer e.mu.Unlock()
	return append([]recordedAudit(nil), e.events...)
}

// fakeRevocationWorkflows is a race-safe RevocationWorkflows: owner maps a
// workflow id to the catalog id whose user install it still is.
type fakeRevocationWorkflows struct {
	mu      sync.Mutex
	owner   map[string]string // workflow id -> catalog id (user catalog provenance)
	version map[string]string
	listErr error
	removed []string
}

func (f *fakeRevocationWorkflows) CatalogInstalledWorkflows(context.Context) ([]RevocationTarget, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.listErr != nil {
		return nil, f.listErr
	}
	var out []RevocationTarget
	for id, cid := range f.owner {
		out = append(out, RevocationTarget{Kind: CatalogKindWorkflow, CatalogID: cid, Version: f.version[id], LocalID: id})
	}
	return out, nil
}

func (f *fakeRevocationWorkflows) RemoveRevokedWorkflow(_ context.Context, id, cid string) (bool, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.owner[id] != cid {
		return false, nil
	}
	delete(f.owner, id)
	f.removed = append(f.removed, id)
	return true, nil
}

func (f *fakeRevocationWorkflows) snapshotRemoved() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string(nil), f.removed...)
}

type sweepFixture struct {
	srv   *sweepListServer
	store *slashcmd.SkillStore
	reg   *slashcmd.Registry
	wf    *fakeRevocationWorkflows
	audit *recordingAuditEmitter
	lanes *SyncLanes
	sw    *RevocationSweeper

	annMu     sync.Mutex
	announced []RevocationTarget
}

func newSweepFixture(t *testing.T) *sweepFixture {
	t.Helper()
	f := &sweepFixture{srv: &sweepListServer{body: `{"items":[]}`}}
	httpSrv := httptest.NewServer(f.srv)
	t.Cleanup(httpSrv.Close)
	stubTokens(t, TokenSet{AccessToken: "at", RefreshToken: "rt", ExpiresAt: time.Now().Add(time.Hour)})
	reg, err := slashcmd.NewRegistry(slashcmd.Deps{})
	if err != nil {
		t.Fatal(err)
	}
	f.store = slashcmd.NewSkillStore(t.TempDir())
	f.reg = reg
	f.wf = &fakeRevocationWorkflows{owner: map[string]string{}, version: map[string]string{}}
	f.audit = &recordingAuditEmitter{}
	f.lanes = NewSyncLanes()
	f.sw = &RevocationSweeper{
		Client: makeTestClient(t, httpSrv.URL), Skills: f.store, Registry: reg,
		Workflows: f.wf, Lanes: f.lanes, Emitter: f.audit,
		OnUninstalled: func(rt RevocationTarget) {
			f.annMu.Lock()
			defer f.annMu.Unlock()
			f.announced = append(f.announced, rt)
		},
	}
	return f
}

func (f *sweepFixture) install(t *testing.T, sk slashcmd.Skill) {
	t.Helper()
	if sk.Kind == "" {
		sk.Kind = slashcmd.KindText
	}
	if sk.Body == "" {
		sk.Body = "body"
	}
	if err := slashcmd.LiveRegister(f.store, f.reg, sk); err != nil && !errors.Is(err, slashcmd.ErrTriggerShadowed) {
		t.Fatalf("LiveRegister(%s): %v", sk.ID, err)
	}
}

func (f *sweepFixture) has(id string) bool {
	_, err := f.store.Get(id)
	return err == nil
}

// listBody renders fleet's live CatalogListResponse for (id, lifecycle)
// pairs; lifecycle "" omits the key (pre-0114 fleet).
func listBody(rows ...[2]string) string {
	var parts []string
	for _, r := range rows {
		lc := ""
		if r[1] != "" {
			lc = `,"lifecycle":"` + r[1] + `"`
			if r[1] == "revoked" {
				lc += `,"revoked_at":"2026-10-05T10:00:00Z"`
			}
		}
		parts = append(parts, `{"id":"`+r[0]+`","owner_user_id":"u","kind":"skill","slug":"s","version":"1","visibility":"team","description":"","signature":"","mandated":false,"published_at":"2026-10-01T00:00:00Z","mandated_reviewed":false`+lc+`}`)
	}
	return `{"items":[` + strings.Join(parts, ",") + `]}`
}

// AC-2: a revoked version the USER installed is uninstalled within one
// cycle, with a local audit row; a mandated copy of the same catalog id and
// a user-authored skill are untouched; active / deprecated / unknown /
// absent rows never uninstall.
func TestRevocationSweep_UninstallsOnlyRevokedUserCatalogCopies(t *testing.T) {
	f := newSweepFixture(t)
	f.install(t, slashcmd.Skill{ID: "deploy", Trigger: "deploy", Source: slashcmd.SkillSourceCatalog, CatalogID: "c-rev", Version: "1"})
	f.install(t, slashcmd.Skill{ID: "policy", Trigger: "policy", Source: slashcmd.SkillSourceMandated, OrgManaged: true, CatalogID: "c-rev-pinned", Version: "2"})
	f.install(t, slashcmd.Skill{ID: "mine", Trigger: "mine"}) // user-authored: no catalog id
	f.install(t, slashcmd.Skill{ID: "active", Trigger: "active", Source: slashcmd.SkillSourceCatalog, CatalogID: "c-act", Version: "1"})
	f.install(t, slashcmd.Skill{ID: "dep", Trigger: "dep", Source: slashcmd.SkillSourceCatalog, CatalogID: "c-dep", Version: "1"})
	f.install(t, slashcmd.Skill{ID: "odd", Trigger: "odd", Source: slashcmd.SkillSourceCatalog, CatalogID: "c-odd", Version: "1"})
	f.install(t, slashcmd.Skill{ID: "gone", Trigger: "gone", Source: slashcmd.SkillSourceCatalog, CatalogID: "c-absent", Version: "1"})
	f.wf.owner["nightly"], f.wf.version["nightly"] = "c-wrev", "3"
	f.wf.owner["weekly"] = "c-act"
	f.srv.set(200, listBody(
		[2]string{"c-rev", "revoked"}, [2]string{"c-rev-pinned", "revoked"}, [2]string{"c-wrev", "revoked"},
		[2]string{"c-act", "active"}, [2]string{"c-dep", "deprecated"}, [2]string{"c-odd", "quarantined"},
	))

	res, err := f.sw.Sweep(context.Background())
	if err != nil {
		t.Fatalf("Sweep: %v", err)
	}
	if f.has("deploy") {
		t.Error("revoked user catalog skill still installed")
	}
	if _, ok := f.reg.Lookup("deploy"); ok {
		t.Error("revoked skill still dispatched by the registry")
	}
	for _, keep := range []string{"policy", "mine", "active", "dep", "odd", "gone"} {
		if !f.has(keep) {
			t.Errorf("%s was uninstalled — only an explicit revoked user catalog copy may be", keep)
		}
	}
	if got := f.wf.snapshotRemoved(); len(got) != 1 || got[0] != "nightly" {
		t.Errorf("workflows removed = %v, want [nightly]", got)
	}
	if len(res.Uninstalled) != 2 {
		t.Errorf("result = %+v", res)
	}
	ev := f.audit.snapshot()
	if len(ev) != 2 {
		t.Fatalf("audit = %+v, want 2 rows", ev)
	}
	p, _ := ev[0].payload.(contextaudit.FleetCatalogRevokedUninstalledPayload)
	if ev[0].kind != contextaudit.KindFleetCatalogRevokedUninstalled || p.CatalogID != "c-rev" || p.Version != "1" || p.Reason != "revoked" || p.LocalID != "deploy" {
		t.Errorf("skill audit row = %+v", ev[0])
	}
	f.annMu.Lock()
	ann := len(f.announced)
	f.annMu.Unlock()
	if ann != 2 {
		t.Errorf("announced %d uninstalls, want 2", ann)
	}
	if st := f.lanes.Snapshot(LaneCatalogRevocation); st.Status != LaneOK {
		t.Errorf("lane = %+v, want ok", st)
	}

	// Idempotent: a second cycle removes nothing more and audits nothing.
	if _, err := f.sw.Sweep(context.Background()); err != nil {
		t.Fatal(err)
	}
	if len(f.audit.snapshot()) != 2 {
		t.Error("second sweep re-audited")
	}
}

// The sweep's list request carries NO lifecycle filter: fleet's no-param
// default is what includes revoked rows (research/fleet-answers-2026-10-06).
func TestRevocationSweep_ListHasNoLifecycleParam(t *testing.T) {
	f := newSweepFixture(t)
	f.install(t, slashcmd.Skill{ID: "a", Trigger: "a", Source: slashcmd.SkillSourceCatalog, CatalogID: "c1", Version: "1"})
	if _, err := f.sw.Sweep(context.Background()); err != nil {
		t.Fatal(err)
	}
	calls := f.srv.calls()
	if len(calls) != 1 {
		t.Fatalf("list calls = %v", calls)
	}
	if calls[0] != "" {
		t.Errorf("sweep list query = %q, want no parameters at all (no lifecycle filter)", calls[0])
	}
}

// AC-6: a pre-0114 fleet (no lifecycle key) — nothing is uninstalled and no
// error is raised.
func TestRevocationSweep_Pre0114FleetUninstallsNothing(t *testing.T) {
	f := newSweepFixture(t)
	f.install(t, slashcmd.Skill{ID: "a", Trigger: "a", Source: slashcmd.SkillSourceCatalog, CatalogID: "c1", Version: "1"})
	f.wf.owner["w"] = "c2"
	f.srv.set(200, listBody([2]string{"c1", ""}, [2]string{"c2", ""}))
	res, err := f.sw.Sweep(context.Background())
	if err != nil || len(res.Uninstalled) != 0 || !f.has("a") || len(f.wf.snapshotRemoved()) != 0 {
		t.Fatalf("pre-0114: res=%+v err=%v", res, err)
	}
}

// A list failure skips the cycle: nothing is uninstalled on ambiguity, and
// the lane reports it.
func TestRevocationSweep_ListFailureUninstallsNothing(t *testing.T) {
	f := newSweepFixture(t)
	f.install(t, slashcmd.Skill{ID: "a", Trigger: "a", Source: slashcmd.SkillSourceCatalog, CatalogID: "c1", Version: "1"})
	f.srv.set(http.StatusInternalServerError, `{"code":"internal_error"}`)
	if _, err := f.sw.Sweep(context.Background()); err == nil {
		t.Fatal("want an error")
	}
	if !f.has("a") {
		t.Fatal("list failure uninstalled a skill")
	}
	if st := f.lanes.Snapshot(LaneCatalogRevocation); st.Status != LaneDegraded || st.Reason != "list_failed" {
		t.Errorf("lane = %+v", st)
	}
	// An unreadable candidate set also uninstalls nothing.
	f.srv.set(200, listBody([2]string{"c1", "revoked"}))
	f.wf.mu.Lock()
	f.wf.listErr = errors.New("provenance unreadable")
	f.wf.mu.Unlock()
	if _, err := f.sw.Sweep(context.Background()); err == nil || !f.has("a") {
		t.Fatalf("enumeration failure: err=%v, skill present=%v", err, f.has("a"))
	}
}

// 403 (tier lapse) backs off: the next cycle does not call fleet, the lane
// says not_entitled, and nothing is uninstalled; once the backoff is spent
// the sweep asks again.
func TestRevocationSweep_TierLapseBacksOff(t *testing.T) {
	f := newSweepFixture(t)
	f.install(t, slashcmd.Skill{ID: "a", Trigger: "a", Source: slashcmd.SkillSourceCatalog, CatalogID: "c1", Version: "1"})
	f.srv.set(http.StatusForbidden, `{"code":"tier_required","message":"x"}`)
	if _, err := f.sw.Sweep(context.Background()); err == nil {
		t.Fatal("want the 403")
	}
	if st := f.lanes.Snapshot(LaneCatalogRevocation); st.Status != LaneDegraded || st.Reason != "not_entitled" {
		t.Errorf("lane = %+v", st)
	}
	res, err := f.sw.Sweep(context.Background())
	if err != nil || !res.Skipped {
		t.Fatalf("backoff cycle = %+v, %v; want skipped", res, err)
	}
	if n := len(f.srv.calls()); n != 1 {
		t.Errorf("fleet called %d times during backoff, want 1", n)
	}
	f.srv.set(200, listBody([2]string{"c1", "revoked"}))
	if _, err := f.sw.Sweep(context.Background()); err != nil {
		t.Fatal(err)
	}
	if f.has("a") || len(f.srv.calls()) != 2 {
		t.Errorf("after backoff: present=%v calls=%d", f.has("a"), len(f.srv.calls()))
	}
}

// With no catalog installs the sweep does not call fleet at all.
func TestRevocationSweep_NothingInstalledNoCall(t *testing.T) {
	f := newSweepFixture(t)
	f.install(t, slashcmd.Skill{ID: "mine", Trigger: "mine"})
	res, err := f.sw.Sweep(context.Background())
	if err != nil || !res.Skipped || len(f.srv.calls()) != 0 {
		t.Fatalf("res=%+v err=%v calls=%v", res, err, f.srv.calls())
	}
}

// A mandate (or another version) that took the local id between the list
// and the removal is left alone: the uninstall re-checks provenance.
func TestRevocationSweep_RechecksProvenanceAtRemoval(t *testing.T) {
	f := newSweepFixture(t)
	f.install(t, slashcmd.Skill{ID: "a", Trigger: "a", Source: slashcmd.SkillSourceCatalog, CatalogID: "c1", Version: "1"})
	// The org mandates a different version under the same local id.
	f.install(t, slashcmd.Skill{ID: "a", Trigger: "a", Source: slashcmd.SkillSourceMandated, OrgManaged: true, CatalogID: "c2", Version: "2"})
	removed, err := f.sw.uninstallLocked(context.Background(), RevocationTarget{Kind: CatalogKindSkill, CatalogID: "c1", LocalID: "a"})
	if err != nil || removed || !f.has("a") {
		t.Fatalf("removed=%v err=%v present=%v — the mandated copy must survive", removed, err, f.has("a"))
	}
}

// The config poller runs the after-poll hook on every round, including a
// failed fetch, and never lets a panicking hook stop polling.
func TestConfigPoller_AfterPollRunsEveryRound(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer srv.Close()
	p := newPollerForTest(t, srv, &fakeApplier{}, t.TempDir())
	var mu sync.Mutex
	n := 0
	p.SetAfterPoll(func(context.Context) {
		mu.Lock()
		n++
		mu.Unlock()
		panic("hook panics")
	})
	for i := 0; i < 2; i++ {
		if err := p.round(context.Background()); err == nil {
			t.Fatal("want the 500")
		}
	}
	mu.Lock()
	defer mu.Unlock()
	if n != 2 {
		t.Errorf("after-poll ran %d times, want 2", n)
	}
}
