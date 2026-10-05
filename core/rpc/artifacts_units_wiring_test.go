package rpc

// artifacts_units_wiring_test.go — artifacts-as-units-01DOGF0C WP04
// (spec P-4, FR-4, FR-6). Every artifacts consumer named in spec §2.3
// that writes artifacts is driven against the PRODUCTION stack —
// core.New's real sqlite, newMediaStore, newArtifactsStack — so each
// write lands in the units tables and reads back through the production
// store. No memory store, no recording fake manager.

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/kameas-ai/kenaz-harness/core"
	coreart "github.com/kameas-ai/kenaz-harness/core/artifacts"
	coreatt "github.com/kameas-ai/kenaz-harness/core/attachments"
	"github.com/kameas-ai/kenaz-harness/core/autonomy"
	artifactsview "github.com/kameas-ai/kenaz-harness/core/rpc/views/artifacts"
	planmodeview "github.com/kameas-ai/kenaz-harness/core/rpc/views/planmode"
	coreplanmode "github.com/kameas-ai/kenaz-harness/core/tools/planmode"
	"github.com/kameas-ai/kenaz-harness/core/tools/saveartifact"
	"github.com/kameas-ai/kenaz-harness/core/tools/updateartifact"
	"github.com/kameas-ai/kenaz-harness/core/units"
	corewf "github.com/kameas-ai/kenaz-harness/core/workflows"
)

type unitsPosture struct {
	mu     sync.Mutex
	layers map[string]autonomy.Layer
}

func (p *unitsPosture) LoadAutonomyProfile(_ context.Context, id string) (autonomy.Layer, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if l, ok := p.layers[id]; ok {
		return l, nil
	}
	return autonomy.DefaultLayer(), nil
}

func (p *unitsPosture) SaveAutonomyProfile(_ context.Context, id string, l autonomy.Layer) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.layers[id] = l
	return nil
}

type nopPlanEmitter struct{}

func (nopPlanEmitter) Emit(context.Context, string, string, map[string]any) error { return nil }

func TestArtifactsStack_IsUnitsBacked_EveryConsumerWritesUnits(t *testing.T) {
	ctx := context.Background()
	dataDir := t.TempDir()
	c, err := core.New(core.Options{DataDir: dataDir})
	if err != nil {
		t.Fatalf("core.New: %v", err)
	}
	media := newMediaStore(c)
	store, mgr := newArtifactsStack(c, media)
	if store == nil || mgr == nil {
		t.Fatal("newArtifactsStack returned nil on a real core")
	}
	if _, ok := store.(coreart.ScopePurger); !ok {
		t.Fatalf("production artifacts store is %T, want the units-backed store (ScopePurger)", store)
	}
	sess, err := c.SessionManager().Create(ctx, "units wiring")
	if err != nil {
		t.Fatalf("create session: %v", err)
	}
	um := units.NewManager(units.NewSQLStore(c.Storage()))
	assertArtifactUnit := func(label, id string) {
		t.Helper()
		u, err := um.Get(ctx, id)
		if err != nil {
			t.Fatalf("%s: artifact %s is not a unit: %v", label, id, err)
		}
		if u.Kind != units.KindArtifact || u.Classification != units.ClassPersonal {
			t.Errorf("%s: unit %s kind=%s class=%s, want artifact/personal", label, id, u.Kind, u.Classification)
		}
	}
	fixedSession := func(context.Context) string { return sess.ID }

	// ---- save_artifact tool.
	saveOut, err := saveartifact.New(saveartifact.Options{Manager: mgr, SessionResolver: fixedSession}).
		Call(ctx, json.RawMessage(`{"title":"notes.md","content":"# saved","mime_type":"text/markdown"}`))
	if err != nil {
		t.Fatalf("save_artifact: %v", err)
	}
	var saved struct {
		ArtifactID string `json:"artifact_id"`
	}
	if err := json.Unmarshal(saveOut, &saved); err != nil || saved.ArtifactID == "" {
		t.Fatalf("save_artifact result %s: %v", saveOut, err)
	}
	assertArtifactUnit("save_artifact", saved.ArtifactID)

	// ---- update_artifact tool → unit_versions.
	if _, err := updateartifact.New(updateartifact.Options{Updater: mgr, SessionResolver: fixedSession}).
		Call(ctx, json.RawMessage(`{"artifact_id":"`+saved.ArtifactID+`","content":"# saved v2","summary":"edit"}`)); err != nil {
		t.Fatalf("update_artifact: %v", err)
	}
	vs, err := store.ListVersions(ctx, saved.ArtifactID)
	if err != nil || len(vs) != 1 || vs[0].Version != 1 {
		t.Fatalf("after update_artifact ListVersions = %+v, %v; want one v1", vs, err)
	}
	if n, _ := media.RefcountFor(ctx, vs[0].ContentHash); n < 1 {
		t.Errorf("revision blob refcount = %d — the production media store does not see unit_versions", n)
	}

	// ---- plan-mode exit (capture) + plan-mode Edit (WriteVersion).
	planMode := autonomy.PostureModePlanMode
	posture := &unitsPosture{layers: map[string]autonomy.Layer{sess.ID: {PostureMode: &planMode}}}
	exitOut, err := coreplanmode.NewExitTool(coreplanmode.ExitOptions{Posture: posture, Artifacts: mgr, SessionResolver: fixedSession}).
		Call(ctx, json.RawMessage(`{"plan":"1. do the thing"}`))
	if err != nil {
		t.Fatalf("exit_plan_mode: %v", err)
	}
	var exited struct {
		PlanID string `json:"plan_id"`
	}
	if err := json.Unmarshal(exitOut, &exited); err != nil || exited.PlanID == "" {
		t.Fatalf("exit_plan_mode result %s: %v", exitOut, err)
	}
	assertArtifactUnit("exit_plan_mode", exited.PlanID)
	if _, err := planmodeview.NewAPI(posture, nopPlanEmitter{}, mgr).Edit(ctx, planmodeview.EditRequest{
		SessionID: sess.ID, PlanID: exited.PlanID, EditedPlan: "1. do the better thing",
	}); err != nil {
		t.Fatalf("planmode Edit: %v", err)
	}
	if vs, _ := store.ListVersions(ctx, exited.PlanID); len(vs) != 1 {
		t.Errorf("plan artifact versions after Edit = %d, want 1", len(vs))
	}

	// ---- workflow artifact adapter (Write + Read through media bytes).
	wf := &wfArtifactsAdapter{store: store, mgr: mgr, media: media}
	wfID, err := wf.Write(ctx, corewf.ArtifactWrite{SessionID: sess.ID, Title: "wf out", MimeType: "text/plain", Content: []byte("workflow bytes")})
	if err != nil {
		t.Fatalf("workflow Write: %v", err)
	}
	assertArtifactUnit("workflow adapter", wfID)
	view, err := wf.Read(ctx, wfID)
	if err != nil || string(view.Content) != "workflow bytes" {
		t.Errorf("workflow Read = %q, %v", view.Content, err)
	}

	// ---- edit-file sync sink.
	t.Setenv("HARNESS_EDIT_FILE_ARTIFACT_SYNC", "on")
	edited := filepath.Join(t.TempDir(), "design.md")
	if err := os.WriteFile(edited, []byte("# design\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	sink := artifactsview.NewSinkWithEditSync(artifactsview.NewSinkConcrete(mgr, nil, nil), artifactsview.NewCoalesceBuffer(), func() bool { return true })
	sink.OnPostToolMessage(ctx, sess.ID, "kenaz__edit_file", `{"path":"`+edited+`","old_str":"x","new_str":"y"}`, `{"written":9}`, time.Second)
	all, err := store.List(ctx, coreart.ArtifactFilter{SessionID: sess.ID})
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	var syncedID string
	for _, a := range all {
		if a.SourceRef.AbsolutePath == edited {
			syncedID = a.ID
		}
	}
	if syncedID == "" {
		t.Fatalf("edit-file sync wrote no artifact with AbsolutePath %s (have %d artifacts)", edited, len(all))
	}
	assertArtifactUnit("edit-file sync", syncedID)

	// ---- FR-4 in production wiring: with the artifact's media metadata
	// rows gone, the ONLY reference left to its bytes is the artifact unit.
	// A GC pass over the production media store must keep them — this is
	// the assertion that fails if newArtifactsStack switches the store
	// without registering its refcount source (spec §2.4).
	head, err := store.Get(ctx, saved.ArtifactID)
	if err != nil {
		t.Fatalf("Get saved: %v", err)
	}
	rows, err := media.List(ctx, coreatt.MediaFilter{ContentHash: head.ContentHash})
	if err != nil {
		t.Fatalf("media List: %v", err)
	}
	for _, r := range rows {
		if err := media.Delete(ctx, r.ID); err != nil {
			t.Fatalf("media Delete: %v", err)
		}
	}
	if _, err := media.PruneOrphans(ctx); err != nil {
		t.Fatalf("PruneOrphans: %v", err)
	}
	headPath := filepath.Join(dataDir, "media", head.ContentHash)
	if _, err := os.Stat(headPath); err != nil {
		t.Fatalf("media GC deleted a live artifact's bytes in the production wiring: %v", err)
	}

	// ---- FR-6 in production wiring: deleting the session through
	// session.Manager purges every session-scoped artifact unit.
	if err := c.SessionManager().Delete(ctx, sess.ID); err != nil {
		t.Fatalf("session Delete: %v", err)
	}
	left, err := store.List(ctx, coreart.ArtifactFilter{})
	if err != nil {
		t.Fatalf("List after delete: %v", err)
	}
	if len(left) != 0 {
		t.Errorf("%d artifact unit(s) survived their session's delete in the production wiring", len(left))
	}
	if _, err := os.Stat(headPath); !os.IsNotExist(err) {
		t.Errorf("purged artifact's bytes still on disk after the session delete (err=%v) — the observer did not release them", err)
	}

	// ---- FR-6 for projects, through the real stack (review F3): a
	// project-scoped artifact survives GC while live, and deleting the
	// project through projects.Manager — the production observer —
	// purges it and releases its bytes; a session-scoped artifact that only
	// carried the project link survives with the link nulled.
	proj, err := c.ProjectManager().Create(ctx, "doomed project", "")
	if err != nil {
		t.Fatalf("create project: %v", err)
	}
	pid := proj.ID
	psess, err := c.SessionManager().CreateInProject(ctx, "in project", &pid)
	if err != nil {
		t.Fatalf("create session in project: %v", err)
	}
	pcap, err := mgr.Capture(ctx, []coreart.CaptureCandidate{{
		Title: "promote me", MimeType: "text/plain", Bytes: []byte("project scoped bytes"),
		Source: coreart.SourceUserPin, SourceRef: coreart.ArtifactSourceRef{MessageID: "m"},
	}, {
		Title: "stay in session", MimeType: "text/plain", Bytes: []byte("session scoped bytes"),
		Source: coreart.SourceUserPin, SourceRef: coreart.ArtifactSourceRef{MessageID: "m"},
	}}, psess.ID)
	if err != nil || len(pcap) != 2 {
		t.Fatalf("capture in project session: %v (%d)", err, len(pcap))
	}
	if _, err := store.UpdateScope(ctx, pcap[0].ID, coreart.ScopeKindProject, pid); err != nil {
		t.Fatalf("promote to project: %v", err)
	}
	projPath := filepath.Join(dataDir, "media", pcap[0].ContentHash)
	prow, _ := media.List(ctx, coreatt.MediaFilter{ContentHash: pcap[0].ContentHash})
	for _, r := range prow {
		_ = media.Delete(ctx, r.ID)
	}
	if _, err := media.PruneOrphans(ctx); err != nil {
		t.Fatalf("PruneOrphans: %v", err)
	}
	if _, err := os.Stat(projPath); err != nil {
		t.Fatalf("media GC deleted a live project-scoped artifact's bytes: %v", err)
	}
	if err := c.ProjectManager().Delete(ctx, pid); err != nil {
		t.Fatalf("project Delete: %v", err)
	}
	if _, err := store.Get(ctx, pcap[0].ID); err == nil {
		t.Error("project-scoped artifact unit survived its project's delete in the production wiring")
	}
	if _, err := os.Stat(projPath); !os.IsNotExist(err) {
		t.Errorf("purged project artifact's bytes still on disk (err=%v)", err)
	}
	kept, err := store.Get(ctx, pcap[1].ID)
	if err != nil {
		t.Fatalf("session-scoped artifact deleted by a project delete: %v", err)
	}
	if kept.ProjectID != nil {
		t.Errorf("surviving artifact still linked to deleted project %q", *kept.ProjectID)
	}
}
