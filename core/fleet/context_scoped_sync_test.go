package fleet

// context_scoped_sync_test.go — kenaz-fleet spec 018 H-2 / H-3 on the
// Curated lane: the opaque cursor, cursor refusals, loss-of-access resets,
// withdrawn tombstones, audience labels, and per-item push results with an
// explicit audience.

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"
)

// scopedPullServer answers pull with a script function and records every
// query. Race-safe: the handler runs on the server goroutine.
type scopedPullServer struct {
	mu      sync.Mutex
	queries []string
	answer  func(q string, n int) (int, any)
	pushes  []map[string]any
	push    func(req map[string]any) (int, any)
}

func (s *scopedPullServer) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	s.mu.Lock()
	defer s.mu.Unlock()
	switch {
	case r.Method == http.MethodGet && r.URL.Path == "/api/v1/context/pull":
		s.queries = append(s.queries, r.URL.RawQuery)
		status, body := s.answer(r.URL.RawQuery, len(s.queries))
		w.WriteHeader(status)
		_ = json.NewEncoder(w).Encode(body)
	case r.Method == http.MethodPost && r.URL.Path == "/api/v1/context/push":
		var req map[string]any
		_ = json.NewDecoder(r.Body).Decode(&req)
		s.pushes = append(s.pushes, req)
		status, body := http.StatusOK, any(map[string]any{"accepted_nodes": 1, "conflicts": []any{}})
		if s.push != nil {
			status, body = s.push(req)
		}
		w.WriteHeader(status)
		_ = json.NewEncoder(w).Encode(body)
	default:
		http.NotFound(w, r)
	}
}

func (s *scopedPullServer) snapshotQueries() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]string(nil), s.queries...)
}

func (s *scopedPullServer) snapshotPushes() []map[string]any {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]map[string]any(nil), s.pushes...)
}

func scopedSyncer(t *testing.T, srv *scopedPullServer, self string) *ContextGraphSyncer {
	t.Helper()
	hs := httptest.NewServer(srv)
	t.Cleanup(hs.Close)
	stubTokens(t, TokenSet{AccessToken: "at-scoped", RefreshToken: "rt", ExpiresAt: time.Now().Add(time.Hour)})
	s := NewContextGraphSyncer(makeTestClient(t, hs.URL), t.TempDir(), makeCapPollerWithTeamCap(t))
	s.WithSelfUserID(func() string { return self })
	return s
}

func curatedNode(id, team, owner string) ContextPulledNode {
	return ContextPulledNode{
		ID: id, OrgID: "org-1", Kind: "guidance", UnitKind: "doc", Title: "T " + id, Body: "body " + id,
		Classification: ClassTeamShared, TeamID: team, OwnerUserID: owner, Audience: AudienceTeam,
		Version: 1, UpdatedAt: "2026-11-15T00:00:00Z",
	}
}

func pullBody(nodes []ContextPulledNode, next string) map[string]any {
	return map[string]any{"nodes": nodes, "edges": []any{}, "next_cursor": next, "access_epoch": 0}
}

func liveIDs(s *ContextGraphSyncer) map[string]bool {
	out := map[string]bool{}
	for _, e := range s.PulledEntries() {
		out[e.ID] = true
	}
	return out
}

const (
	nA = "6b2f6f0e-0000-4000-8000-0000000018a1"
	nB = "6b2f6f0e-0000-4000-8000-0000000018b2"
	nC = "6b2f6f0e-0000-4000-8000-0000000018c3"
)

func TestScopedPull_OpaqueCursorIsSentBackAsCursor(t *testing.T) {
	srv := &scopedPullServer{answer: func(q string, n int) (int, any) {
		if n == 1 {
			return 200, pullBody([]ContextPulledNode{curatedNode(nA, "team-a", "u-x")}, "opaque-1")
		}
		return 200, pullBody(nil, "opaque-2")
	}}
	s := scopedSyncer(t, srv, "u-me")
	ctx := context.Background()
	for i := 0; i < 2; i++ {
		if _, err := s.PullDelta(ctx); err != nil {
			t.Fatal(err)
		}
	}
	q := srv.snapshotQueries()
	if len(q) != 2 || strings.Contains(q[0], "cursor=") || strings.Contains(q[0], "since=") {
		t.Fatalf("queries = %q; the first pull must carry no cursor", q)
	}
	if !strings.Contains(q[1], "cursor=opaque-1") || strings.Contains(q[1], "since=") {
		t.Fatalf("second query %q, want cursor=opaque-1 and no since", q[1])
	}
	if got := s.Status().Cursor; got != "opaque-2" {
		t.Fatalf("stored cursor %q, want the latest next_cursor", got)
	}
	// The served audience reaches the entry.
	if e := s.PulledEntries(); len(e) != 1 || e[0].Audience != AudienceTeam {
		t.Fatalf("entries = %+v, want audience team", e)
	}
}

func TestScopedPull_CursorRefusedStartsOneCleanFullPull(t *testing.T) {
	for _, code := range []string{codeCursorFilterMismatch, codeInvalidCursor} {
		t.Run(code, func(t *testing.T) {
			srv := &scopedPullServer{answer: func(q string, n int) (int, any) {
				if strings.Contains(q, "cursor=") {
					return 400, map[string]any{"code": code, "message": "no"}
				}
				if n == 1 {
					return 200, pullBody([]ContextPulledNode{curatedNode(nA, "team-a", "u-x")}, "opaque-1")
				}
				return 200, pullBody([]ContextPulledNode{curatedNode(nB, "team-a", "u-x")}, "opaque-fresh")
			}}
			s := scopedSyncer(t, srv, "u-me")
			ctx := context.Background()
			if _, err := s.PullDelta(ctx); err != nil {
				t.Fatal(err)
			}
			if _, err := s.PullDelta(ctx); err != nil {
				t.Fatalf("refused cursor: %v, want a clean full re-pull", err)
			}
			q := srv.snapshotQueries()
			if len(q) != 3 || !strings.Contains(q[1], "cursor=opaque-1") || strings.Contains(q[2], "cursor=") {
				t.Fatalf("queries = %q; want first, refused, one cursorless re-pull", q)
			}
			// The full re-pull is a snapshot: A was not served again, so it goes.
			if got := liveIDs(s); !got[nB] || got[nA] || len(got) != 1 {
				t.Fatalf("live = %v, want only %s", got, nB)
			}
		})
	}
}

func TestScopedPull_CursorRefusalNeverLoops(t *testing.T) {
	// A broken server that refuses everything: one retry, then an error.
	srv := &scopedPullServer{answer: func(q string, n int) (int, any) {
		if n == 1 {
			return 200, pullBody(nil, "opaque-1")
		}
		return 400, map[string]any{"code": codeInvalidCursor}
	}}
	s := scopedSyncer(t, srv, "u-me")
	ctx := context.Background()
	if _, err := s.PullDelta(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := s.PullDelta(ctx); err == nil {
		t.Fatal("a server refusing the cursorless pull too must surface an error")
	}
	if q := srv.snapshotQueries(); len(q) != 3 {
		t.Fatalf("queries = %d, want 3 (first, refused cursor, one cursorless try)", len(q))
	}
}

func TestScopedPull_ResetDropsNamedTeamsAndReSnapshots(t *testing.T) {
	srv := &scopedPullServer{}
	srv.answer = func(q string, n int) (int, any) {
		switch {
		case n == 1:
			return 200, pullBody([]ContextPulledNode{
				curatedNode(nA, "team-a", "u-x"), curatedNode(nB, "team-b", "u-x"), curatedNode(nC, "team-b", "u-x"),
			}, "opaque-1")
		case strings.Contains(q, "cursor="):
			return 200, map[string]any{"nodes": []any{}, "edges": []any{}, "next_cursor": "start", "access_epoch": 3,
				"reset": true, "reset_reason": ResetLeftTeam, "reset_team_ids": []string{"team-a"}}
		default:
			// The re-snapshot serves B only (C went too, unannounced).
			return 200, pullBody([]ContextPulledNode{curatedNode(nB, "team-b", "u-x")}, "opaque-2")
		}
	}
	s := scopedSyncer(t, srv, "u-me")
	var merged [][]ContextNodeEntry
	var mmu sync.Mutex
	s.SetLibraryMerger(func(es []ContextNodeEntry) {
		mmu.Lock()
		merged = append(merged, es)
		mmu.Unlock()
	})
	ctx := context.Background()
	if _, err := s.pullAndMerge(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := s.pullAndMerge(ctx); err != nil {
		t.Fatal(err)
	}
	if got := liveIDs(s); len(got) != 1 || !got[nB] {
		t.Fatalf("live after reset = %v, want only %s", got, nB)
	}
	q := srv.snapshotQueries()
	if len(q) != 3 || strings.Contains(q[2], "cursor=") {
		t.Fatalf("queries = %q; want first, reset, one cursorless re-snapshot (no loop)", q)
	}
	st := s.Status()
	if st.ResetCount != 1 || st.LastResetReason != ResetLeftTeam || st.Cursor != "opaque-2" {
		t.Fatalf("status = %+v", st)
	}
	// The merger saw the drops as tombstones (it removes copies only then).
	mmu.Lock()
	last := merged[len(merged)-1]
	mmu.Unlock()
	tomb := map[string]bool{}
	for _, e := range last {
		if e.DeletedAt != nil {
			tomb[e.ID] = true
			if e.Body != "" || e.Title != "" {
				t.Fatalf("a dropped entry kept content: %+v", e)
			}
		}
	}
	if !tomb[nA] || !tomb[nC] || tomb[nB] {
		t.Fatalf("merger tombstones = %v, want %s and %s", tomb, nA, nC)
	}
}

func TestScopedPull_ResetWithoutTeamsDropsEverythingUntilReServed(t *testing.T) {
	srv := &scopedPullServer{}
	srv.answer = func(q string, n int) (int, any) {
		switch {
		case n == 1:
			return 200, pullBody([]ContextPulledNode{curatedNode(nA, "team-a", "u-x"), curatedNode(nB, "team-b", "u-x")}, "opaque-1")
		case strings.Contains(q, "cursor="):
			return 200, map[string]any{"nodes": []any{}, "edges": []any{}, "next_cursor": "start", "reset": true, "reset_reason": ResetInheritanceOff}
		default:
			return 500, map[string]any{"code": "boom"} // the re-snapshot fails
		}
	}
	s := scopedSyncer(t, srv, "u-me")
	var mergedTomb int
	var mmu sync.Mutex
	s.SetLibraryMerger(func(es []ContextNodeEntry) {
		mmu.Lock()
		defer mmu.Unlock()
		mergedTomb = 0
		for _, e := range es {
			if e.DeletedAt != nil {
				mergedTomb++
			}
		}
	})
	ctx := context.Background()
	if _, err := s.pullAndMerge(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := s.pullAndMerge(ctx); err == nil {
		t.Fatal("want the failed re-snapshot's error")
	}
	if got := liveIDs(s); len(got) != 0 {
		t.Fatalf("live after a reset naming no team = %v, want none until re-served", got)
	}
	mmu.Lock()
	defer mmu.Unlock()
	if mergedTomb != 2 {
		t.Fatalf("merger saw %d tombstones, want 2 even though the re-snapshot failed", mergedTomb)
	}
}

func TestScopedPull_WithdrawnTombstoneDropsCopyAndLocation(t *testing.T) {
	own := curatedNode(nA, "team-a", "u-me")
	srv := &scopedPullServer{}
	srv.answer = func(q string, n int) (int, any) {
		if n == 1 {
			return 200, pullBody([]ContextPulledNode{own}, "opaque-1")
		}
		at := "2026-11-15T01:00:00Z"
		w := ContextPulledNode{ID: nA, OrgID: "org-1", Kind: "guidance", UnitKind: "doc", Classification: ClassTeamShared,
			TeamID: "team-a", Audience: AudienceTeam, Version: 4, UpdatedAt: at, DeletedAt: &at, Withdrawn: true}
		return 200, pullBody([]ContextPulledNode{w}, "opaque-2")
	}
	s := scopedSyncer(t, srv, "u-me")
	ctx := context.Background()
	if _, err := s.PullDelta(ctx); err != nil {
		t.Fatal(err)
	}
	if len(s.PulledShareLocations()) != 1 || !s.ServesLive(nA) {
		t.Fatal("setup: own node not recorded")
	}
	if _, err := s.PullDelta(ctx); err != nil {
		t.Fatal(err)
	}
	if s.ServesLive(nA) || len(s.PulledEntries()) != 0 {
		t.Fatal("a withdrawn tombstone must drop the working copy")
	}
	if len(s.PulledShareLocations()) != 0 {
		t.Fatal("a withdrawn tombstone must drop the location row")
	}
	for _, e := range s.pulledEntriesForMerger() {
		if e.ID == nA && (e.Version != 0 || e.Body != "") {
			t.Fatalf("withdrawn entry kept version/content: %+v", e)
		}
	}
	if len(srv.snapshotPushes()) != 0 {
		t.Fatal("a withdrawn tombstone must never push anything")
	}
	if got := s.Status().WithdrawnApplied; got != 1 {
		t.Fatalf("withdrawn_applied = %d", got)
	}
}

func TestScopedPull_LegacySinceStreamResetsOnAccessEpochChange(t *testing.T) {
	srv := &scopedPullServer{}
	srv.answer = func(q string, n int) (int, any) {
		switch n {
		case 1:
			return 200, map[string]any{"nodes": []ContextPulledNode{curatedNode(nA, "team-a", "u-x")}, "cursor": "2026-11-15T00:00:00Z", "access_epoch": 1}
		case 2:
			return 200, map[string]any{"nodes": []any{}, "cursor": "", "access_epoch": 2}
		default:
			return 200, map[string]any{"nodes": []any{}, "cursor": "", "access_epoch": 2}
		}
	}
	s := scopedSyncer(t, srv, "u-me")
	ctx := context.Background()
	for i := 0; i < 2; i++ {
		if _, err := s.PullDelta(ctx); err != nil {
			t.Fatal(err)
		}
	}
	q := srv.snapshotQueries()
	if !strings.Contains(q[1], "since=") {
		t.Fatalf("legacy stream query %q, want since=", q[1])
	}
	if len(q) != 3 || strings.Contains(q[2], "since=") {
		t.Fatalf("queries = %q; an epoch change re-snapshots once", q)
	}
	if len(s.PulledEntries()) != 0 {
		t.Fatal("A was not re-served after the epoch change; it must be gone")
	}
}
