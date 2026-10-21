package fleet

// unit_sync_tombstone_test.go — a pulled tombstone removes the local unit in
// the Unit lane; a clean re-appearance restores it. Real sqlite (CLAUDE.md
// blind spot #2).
//
// Fleet sends a tombstone (deleted_at set) for an FR-103 delete and, since
// kenaz-fleet #325, for a quarantined node to every member but its author:
// empty title/body/metadata, deleted_at = quarantined_at, version UNCHANGED.
// Before this fix applyPulledNode skipped every tombstone ("defer to
// Phase-3"), so the withdrawn body stayed in the local unit store.

import (
	"context"
	"encoding/json"
	"errors"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/kameas-ai/kenaz-harness/core/storage"
	"github.com/kameas-ai/kenaz-harness/core/units"
)

func unitTombstoneSyncer(t *testing.T, m *units.Manager, pages ...contextPullResponse) *UnitSyncer {
	t.Helper()
	fake := &contextFakeServer{}
	for _, p := range pages {
		fake.addPullResponse(p)
	}
	srv := httptest.NewServer(fake)
	t.Cleanup(srv.Close)
	stubTokens(t, TokenSet{AccessToken: "at-unit-tomb", RefreshToken: "rt", ExpiresAt: time.Now().Add(time.Hour)})
	return NewUnitSyncer(makeTestClient(t, srv.URL), m, NewUnitMapper(""), makeCapPollerWithTeamCap(t), t.TempDir())
}

func page(n ContextPulledNode) contextPullResponse {
	return contextPullResponse{Nodes: []ContextPulledNode{n}, Cursor: n.UpdatedAt}
}

func liveUnitNode(id, body string, version int, at string) ContextPulledNode {
	return ContextPulledNode{
		ID: id, Kind: "doc", Title: "Team runbook", Body: body,
		Classification: ClassTeamShared, Version: version, UpdatedAt: at,
	}
}

// memberTombstone is the exact #325 member shape: empty fields, {} metadata,
// deleted_at set, version unchanged.
func memberTombstone(id string, version int, at string) ContextPulledNode {
	return ContextPulledNode{
		ID: id, Kind: "doc", Title: "", Body: "", Metadata: json.RawMessage(`{}`),
		Classification: ClassTeamShared, Version: version, UpdatedAt: at, DeletedAt: &at,
	}
}

func mustPullDown(t *testing.T, s *UnitSyncer) {
	t.Helper()
	if _, err := s.PullDown(context.Background()); err != nil {
		t.Fatalf("PullDown: %v", err)
	}
}

func assertUnitGone(t *testing.T, db storage.DB, m *units.Manager, nodeID, bodyPrefix string) {
	t.Helper()
	if _, err := m.GetSyncStateByNodeID(context.Background(), nodeID); !errors.Is(err, units.ErrSyncStateNotFound) {
		t.Errorf("sidecar for %s after tombstone = %v, want ErrSyncStateNotFound", nodeID, err)
	}
	if n := countRows(t, db, "SELECT COUNT(*) FROM units WHERE id = ? OR body LIKE ?", nodeID, bodyPrefix+"%"); n != 0 {
		t.Errorf("units rows still holding the withdrawn content = %d", n)
	}
	if n := countRows(t, db, "SELECT COUNT(*) FROM unit_versions WHERE unit_id = ? OR body LIKE ?", nodeID, bodyPrefix+"%"); n != 0 {
		t.Errorf("history rows still holding the withdrawn content = %d", n)
	}
}

// FR-103 delete: create, fast-forward (history row), then a tombstone.
func TestUnitPull_DeleteTombstoneRemovesUnitAndSidecar(t *testing.T) {
	db, m := openUnitSyncSQLite(t)
	s := unitTombstoneSyncer(t, m,
		page(liveUnitNode("srv-u-del", "withdrawn v1", 1, ts(-2*time.Minute))),
		page(liveUnitNode("srv-u-del", "withdrawn v2", 2, ts(-time.Minute))),
		page(memberTombstone("srv-u-del", 3, ts(0))),
	)
	mustPullDown(t, s)
	mustPullDown(t, s)
	if n := countRows(t, db, "SELECT COUNT(*) FROM unit_versions WHERE unit_id = ?", "srv-u-del"); n == 0 {
		t.Fatal("setup: fast-forward wrote no history row")
	}
	mustPullDown(t, s)
	assertUnitGone(t, db, m, "srv-u-del", "withdrawn")
	if got := s.Status(); got.TombstonesApplied != 1 || got.TombstoneDroppedLocalEdits != 0 {
		t.Errorf("status = applied %d dropped %d; want 1, 0", got.TombstonesApplied, got.TombstoneDroppedLocalEdits)
	}
}

// Quarantine (#325 member shape, SAME version as the synced baseline) →
// gone; the author's clean edit (higher version, new content) → recreated
// through first sight.
func TestUnitPull_QuarantineTombstoneThenCleanReappearance(t *testing.T) {
	db, m := openUnitSyncSQLite(t)
	ctx := context.Background()
	s := unitTombstoneSyncer(t, m,
		page(liveUnitNode("srv-u-q", "tripped-lint body", 3, ts(-time.Minute))),
		page(memberTombstone("srv-u-q", 3, ts(0))),
		page(liveUnitNode("srv-u-q", "clean body", 4, ts(time.Minute))),
	)
	mustPullDown(t, s)
	if _, err := m.GetSyncStateByNodeID(ctx, "srv-u-q"); err != nil {
		t.Fatalf("setup: first sight did not create the unit: %v", err)
	}

	mustPullDown(t, s)
	assertUnitGone(t, db, m, "srv-u-q", "tripped-lint")

	mustPullDown(t, s)
	st, err := m.GetSyncStateByNodeID(ctx, "srv-u-q")
	if err != nil {
		t.Fatalf("clean re-appearance did not recreate the unit: %v", err)
	}
	u, err := m.Get(ctx, st.UnitID)
	if err != nil {
		t.Fatalf("Get recreated unit: %v", err)
	}
	if u.Body != "clean body" || u.Classification != units.ClassTeam {
		t.Errorf("recreated unit = body %q class %s; want the clean body, team", u.Body, u.Classification)
	}
	if st.SyncedServerVersion != 4 || st.SyncedLocalVersion != u.Version {
		t.Errorf("recreated sidecar = %+v; want server=4 local=%d", st, u.Version)
	}
	if n := countRows(t, db, "SELECT COUNT(*) FROM unit_versions WHERE body LIKE 'tripped-lint%'"); n != 0 {
		t.Errorf("withdrawn body resurfaced in history: %d rows", n)
	}
}

// Author view: full content + quarantine fields, no deleted_at → applied
// as a normal node and kept.
func TestUnitPull_AuthorQuarantineViewIsKept(t *testing.T) {
	_, m := openUnitSyncSQLite(t)
	ctx := context.Background()
	q := ts(0)
	n := liveUnitNode("srv-u-author", "author body", 2, q)
	n.QuarantinedAt, n.QuarantineReason = &q, "secret_detected"
	s := unitTombstoneSyncer(t, m, page(n))
	mustPullDown(t, s)
	st, err := m.GetSyncStateByNodeID(ctx, "srv-u-author")
	if err != nil {
		t.Fatalf("author's quarantined node was not kept: %v", err)
	}
	if u, err := m.Get(ctx, st.UnitID); err != nil || u.Body != "author body" {
		t.Errorf("author unit = %+v, %v", u, err)
	}
}

// Documented rule: a tombstone wins over un-pushed local edits (the
// withdrawn content they sit on must not stay), counted and conflict
// cleared rather than silently dropped.
func TestUnitPull_TombstoneWithUnpushedLocalEdits(t *testing.T) {
	db, m := openUnitSyncSQLite(t)
	ctx := context.Background()
	s := unitTombstoneSyncer(t, m,
		page(liveUnitNode("srv-u-edit", "shared base", 1, ts(-2*time.Minute))),
		page(liveUnitNode("srv-u-edit", "shared base v2", 2, ts(-time.Minute))),
		page(memberTombstone("srv-u-edit", 2, ts(0))),
	)
	mustPullDown(t, s)
	st, err := m.GetSyncStateByNodeID(ctx, "srv-u-edit")
	if err != nil {
		t.Fatalf("setup: %v", err)
	}
	if _, err := m.Update(ctx, st.UnitID, "shared base + my local edit", nil); err != nil {
		t.Fatalf("local edit: %v", err)
	}
	mustPullDown(t, s) // v2 vs local edit → conflict, local kept
	if len(s.Conflicts()) != 1 {
		t.Fatalf("setup: conflicts = %+v, want 1", s.Conflicts())
	}

	mustPullDown(t, s) // tombstone
	assertUnitGone(t, db, m, "srv-u-edit", "shared base")
	got := s.Status()
	if got.TombstonesApplied != 1 || got.TombstoneDroppedLocalEdits != 1 {
		t.Errorf("status = applied %d dropped %d; want 1, 1", got.TombstonesApplied, got.TombstoneDroppedLocalEdits)
	}
	if len(s.Conflicts()) != 0 {
		t.Errorf("conflict for the removed node survived: %+v", s.Conflicts())
	}
}

// Personal units are never touched: a tombstone for a node with no sidecar
// is a no-op, and even a (defensive, should-not-exist) sidecar mapping a
// node to a personal unit does not get it deleted.
func TestUnitPull_TombstoneNeverTouchesPersonal(t *testing.T) {
	_, m := openUnitSyncSQLite(t)
	ctx := context.Background()
	personal, err := m.Create(ctx, units.Unit{
		Kind: units.KindDoc, Scope: units.ScopeProject, ScopeID: "proj-1",
		Classification: units.ClassPersonal, LoadPolicy: units.LoadOnDemand,
		Title: "mine", Body: "my notes",
	})
	if err != nil {
		t.Fatalf("Create personal: %v", err)
	}
	if _, err := m.UpsertSyncState(ctx, units.SyncState{
		UnitID: personal.ID, NodeID: "srv-u-personal", Classification: "team_shared",
	}); err != nil {
		t.Fatalf("seed defensive personal sidecar: %v", err)
	}
	s := unitTombstoneSyncer(t, m,
		page(memberTombstone("srv-u-personal", 1, ts(-time.Minute))),
		page(memberTombstone("srv-u-never-seen", 1, ts(0))),
	)
	mustPullDown(t, s)
	mustPullDown(t, s)
	if got, err := m.Get(ctx, personal.ID); err != nil || got.Body != "my notes" {
		t.Errorf("personal unit = %+v, %v; want untouched", got, err)
	}
	if got := s.Status(); got.TombstonesApplied != 0 {
		t.Errorf("TombstonesApplied = %d, want 0", got.TombstonesApplied)
	}
}
