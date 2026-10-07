package workflows

// skill-library-01SKLIB01 WP02 — closes docs/unwired-ledger.md 2026-10-06
// conformance residual item 4: a mandated workflow was delete-protected but
// not edit/unschedule-protected. It is now refused on Save, ScheduleSet,
// ScheduleClear and a user catalog re-install, reported OrgManaged on List,
// and the mandate's own removal still disarms its schedule. Real sqlite
// store; real file-backed provenance.

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"

	cedargo "github.com/cedar-policy/cedar-go"
	"github.com/kameas-ai/kenaz-harness/core/policy/cedar"

	corewf "github.com/kameas-ai/kenaz-harness/core/workflows"
	wfsched "github.com/kameas-ai/kenaz-harness/core/workflows/scheduler"
)

// recordingScheduler records Register/Unregister calls (mutex + snapshot:
// the view may call it from the install goroutine).
type recordingScheduler struct {
	fakeScheduler
	mu    sync.Mutex
	calls []string
}

func (r *recordingScheduler) Register(_ context.Context, id, _, _ string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.calls = append(r.calls, "register:"+id)
	return nil
}

func (r *recordingScheduler) Unregister(_ context.Context, id string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.calls = append(r.calls, "unregister:"+id)
	return nil
}

func (r *recordingScheduler) snapshot() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]string(nil), r.calls...)
}

var _ wfsched.Scheduler = (*recordingScheduler)(nil)

func TestMandatedWorkflow_EditAndScheduleGuarded(t *testing.T) {
	store := newWP07TestStore(t)
	prov := corewf.NewFileProvenanceStore(t.TempDir())
	sched := &recordingScheduler{}
	api := New(Config{Engine: corewf.NewEngine(), Store: store, Provenance: prov, Scheduler: sched})
	ctx := context.Background()
	doc := []byte("id: org-flow\nname: Org flow\nversion: 1\nsteps:\n  - name: a\n    kind: shell\n    cmd: echo\n")
	if _, err := api.InstallDocument(ctx, doc, DocumentOrigin{CatalogID: "cat-m", Version: "1", Mandated: true}); err != nil {
		t.Fatalf("mandated install: %v", err)
	}

	sums, err := api.List(ctx)
	if err != nil {
		t.Fatal(err)
	}
	var found bool
	for _, s := range sums {
		if s.ID == "org-flow" {
			found = true
			if !s.OrgManaged {
				t.Error("List: mandated workflow not reported OrgManaged")
			}
		}
	}
	if !found {
		t.Fatal("List: mandated workflow missing")
	}

	// The structured-editor path keeps the caller's id (the YAML path mints
	// a fresh id, so it can never overwrite an existing workflow).
	edited, err := api.Get(ctx, "org-flow")
	if err != nil {
		t.Fatal(err)
	}
	edited.Name = "Edited"
	if _, err := api.Save(ctx, SaveInput{Workflow: &edited}); !errors.Is(err, ErrWorkflowOrgManaged) {
		t.Errorf("Save(mandated) = %v, want ErrWorkflowOrgManaged", err)
	}
	if w, _ := store.Load(ctx, "org-flow"); w.Name != "Org flow" {
		t.Errorf("refused Save still wrote: name = %q", w.Name)
	}
	if err := api.ScheduleSet(ctx, ScheduleSetInput{WorkflowID: "org-flow", Cron: "0 * * * *", Timezone: "UTC"}); !errors.Is(err, ErrWorkflowOrgManaged) {
		t.Errorf("ScheduleSet(mandated) = %v, want ErrWorkflowOrgManaged", err)
	}
	if err := api.ScheduleClear(ctx, "org-flow"); !errors.Is(err, ErrWorkflowOrgManaged) {
		t.Errorf("ScheduleClear(mandated) = %v, want ErrWorkflowOrgManaged", err)
	}
	if got := sched.snapshot(); len(got) != 0 {
		t.Errorf("refused schedule calls reached the scheduler: %v", got)
	}
	// A user catalog re-install of the same item must not relabel the
	// org's copy as the user's.
	if _, err := api.InstallDocument(ctx, doc, DocumentOrigin{CatalogID: "cat-m", Slug: "org-flow", Version: "1"}); !errors.Is(err, ErrWorkflowOrgManaged) {
		t.Errorf("user install over mandate = %v, want ErrWorkflowOrgManaged", err)
	}
	if p, _, _ := prov.Get("org-flow"); p.Source != corewf.ProvenanceMandated {
		t.Errorf("provenance relabelled to %q", p.Source)
	}

	// A user's own workflow is unaffected by the guard.
	if _, err := api.Save(ctx, SaveInput{YAML: "id: mine\nname: Mine\nversion: 1\nsteps:\n  - name: a\n    kind: shell\n    cmd: echo\n"}); err != nil {
		t.Fatalf("Save(user) = %v", err)
	}
	if err := api.ScheduleSet(ctx, ScheduleSetInput{WorkflowID: "mine", Cron: "0 * * * *", Timezone: "UTC"}); err != nil {
		t.Errorf("ScheduleSet(user) = %v", err)
	}

	// The mandate's own removal still disarms the schedule and deletes.
	if _, err := api.RemoveMandatedDocument(ctx, "org-flow", "cat-m", nil, false); err != nil {
		t.Fatalf("RemoveMandatedDocument: %v", err)
	}
	got := sched.snapshot()
	if len(got) == 0 || got[len(got)-1] != "unregister:org-flow" {
		t.Errorf("scheduler calls = %v, want the removal to unregister org-flow", got)
	}
	if _, err := store.Load(ctx, "org-flow"); !errors.Is(err, corewf.ErrWorkflowNotFound) {
		t.Errorf("mandated workflow still stored after removal: %v", err)
	}
}

// skill-library-01SKLIB01 WP03: the revocation sweep's workflow half lists
// ONLY the user's catalog installs and removes one only while its
// provenance is still that catalog install.
func TestRevokedCatalogDocument_OnlyUserCatalogInstalls(t *testing.T) {
	store := newWP07TestStore(t)
	dir := t.TempDir()
	prov := corewf.NewFileProvenanceStore(dir)
	sched := &recordingScheduler{}
	api := New(Config{Engine: corewf.NewEngine(), Store: store, Provenance: prov, Scheduler: sched})
	ctx := context.Background()
	doc := func(id string) []byte {
		return []byte("id: " + id + "\nname: " + id + "\nversion: 1\nsteps:\n  - name: a\n    kind: shell\n    cmd: echo\n")
	}
	if _, err := api.InstallDocument(ctx, doc("user-cat"), DocumentOrigin{CatalogID: "c-rev", Slug: "user-cat", Version: "1"}); err != nil {
		t.Fatal(err)
	}
	if _, err := api.InstallDocument(ctx, doc("org"), DocumentOrigin{CatalogID: "c-org", Version: "2", Mandated: true}); err != nil {
		t.Fatal(err)
	}
	if _, err := api.Save(ctx, SaveInput{YAML: string(doc("authored"))}); err != nil {
		t.Fatal(err)
	}

	// A fresh store over the same file: enumeration reads what persisted.
	api2 := New(Config{Engine: corewf.NewEngine(), Store: store, Provenance: corewf.NewFileProvenanceStore(dir), Scheduler: sched})
	ins, err := api2.CatalogInstalls(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(ins) != 1 || ins[0].WorkflowID != "user-cat" || ins[0].CatalogID != "c-rev" || ins[0].Version != "1" {
		t.Fatalf("CatalogInstalls = %+v, want only the user's catalog install", ins)
	}

	// Wrong catalog id (another version): left alone.
	if removed, err := api.RemoveRevokedCatalogDocument(ctx, "user-cat", "c-other"); err != nil || removed {
		t.Fatalf("other version: removed=%v err=%v", removed, err)
	}
	// A mandate is never removed by revocation of its catalog id.
	if removed, err := api.RemoveRevokedCatalogDocument(ctx, "org", "c-org"); err != nil || removed {
		t.Fatalf("mandated: removed=%v err=%v", removed, err)
	}
	if _, err := store.Load(ctx, "org"); err != nil {
		t.Fatal("revocation removed a mandated workflow")
	}
	removed, err := api.RemoveRevokedCatalogDocument(ctx, "user-cat", "c-rev")
	if err != nil || !removed {
		t.Fatalf("revoked user install: removed=%v err=%v", removed, err)
	}
	if _, err := store.Load(ctx, "user-cat"); !errors.Is(err, corewf.ErrWorkflowNotFound) {
		t.Fatalf("revoked workflow still stored: %v", err)
	}
	if got := sched.snapshot(); len(got) == 0 || got[len(got)-1] != "unregister:user-cat" {
		t.Errorf("schedule not disarmed: %v", got)
	}
	if _, ok, _ := corewf.NewFileProvenanceStore(dir).Get("user-cat"); ok {
		t.Error("provenance record survived the removal on disk")
	}
	// Idempotent.
	if removed, err := api.RemoveRevokedCatalogDocument(ctx, "user-cat", "c-rev"); err != nil || removed {
		t.Fatalf("second removal: removed=%v err=%v", removed, err)
	}
}

// strictShellGate denies a workflow save carrying a shell step while the
// mode is strict — the shipped bundle's strict arm, reduced to its rule.
type strictShellGate struct{}

func (strictShellGate) Evaluate(_ context.Context, _ cedargo.EntityUID, action string, _ cedargo.EntityUID, attrs map[cedargo.String]cedargo.Value) cedar.Decision {
	mode, _ := attrs[cedargo.String("mode")].(cedargo.String)
	kinds, _ := attrs[cedargo.String("step_kinds")].(cedargo.String)
	if action == cedar.ActionWorkflowSave && string(mode) == "strict" && strings.Contains(string(kinds), "shell") {
		return cedar.Decision{Outcome: cedar.Deny, Action: action, Reason: "strict mode forbids shell steps"}
	}
	return cedar.Decision{Outcome: cedar.Allow, Action: action}
}

// Review F3 (skill-library-01SKLIB01): restoring the user's snapshotted copy
// on mandate withdrawal goes through the Cedar save gate. A shell-step copy
// snapshotted under permissive mode is REFUSED once strict mode is on: the
// mandated copy is deleted (not left, not replaced by the ungated
// snapshot), and the refusal is reported so the caller audits it.
func TestRemoveMandatedDocument_RestoreRefusedByPolicyDeletes(t *testing.T) {
	store := newWP07TestStore(t)
	prov := corewf.NewFileProvenanceStore(t.TempDir())
	var mu sync.Mutex
	mode := "permissive"
	api := New(Config{Engine: corewf.NewEngine(), Store: store, Provenance: prov, Cedar: strictShellGate{},
		CedarModeFn: func() string { mu.Lock(); defer mu.Unlock(); return mode }})
	ctx := context.Background()
	user := []byte("id: sh-flow\nname: Mine\nversion: 1\nsteps:\n  - name: a\n    kind: shell\n    cmd: echo\n")
	org := []byte("id: sh-flow\nname: ORG\nversion: 2\nsteps:\n  - name: b\n    kind: model_turn\n    user_prompt: hi\n")
	if _, err := api.InstallDocument(ctx, user, DocumentOrigin{CatalogID: "cat-u", Slug: "sh-flow", Version: "1"}); err != nil {
		t.Fatalf("permissive user install: %v", err)
	}
	_, prior, err := api.InstallMandatedDocument(ctx, org, "cat-m", "2")
	if err != nil || len(prior) == 0 {
		t.Fatalf("takeover: prior=%d err=%v", len(prior), err)
	}
	mu.Lock()
	mode = "strict"
	mu.Unlock()

	out, err := api.RemoveMandatedDocument(ctx, "sh-flow", "cat-m", prior, false)
	if err != nil {
		t.Fatalf("withdrawal: %v", err)
	}
	if out.Restored || out.RestoreRefused == "" {
		t.Fatalf("outcome = %+v, want restore refused with a reason", out)
	}
	if _, err := store.Load(ctx, "sh-flow"); !errors.Is(err, corewf.ErrWorkflowNotFound) {
		t.Fatalf("after a refused restore the workflow must be gone (neither the mandate nor the ungated snapshot): %v", err)
	}
	if _, ok, _ := prov.Get("sh-flow"); ok {
		t.Error("provenance survived the refused-restore delete")
	}
}
