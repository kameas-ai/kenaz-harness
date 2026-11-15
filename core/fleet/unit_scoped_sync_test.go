package fleet

// unit_scoped_sync_test.go — kenaz-fleet spec 018 H-2 on the Unit lane, over
// a REAL sqlite units store (CLAUDE.md blind spot #2): the persisted opaque
// cursor, the legacy since-cursor upgrade, cursor refusals, loss-of-access
// resets, withdrawn tombstones per stream, audience labels, and results[]
// on push.

import (
	"context"
	"errors"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/kameas-ai/kenaz-harness/core/units"
)

func unitScopedSyncer(t *testing.T, m *units.Manager, srv *scopedPullServer, self, dataDir string) *UnitSyncer {
	t.Helper()
	hs := httptest.NewServer(srv)
	t.Cleanup(hs.Close)
	stubTokens(t, TokenSet{AccessToken: "at-unit-scoped", RefreshToken: "rt", ExpiresAt: time.Now().Add(time.Hour)})
	if dataDir == "" {
		dataDir = t.TempDir()
	}
	if self != "" {
		if err := SaveIdentity(dataDir, Identity{UserID: self, OrgID: testOrg, TeamID: "team-a"}); err != nil {
			t.Fatal(err)
		}
	}
	return NewUnitSyncer(makeTestClient(t, hs.URL), m, NewUnitMapper("team-a"), makeCapPollerWithTeamCap(t), dataDir)
}

func unitNode(id, team, owner string) ContextPulledNode {
	return ContextPulledNode{
		ID: id, OrgID: testOrg, Kind: "doc", Title: "Unit " + id, Body: "body " + id, OwnerUserID: owner,
		Classification: ClassTeamShared, TeamID: team, Audience: AudienceTeam, Version: 1, UpdatedAt: "2026-11-15T00:00:00Z",
	}
}

func hasUnit(t *testing.T, m *units.Manager, id string) (units.Unit, bool) {
	t.Helper()
	u, err := m.Get(context.Background(), id)
	if errors.Is(err, units.ErrUnitNotFound) {
		return units.Unit{}, false
	}
	if err != nil {
		t.Fatal(err)
	}
	return u, true
}

const (
	uA = "6b2f6f0e-0000-4000-8000-0000000028a1"
	uB = "6b2f6f0e-0000-4000-8000-0000000028b2"
	uC = "6b2f6f0e-0000-4000-8000-0000000028c3"
	uD = "6b2f6f0e-0000-4000-8000-0000000028d4"
)

func TestUnitScoped_OpaqueCursorPersistsAcrossRestart(t *testing.T) {
	_, m := openUnitSyncSQLite(t)
	dataDir := t.TempDir()
	srv := &scopedPullServer{answer: func(q string, n int) (int, any) {
		if n == 1 {
			return 200, pullBody([]ContextPulledNode{unitNode(uA, "team-b", testAuthor)}, "u-opaque-1")
		}
		return 200, pullBody(nil, "u-opaque-1")
	}}
	s := unitScopedSyncer(t, m, srv, testMember, dataDir)
	mustPullDown(t, s)
	raw, _ := os.ReadFile(unitCursorPath(dataDir))
	if !strings.HasPrefix(string(raw), unitCursorOpaquePrefix+"u-opaque-1") {
		t.Fatalf("cursor file %q, want the opaque cursor", raw)
	}
	s2 := unitScopedSyncer(t, m, srv, testMember, dataDir)
	mustPullDown(t, s2)
	if q := srv.snapshotQueries(); !strings.Contains(q[1], "cursor=u-opaque-1") || strings.Contains(q[1], "since=") {
		t.Fatalf("restart query %q, want cursor=u-opaque-1", q[1])
	}
	// Audience labels are kept for the unit.
	if l, ok := s2.NodeLabels(context.Background(), uA); !ok || l.Audience != AudienceTeam || l.TeamID != "team-b" {
		t.Fatalf("labels = %+v %v", l, ok)
	}
}

func TestUnitScoped_LegacySinceCursorUpgrades(t *testing.T) {
	_, m := openUnitSyncSQLite(t)
	dataDir := t.TempDir()
	if err := saveUnitCursor(dataDir, "2026-11-01T00:00:00Z"); err != nil {
		t.Fatal(err)
	}
	srv := &scopedPullServer{answer: func(q string, n int) (int, any) { return 200, pullBody(nil, "u-opaque-9") }}
	s := unitScopedSyncer(t, m, srv, testMember, dataDir)
	mustPullDown(t, s)
	mustPullDown(t, s)
	q := srv.snapshotQueries()
	if !strings.Contains(q[0], "since=2026-11-01") || !strings.Contains(q[1], "cursor=u-opaque-9") {
		t.Fatalf("queries = %q; want since= once, then the opaque cursor", q)
	}
}

func TestUnitScoped_CursorRefusedIsOneCleanRepullWithoutSweep(t *testing.T) {
	_, m := openUnitSyncSQLite(t)
	srv := &scopedPullServer{answer: func(q string, n int) (int, any) {
		if strings.Contains(q, "cursor=") {
			return 400, map[string]any{"code": codeCursorFilterMismatch}
		}
		if n == 1 {
			return 200, pullBody([]ContextPulledNode{unitNode(uA, "team-b", testAuthor)}, "u-1")
		}
		return 200, pullBody([]ContextPulledNode{unitNode(uB, "team-b", testAuthor)}, "u-2")
	}}
	s := unitScopedSyncer(t, m, srv, testMember, "")
	mustPullDown(t, s)
	mustPullDown(t, s)
	if q := srv.snapshotQueries(); len(q) != 3 || strings.Contains(q[2], "cursor=") {
		t.Fatalf("queries = %q", q)
	}
	// A cursor is bound to a member: a refusal says nothing about this
	// account's units, so nothing is swept.
	if _, ok := hasUnit(t, m, uA); !ok {
		t.Fatal("a refused cursor must not sweep units")
	}
	if _, ok := hasUnit(t, m, uB); !ok {
		t.Fatal("the clean re-pull did not apply")
	}
	if st := s.Status(); st.Cursor != "u-2" || st.ResetCount != 0 {
		t.Fatalf("status = %+v", st)
	}
}

func TestUnitScoped_ResetDropsTeamsDemotesOwnAndSweeps(t *testing.T) {
	_, m := openUnitSyncSQLite(t)
	ctx := context.Background()
	srv := &scopedPullServer{}
	srv.answer = func(q string, n int) (int, any) {
		switch {
		case n == 1:
			return 200, pullBody([]ContextPulledNode{
				unitNode(uA, "team-a", testAuthor), // teammate's, in the lost team
				unitNode(uB, "team-a", testMember), // the member's own, in the lost team
				unitNode(uC, "team-b", testAuthor), // still readable, re-served
				unitNode(uD, "team-b", testAuthor), // gone without notice: swept
			}, "u-1")
		case strings.Contains(q, "cursor="):
			return 200, map[string]any{"nodes": []any{}, "next_cursor": "start", "reset": true,
				"reset_reason": ResetLeftTeam, "reset_team_ids": []string{"team-a"}}
		default:
			return 200, pullBody([]ContextPulledNode{unitNode(uC, "team-b", testAuthor)}, "u-2")
		}
	}
	s := unitScopedSyncer(t, m, srv, testMember, "")
	mustPullDown(t, s)
	mustPullDown(t, s)

	if _, ok := hasUnit(t, m, uA); ok {
		t.Error("teammate's unit in the lost team survived the reset")
	}
	if u, ok := hasUnit(t, m, uB); !ok || u.Classification != units.ClassPersonal {
		t.Errorf("own unit in the lost team = %+v %v, want kept as personal", u, ok)
	}
	if _, ok := hasUnit(t, m, uC); !ok {
		t.Error("a re-served unit was dropped")
	}
	if _, ok := hasUnit(t, m, uD); ok {
		t.Error("a unit the re-snapshot did not serve survived the sweep")
	}
	if _, err := m.GetSyncStateByNodeID(ctx, uB); !errors.Is(err, units.ErrSyncStateNotFound) {
		t.Errorf("demoted unit kept its sidecar: %v", err)
	}
	q := srv.snapshotQueries()
	if len(q) != 3 || strings.Contains(q[2], "cursor=") {
		t.Fatalf("queries = %q; want first, reset, one cursorless re-snapshot", q)
	}
	st := s.Status()
	if st.ResetCount != 1 || st.LastResetReason != ResetLeftTeam || st.ResetDropped != 2 || st.ResetDemoted != 1 {
		t.Fatalf("status = %+v", st)
	}
	if _, err := os.Stat(unitSnapshotMarkPath(s.dataDir)); !os.IsNotExist(err) {
		t.Fatal("snapshot mark left behind after a completed re-snapshot")
	}
}

func TestUnitScoped_InterruptedResetSnapshotResumesAfterRestart(t *testing.T) {
	_, m := openUnitSyncSQLite(t)
	dataDir := t.TempDir()
	fail := true
	srv := &scopedPullServer{}
	srv.answer = func(q string, n int) (int, any) {
		switch {
		case n == 1:
			return 200, pullBody([]ContextPulledNode{unitNode(uA, "team-b", testAuthor), unitNode(uC, "team-b", testAuthor)}, "u-1")
		case strings.Contains(q, "cursor="):
			return 200, map[string]any{"nodes": []any{}, "next_cursor": "start", "reset": true, "reset_reason": ResetTeamReparented}
		case fail:
			return 500, map[string]any{"code": "boom"}
		default:
			return 200, pullBody([]ContextPulledNode{unitNode(uC, "team-b", testAuthor)}, "u-2")
		}
	}
	s := unitScopedSyncer(t, m, srv, testMember, dataDir)
	mustPullDown(t, s)
	if _, err := s.PullDown(context.Background()); err == nil {
		t.Fatal("want the failed re-snapshot's error")
	}
	srv.mu.Lock()
	fail = false
	srv.mu.Unlock()
	s2 := unitScopedSyncer(t, m, srv, testMember, dataDir) // restart
	mustPullDown(t, s2)
	if _, ok := hasUnit(t, m, uA); ok {
		t.Fatal("the resumed re-snapshot did not sweep the unit it no longer serves")
	}
	if _, ok := hasUnit(t, m, uC); !ok {
		t.Fatal("re-served unit dropped")
	}
}

func TestUnitScoped_WithdrawnIsPerStream(t *testing.T) {
	_, m := openUnitSyncSQLite(t)
	at := "2026-11-15T02:00:00Z"
	withdrawn := func(id string) ContextPulledNode {
		return ContextPulledNode{ID: id, OrgID: testOrg, Kind: "doc", Classification: ClassTeamShared, TeamID: "team-b",
			Audience: AudienceTeam, Version: 3, UpdatedAt: at, DeletedAt: &at, Withdrawn: true}
	}
	srv := &scopedPullServer{}
	srv.answer = func(q string, n int) (int, any) {
		if n == 1 {
			return 200, pullBody([]ContextPulledNode{unitNode(uA, "team-b", testAuthor), unitNode(uB, "team-b", testAuthor)}, "u-1")
		}
		return 200, pullBody([]ContextPulledNode{withdrawn(uA), withdrawn(uB), withdrawn(uD)}, "u-2")
	}
	// No identity: a withdrawn row needs no ownership decision, so it is
	// never held.
	s := unitScopedSyncer(t, m, srv, "", "")
	// The Curated lane's (wider) stream still serves uB live.
	s.SetServesLive(func(id string) bool { return id == uB })
	mustPullDown(t, s)
	mustPullDown(t, s)
	if _, ok := hasUnit(t, m, uA); ok {
		t.Error("withdrawn unit not dropped")
	}
	if _, err := m.GetSyncStateByNodeID(context.Background(), uA); !errors.Is(err, units.ErrSyncStateNotFound) {
		t.Error("withdrawn unit's sidecar (its version) not forgotten")
	}
	if _, ok := hasUnit(t, m, uB); !ok {
		t.Error("a unit another stream serves live was dropped on a withdrawn tombstone")
	}
	st := s.Status()
	if st.WithdrawnApplied != 1 || st.WithdrawnKept != 1 || st.TombstonesHeldIdentityUnknown != 0 {
		t.Fatalf("status = %+v", st)
	}
	if len(srv.snapshotPushes()) != 0 {
		t.Fatal("a withdrawn tombstone must never push anything")
	}
	// It comes back later at any version: first sight again.
	srv.mu.Lock()
	srv.answer = func(string, int) (int, any) {
		n := unitNode(uA, "team-b", testAuthor)
		n.Version = 1
		return 200, pullBody([]ContextPulledNode{n}, "u-3")
	}
	srv.mu.Unlock()
	mustPullDown(t, s)
	if _, ok := hasUnit(t, m, uA); !ok {
		t.Fatal("a node returning after a withdrawal was not accepted at its (lower) version")
	}
}

func TestUnitScoped_TeamDeleteTombstoneRemovesLikeAnyOther(t *testing.T) {
	_, m := openUnitSyncSQLite(t)
	at := "2026-11-15T03:00:00Z"
	srv := &scopedPullServer{}
	srv.answer = func(q string, n int) (int, any) {
		if n == 1 {
			return 200, pullBody([]ContextPulledNode{unitNode(uA, "team-gone", testAuthor)}, "u-1")
		}
		tomb := ContextPulledNode{ID: uA, OrgID: testOrg, Kind: "doc", OwnerUserID: testAuthor, Classification: ClassTeamShared,
			TeamID: "team-gone", Version: 2, UpdatedAt: at, DeletedAt: &at}
		return 200, pullBody([]ContextPulledNode{tomb}, "u-2")
	}
	s := unitScopedSyncer(t, m, srv, testMember, "")
	mustPullDown(t, s)
	mustPullDown(t, s)
	if _, ok := hasUnit(t, m, uA); ok {
		t.Fatal("a deleted team's tombstone did not remove the unit")
	}
}
