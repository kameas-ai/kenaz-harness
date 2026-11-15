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
	raw, _ := os.ReadFile(unitCursorV2Path(dataDir))
	if strings.TrimSpace(string(raw)) != "u-opaque-1" {
		t.Fatalf("v2 cursor file %q, want the opaque cursor", raw)
	}
	if _, err := os.Stat(unitCursorPath(dataDir)); !os.IsNotExist(err) {
		t.Fatal("the opaque cursor leaked into unit_cursor.txt (older builds read it as since=)")
	}
	if q := srv.snapshotQueries(); !strings.Contains(q[0], "limit=1000") {
		t.Fatalf("pull query %q, want an explicit limit=1000", q[0])
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
	s := unitScopedSyncer(t, m, srv, testMember, "")
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

// ── Review fixes (PR #505): ownership, holds, complete-pull sweep, cursor files ──

func TestUnitScoped_LegacyOpaqueCursorMigratesToV2AndSinceStaysForDowngrade(t *testing.T) {
	_, m := openUnitSyncSQLite(t)
	dataDir := t.TempDir()
	// What build 369f26be wrote.
	if err := saveUnitCursor(dataDir, unitCursorOpaquePrefix+"old-opaque"); err != nil {
		t.Fatal(err)
	}
	srv := &scopedPullServer{answer: func(string, int) (int, any) { return 200, pullBody(nil, "next-opaque") }}
	s := unitScopedSyncer(t, m, srv, testMember, dataDir)
	mustPullDown(t, s)
	if q := srv.snapshotQueries(); !strings.Contains(q[0], "cursor=old-opaque") {
		t.Fatalf("query %q, want the migrated opaque cursor", q[0])
	}
	if _, err := os.Stat(unitCursorPath(dataDir)); !os.IsNotExist(err) {
		t.Fatal("the prefixed opaque cursor stayed in unit_cursor.txt")
	}

	// A since cursor stays in unit_cursor.txt when the opaque one arrives,
	// so a downgraded build continues from it.
	dir2 := t.TempDir()
	if err := saveUnitCursor(dir2, "2026-11-01T00:00:00Z"); err != nil {
		t.Fatal(err)
	}
	s2 := unitScopedSyncer(t, m, srv, testMember, dir2)
	mustPullDown(t, s2)
	if v, _ := loadUnitCursor(dir2); v != "2026-11-01T00:00:00Z" {
		t.Fatalf("unit_cursor.txt = %q, want the legacy since kept", v)
	}
	if got := loadUnitPullCursor(dir2); got.Kind != pullCursorOpaque || got.Value != "next-opaque" {
		t.Fatalf("loaded cursor = %+v, want v2 to win", got)
	}
}

// ownUnitWithEdits pulls N as the member's own node, then edits it locally.
func ownUnitWithEdits(t *testing.T, m *units.Manager, id string) {
	t.Helper()
	if _, err := m.Update(context.Background(), id, "my unpushed edit", nil); err != nil {
		t.Fatal(err)
	}
}

func assertKeptPersonalWithEdit(t *testing.T, m *units.Manager, id string) {
	t.Helper()
	u, ok := hasUnit(t, m, id)
	if !ok {
		t.Fatalf("%s was deleted; the member's own unit must be kept", id)
	}
	if u.Classification != units.ClassPersonal || u.Body != "my unpushed edit" {
		t.Fatalf("%s = class %q body %q, want personal with the un-pushed edit", id, u.Classification, u.Body)
	}
}

func withdrawnRow(id, team string) ContextPulledNode {
	at := "2026-11-15T05:00:00Z"
	return ContextPulledNode{ID: id, OrgID: testOrg, Kind: "doc", Classification: ClassTeamShared, TeamID: team,
		Audience: AudienceTeam, Version: 2, UpdatedAt: at, DeletedAt: &at, Withdrawn: true}
}

// The reviewer's scenario: the author moved N from T1 to T2, then left T2.
// The reset's re-snapshot serves N's withdrawal (Fleet's withdrawal row
// carries no author). N must be kept as personal with its edits.
func TestUnitScoped_WithdrawnOwnUnitIsKeptAsPersonal(t *testing.T) {
	for _, tc := range []struct {
		name      string
		resetBody map[string]any
		legacy    bool
		index     string // "", "missing", "corrupt", "pre-index"
	}{
		{"reset names T2", map[string]any{"reset": true, "reset_reason": ResetLeftTeam, "reset_team_ids": []string{"T2"}}, false, ""},
		{"reset names no team", map[string]any{"reset": true, "reset_reason": ResetLeftTeam}, false, ""},
		{"access_epoch on a since stream", nil, true, ""},
		{"index missing", map[string]any{"reset": true, "reset_reason": ResetLeftTeam}, false, "missing"},
		{"index corrupt", map[string]any{"reset": true, "reset_reason": ResetLeftTeam}, false, "corrupt"},
		{"synced before the index", map[string]any{"reset": true, "reset_reason": ResetLeftTeam}, false, "pre-index"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, m := openUnitSyncSQLite(t)
			dataDir := t.TempDir()
			srv := &scopedPullServer{}
			srv.answer = func(q string, n int) (int, any) {
				own := unitNode(uA, "T2", testMember)
				switch {
				case n == 1 && tc.legacy:
					return 200, map[string]any{"nodes": []ContextPulledNode{own}, "cursor": "2026-11-15T00:00:00Z", "access_epoch": 1}
				case n == 1:
					return 200, pullBody([]ContextPulledNode{own}, "u-1")
				case n == 2 && tc.legacy:
					return 200, map[string]any{"nodes": []any{}, "cursor": "", "access_epoch": 2}
				case n == 2:
					body := map[string]any{"nodes": []any{}, "next_cursor": "start"}
					for k, v := range tc.resetBody {
						body[k] = v
					}
					return 200, body
				default:
					return 200, pullBody([]ContextPulledNode{withdrawnRow(uA, "T1")}, "u-2")
				}
			}
			s := unitScopedSyncer(t, m, srv, testMember, dataDir)
			mustPullDown(t, s)
			ownUnitWithEdits(t, m, uA)
			switch tc.index {
			case "missing":
				_ = os.Remove(unitIndexPath(dataDir))
				s.index = newUnitIndex(dataDir)
			case "corrupt":
				_ = os.WriteFile(unitIndexPath(dataDir), []byte("{not json"), 0o600)
				s.index = newUnitIndex(dataDir)
			case "pre-index":
				s.index.remove(uA)
				s.index.flush()
			}
			mustPullDown(t, s)
			assertKeptPersonalWithEdit(t, m, uA)
		})
	}
}

func TestUnitScoped_WithdrawnHeldWhileIdentityUnknown(t *testing.T) {
	_, m := openUnitSyncSQLite(t)
	srv := &scopedPullServer{}
	srv.answer = func(q string, n int) (int, any) {
		if n == 1 {
			return 200, pullBody([]ContextPulledNode{unitNode(uA, "team-b", testAuthor)}, "u-1")
		}
		return 200, pullBody([]ContextPulledNode{withdrawnRow(uA, "team-b")}, "u-2")
	}
	s := unitScopedSyncer(t, m, srv, "", "")
	mustPullDown(t, s)
	mustPullDown(t, s)
	if _, ok := hasUnit(t, m, uA); !ok {
		t.Fatal("a withdrawn tombstone removed a unit while the identity was unknown")
	}
	if st := s.Status(); st.TombstonesHeldIdentityUnknown != 1 || st.Cursor != "u-1" {
		t.Fatalf("status = %+v, want held with the cursor not advanced", st)
	}
}

// resetScript: first page serves nodes, then a reset, then the re-snapshot.
func resetScript(first []ContextPulledNode, reset map[string]any, resnap func() (int, any)) func(q string, n int) (int, any) {
	return func(q string, n int) (int, any) {
		switch {
		case n == 1:
			return 200, pullBody(first, "u-1")
		case strings.Contains(q, "cursor=u-1"):
			body := map[string]any{"nodes": []any{}, "next_cursor": "start", "reset": true, "reset_reason": ResetLeftTeam}
			for k, v := range reset {
				body[k] = v
			}
			return 200, body
		default:
			return resnap()
		}
	}
}

func TestUnitScoped_ResetHeldWhileIdentityUnknown(t *testing.T) {
	_, m := openUnitSyncSQLite(t)
	dataDir := t.TempDir()
	srv := &scopedPullServer{}
	srv.answer = resetScript(
		[]ContextPulledNode{unitNode(uA, "team-a", testAuthor), unitNode(uB, "team-a", testMember)},
		map[string]any{"reset_team_ids": []string{"team-a"}},
		func() (int, any) { return 200, pullBody(nil, "u-2") },
	)
	s := unitScopedSyncer(t, m, srv, testMember, dataDir)
	mustPullDown(t, s)
	ownUnitWithEdits(t, m, uB)
	// Enroll in flight / failed (ErrNodeIDInvalid): identity.json is gone.
	if err := os.Remove(IdentityFilePath(dataDir)); err != nil {
		t.Fatal(err)
	}
	mustPullDown(t, s)
	for _, id := range []string{uA, uB} {
		if u, ok := hasUnit(t, m, id); !ok || u.Classification == units.ClassPersonal {
			t.Fatalf("%s changed while the identity was unknown: %+v %v", id, u, ok)
		}
	}
	// The reset came while the identity was unknown: the mark names no
	// account and is adopted by the first known one.
	if len(loadUnitSnapshotMarks(dataDir)) != 1 {
		t.Fatal("the snapshot mark was dropped while held")
	}
	if s.Status().ResetHeld == 0 {
		t.Fatal("reset_held not counted")
	}
	// The identity comes back: the held sweep runs.
	if err := SaveIdentity(dataDir, Identity{UserID: testMember, OrgID: testOrg}); err != nil {
		t.Fatal(err)
	}
	mustPullDown(t, s)
	if _, ok := hasUnit(t, m, uA); ok {
		t.Fatal("the teammate's unit survived once the identity was known")
	}
	assertKeptPersonalWithEdit(t, m, uB)
	if len(loadUnitSnapshotMarks(dataDir)) != 0 {
		t.Fatal("mark left after the sweep ran")
	}
}

func TestUnitScoped_ResetNeverSweepsAnotherAccountsUnits(t *testing.T) {
	_, m := openUnitSyncSQLite(t)
	dataDir := t.TempDir()
	srv := &scopedPullServer{}
	failResnap := true
	srv.answer = resetScript(
		[]ContextPulledNode{unitNode(uA, "team-a", testAuthor), unitNode(uB, "team-a", testMember)},
		nil,
		func() (int, any) {
			if failResnap {
				return 500, map[string]any{"code": "boom"}
			}
			return 200, pullBody(nil, "u-2")
		},
	)
	// Account A starts a reset; the re-snapshot fails, so the mark persists.
	s := unitScopedSyncer(t, m, srv, testMember, dataDir)
	mustPullDown(t, s)
	if _, err := s.PullDown(context.Background()); err == nil {
		t.Fatal("want the failed re-snapshot's error")
	}
	if mk, ok := markFor(dataDir, testMember); !ok || mk.UserID != testMember {
		t.Fatalf("mark = %+v %v, want account A recorded", mk, ok)
	}
	// Restart into account B (same org): B's snapshot finishes, but the
	// sweep belongs to A and is held.
	srv.mu.Lock()
	failResnap = false
	srv.mu.Unlock()
	if err := SaveIdentity(dataDir, Identity{UserID: "u-other-account", OrgID: testOrg}); err != nil {
		t.Fatal(err)
	}
	s2 := unitScopedSyncer(t, m, srv, "", dataDir)
	mustPullDown(t, s2)
	for _, id := range []string{uA, uB} {
		if _, ok := hasUnit(t, m, id); !ok {
			t.Fatalf("account B's sweep removed account A's unit %s", id)
		}
	}
	if _, ok := markFor(dataDir, testMember); !ok {
		t.Fatal("A's mark was dropped under B")
	}
	// Even B's own sweep keeps units recorded for A.
	if v := dropVerdictFor(units.Unit{}, units.SyncState{NodeID: uA}, s2.index, "u-other-account"); v != dropKeep {
		t.Fatalf("verdict for A's unit under B = %v, want keep", v)
	}
}

// The sweep runs only after a COMPLETE pull: a full first page does not end
// the re-snapshot, an error on page 2 sweeps nothing, a short page 2 does.
func TestUnitScoped_SweepOnlyAfterCompletePull(t *testing.T) {
	_, m := openUnitSyncSQLite(t)
	srv := &scopedPullServer{}
	page2 := "error"
	srv.answer = resetScript(
		[]ContextPulledNode{unitNode(uA, "team-b", testAuthor), unitNode(uB, "team-b", testAuthor), unitNode(uC, "team-b", testAuthor)},
		nil,
		nil,
	)
	inner := srv.answer
	srv.answer = func(q string, n int) (int, any) {
		if n <= 2 {
			return inner(q, n)
		}
		if !strings.Contains(q, "cursor=") { // page 1 of the re-snapshot: full (2 = page size)
			return 200, pullBody([]ContextPulledNode{unitNode(uA, "team-b", testAuthor), unitNode(uB, "team-b", testAuthor)}, "p1")
		}
		if page2 == "error" {
			return 500, map[string]any{"code": "boom"}
		}
		return 200, pullBody(nil, "p2") // short page: the pull is complete
	}
	s := unitScopedSyncer(t, m, srv, testMember, "")
	s.pageSize = 2
	// Page 1 of the first pull is 3 nodes (>= page size): no snapshot yet.
	mustPullDown(t, s)
	if _, err := s.PullDown(context.Background()); err == nil {
		t.Fatal("want page 2's error")
	}
	if _, ok := hasUnit(t, m, uC); !ok {
		t.Fatal("swept after a full page 1 and a failed page 2 (the pull was not complete)")
	}
	if q := srv.snapshotQueries(); !strings.Contains(q[len(q)-1], "limit=2") {
		t.Fatalf("query %q, want the explicit limit", q[len(q)-1])
	}
	srv.mu.Lock()
	page2 = "short"
	srv.mu.Unlock()
	mustPullDown(t, s)
	if _, ok := hasUnit(t, m, uC); ok {
		t.Fatal("the unit the complete re-snapshot did not serve survived")
	}
	for _, id := range []string{uA, uB} {
		if _, ok := hasUnit(t, m, id); !ok {
			t.Fatalf("served unit %s dropped", id)
		}
	}
}

// A cursor refusal during a reset's re-snapshot keeps the sweep.
func TestUnitScoped_CursorRefusalDuringResetKeepsSweep(t *testing.T) {
	_, m := openUnitSyncSQLite(t)
	srv := &scopedPullServer{}
	refused := false
	srv.answer = func(q string, n int) (int, any) {
		switch {
		case n == 1:
			return 200, pullBody([]ContextPulledNode{unitNode(uA, "team-b", testAuthor), unitNode(uC, "team-b", testAuthor), unitNode(uD, "team-b", testAuthor)}, "u-1")
		case strings.Contains(q, "cursor=u-1"):
			return 200, map[string]any{"nodes": []any{}, "next_cursor": "start", "reset": true, "reset_reason": ResetLeftTeam}
		case !strings.Contains(q, "cursor="):
			return 200, pullBody([]ContextPulledNode{unitNode(uA, "team-b", testAuthor), unitNode(uC, "team-b", testAuthor)}, "p1")
		case !refused:
			refused = true
			return 400, map[string]any{"code": codeInvalidCursor}
		default:
			return 200, pullBody(nil, "p2")
		}
	}
	s := unitScopedSyncer(t, m, srv, testMember, "")
	s.pageSize = 2
	mustPullDown(t, s)
	mustPullDown(t, s) // reset → page 1 full → page 2 refused → cursorless again → ... → short
	mustPullDown(t, s)
	if _, ok := hasUnit(t, m, uD); ok {
		t.Fatal("a cursor refusal mid re-snapshot cancelled the reset's sweep")
	}
}

// A unit shared while a re-snapshot is in progress counts as served.
func TestUnitScoped_PushDuringSnapshotIsNotSwept(t *testing.T) {
	_, m := openUnitSyncSQLite(t)
	ctx := context.Background()
	srv := &scopedPullServer{}
	stage := 0
	srv.answer = func(q string, n int) (int, any) {
		switch {
		case n == 1:
			return 200, pullBody([]ContextPulledNode{unitNode(uA, "team-b", testAuthor), unitNode(uC, "team-b", testAuthor), unitNode(uD, "team-b", testAuthor)}, "u-1")
		case strings.Contains(q, "cursor=u-1"):
			return 200, map[string]any{"nodes": []any{}, "next_cursor": "start", "reset": true, "reset_reason": ResetLeftTeam}
		case !strings.Contains(q, "cursor="):
			return 200, pullBody([]ContextPulledNode{unitNode(uA, "team-b", testAuthor), unitNode(uC, "team-b", testAuthor)}, "p1")
		case stage == 0:
			return 500, map[string]any{"code": "boom"}
		default:
			return 200, pullBody(nil, "p2")
		}
	}
	s := unitScopedSyncer(t, m, srv, testMember, "")
	s.pageSize = 2
	mustPullDown(t, s)
	if _, err := s.PullDown(ctx); err == nil {
		t.Fatal("want page 2's error")
	}
	mine, err := m.Create(ctx, units.Unit{Kind: units.KindDoc, Scope: units.ScopeGlobal, Classification: units.ClassTeam,
		LoadPolicy: units.LoadOnDemand, Title: "shared just now", Body: "b"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.PushUnit(ctx, mine.ID); err != nil {
		t.Fatal(err)
	}
	srv.mu.Lock()
	stage = 1
	srv.mu.Unlock()
	mustPullDown(t, s)
	if u, ok := hasUnit(t, m, mine.ID); !ok || u.Classification != units.ClassTeam {
		t.Fatalf("a unit shared during the re-snapshot was swept: %+v %v", u, ok)
	}
	if _, ok := hasUnit(t, m, uD); ok {
		t.Fatal("unserved teammate unit survived")
	}
}

func markFor(dataDir, user string) (unitSnapshotMark, bool) {
	m, ok := loadUnitSnapshotMarks(dataDir)[snapshotAccount{UserID: user, OrgID: testOrg}.key()]
	return m, ok
}

// ── Delta review (dada4a91): revocation leak and per-account marks ──

// dropVerdictFor in rule order, over real sqlite.
func TestUnitScoped_DropVerdictRules(t *testing.T) {
	_, m := openUnitSyncSQLite(t)
	ctx := context.Background()
	srv := &scopedPullServer{}
	srv.answer = resetScript(
		[]ContextPulledNode{
			unitNode(uA, "team-a", testAuthor), // teammate's, pulled
			unitNode(uB, "team-a", testMember), // own, pulled (index author self)
			unitNode(uC, "team-a", testAuthor), // teammate's, edited locally
		},
		map[string]any{"reset_team_ids": []string{"team-a"}},
		func() (int, any) { return 200, pullBody(nil, "u-2") },
	)
	dataDir := t.TempDir()
	s := unitScopedSyncer(t, m, srv, testMember, dataDir)
	mustPullDown(t, s)
	if _, err := m.Update(ctx, uC, "edited here", nil); err != nil {
		t.Fatal(err)
	}
	// uD: a pre-index teammate unit (pulled before the index existed): node
	// id as its id, a sidecar, no index row, no edits.
	pre := unitNode(uD, "team-a", testAuthor)
	mp, _, err := s.mapper.PulledNodeToUnit(pre)
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := m.CreateWithSyncState(ctx, mp, units.SyncState{NodeID: uD, SyncedServerVersion: 1, Classification: "team_shared"}); err != nil {
		t.Fatal(err)
	}
	// A unit created on this device and synced, with no index row (pushed
	// before the index existed): its ULID differs from its wire node id.
	local, err := m.Create(ctx, units.Unit{Kind: units.KindDoc, Scope: units.ScopeGlobal, Classification: units.ClassTeam,
		LoadPolicy: units.LoadOnDemand, Title: "mine", Body: "local words"})
	if err != nil {
		t.Fatal(err)
	}
	const localNode = "6b2f6f0e-0000-4000-8000-0000000028e5"
	if _, err := m.UpsertSyncState(ctx, units.SyncState{UnitID: local.ID, NodeID: localNode, SyncedServerVersion: 0,
		SyncedLocalVersion: local.Version, Classification: "team_shared"}); err != nil {
		t.Fatal(err)
	}
	// The local unit lived in the lost team too.
	s.index.put(localNode, UnitNodeLabels{TeamID: "team-a"})
	s.index.flush()

	mustPullDown(t, s) // reset naming team-a, then the sweep

	if _, ok := hasUnit(t, m, uA); ok {
		t.Error("rule 5: a pulled teammate unit with no edits survived the reset (access leak)")
	}
	if _, ok := hasUnit(t, m, uD); ok {
		t.Error("rule 5: a pre-index teammate unit with no edits was kept (access leak)")
	}
	if u, ok := hasUnit(t, m, uB); !ok || u.Classification != units.ClassPersonal {
		t.Errorf("rule 2: own pulled unit = %+v %v, want personal", u, ok)
	}
	if u, ok := hasUnit(t, m, uC); !ok || u.Classification != units.ClassPersonal || u.Body != "edited here" {
		t.Errorf("rule 3: unit with un-pushed edits = %+v %v, want personal with the edit", u, ok)
	}
	if u, ok := hasUnit(t, m, local.ID); !ok || u.Classification != units.ClassPersonal || u.Body != "local words" {
		t.Errorf("rule 4: locally created unit with no index row = %+v %v, want personal", u, ok)
	}
}

// B's own reset while A's sweep is held must not overwrite A's mark; each
// sweep runs only under its own account.
func TestUnitScoped_SnapshotMarksArePerAccount(t *testing.T) {
	_, m := openUnitSyncSQLite(t)
	dataDir := t.TempDir()
	const userB = "u-account-b"
	failResnap := true
	srv := &scopedPullServer{}
	srv.answer = func(q string, n int) (int, any) {
		switch {
		case n == 1: // A's first pull
			return 200, pullBody([]ContextPulledNode{unitNode(uA, "team-a", testAuthor), unitNode(uC, "team-a", testAuthor)}, "u-1")
		case strings.Contains(q, "cursor=u-1"), strings.Contains(q, "cursor=b-1"):
			return 200, map[string]any{"nodes": []any{}, "next_cursor": "start", "reset": true, "reset_reason": ResetLeftTeam}
		case failResnap:
			return 500, map[string]any{"code": "boom"}
		default:
			// Re-snapshots serve uC only.
			return 200, pullBody([]ContextPulledNode{unitNode(uC, "team-a", testAuthor)}, "end")
		}
	}
	sA := unitScopedSyncer(t, m, srv, testMember, dataDir)
	mustPullDown(t, sA)
	if _, err := sA.PullDown(context.Background()); err == nil {
		t.Fatal("want A's failed re-snapshot")
	}
	if _, ok := markFor(dataDir, testMember); !ok {
		t.Fatal("A's mark missing")
	}

	// Restart as B, who then gets a reset of its own and completes it.
	srv.mu.Lock()
	failResnap = false
	srv.mu.Unlock()
	if err := SaveIdentity(dataDir, Identity{UserID: userB, OrgID: testOrg}); err != nil {
		t.Fatal(err)
	}
	if err := saveUnitPullCursor(dataDir, pullCursor{Kind: pullCursorOpaque, Value: "b-1"}); err != nil {
		t.Fatal(err)
	}
	sB := unitScopedSyncer(t, m, srv, "", dataDir)
	mustPullDown(t, sB)
	if _, ok := markFor(dataDir, testMember); !ok {
		t.Fatal("B's reset overwrote A's pending mark")
	}
	if _, ok := markFor(dataDir, userB); ok {
		t.Fatal("B's finished sweep left its own mark")
	}
	if _, ok := hasUnit(t, m, uA); !ok {
		t.Fatal("B's sweep removed a unit recorded for A")
	}

	// Back to A: A's held sweep resumes and runs.
	if err := SaveIdentity(dataDir, Identity{UserID: testMember, OrgID: testOrg}); err != nil {
		t.Fatal(err)
	}
	sA2 := unitScopedSyncer(t, m, srv, "", dataDir)
	mustPullDown(t, sA2)
	if _, ok := hasUnit(t, m, uA); ok {
		t.Fatal("A's resumed sweep did not remove the unit A lost")
	}
	if _, ok := hasUnit(t, m, uC); !ok {
		t.Fatal("re-served unit dropped")
	}
	if len(loadUnitSnapshotMarks(dataDir)) != 0 {
		t.Fatalf("marks left: %+v", loadUnitSnapshotMarks(dataDir))
	}
}
