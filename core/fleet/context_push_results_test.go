package fleet

// context_push_results_test.go — kenaz-fleet spec 018 H-3: the explicit
// audience on the wire, per-item push results (published / pending_review /
// refused) on both lanes, and the default-team lookup.

import (
	"context"
	"errors"
	"testing"

	"github.com/kameas-ai/kenaz-harness/core/units"
)

// ── Push: explicit audience and per-item results (H-3) ──────────────────────

func pushSyncer(t *testing.T, reply func(req map[string]any) (int, any)) (*ContextGraphSyncer, *scopedPullServer) {
	t.Helper()
	srv := &scopedPullServer{answer: func(string, int) (int, any) { return 200, pullBody(nil, "x") }, push: reply}
	return scopedSyncer(t, srv, "u-me"), srv
}

func firstNode(req map[string]any) map[string]any {
	ns, _ := req["nodes"].([]any)
	if len(ns) == 0 {
		return nil
	}
	n, _ := ns[0].(map[string]any)
	return n
}

func TestScopedPush_ExplicitAudienceOnTheWire(t *testing.T) {
	s, srv := pushSyncer(t, nil)
	ctx := context.Background()
	team := "team-b"
	cases := []struct {
		entry      ContextNodeEntry
		audience   string
		governed   any
		class      string
		teamOnWire any
	}{
		{ContextNodeEntry{ID: "notes/a.md", Layer: "team", Kind: "guidance", Title: "a", Body: "a", TeamID: &team, Audience: AudienceTeam}, "team", nil, "team_shared", "team-b"},
		{ContextNodeEntry{ID: "notes/b.md", Layer: "team", Kind: "guidance", Title: "b", Body: "b", TeamID: &team, Audience: AudienceOrg}, "org", false, "team_shared", "team-b"},
		{ContextNodeEntry{ID: "notes/c.md", Layer: "org", Kind: "guidance", Title: "c", Body: "c", TeamID: &team, Audience: AudienceOrg, Governed: true}, "org", true, "org_shared", nil},
	}
	for i, c := range cases {
		if _, err := s.PushEntry(ctx, c.entry, nil); err != nil {
			t.Fatalf("case %d: %v", i, err)
		}
		n := firstNode(srv.snapshotPushes()[i])
		if n["audience"] != c.audience || n["governed"] != c.governed || n["classification"] != c.class || n["team_id"] != c.teamOnWire {
			t.Fatalf("case %d node on the wire = %v", i, n)
		}
	}
	if _, err := s.PushEntry(ctx, ContextNodeEntry{ID: "notes/d.md", Layer: "team", Kind: "guidance", Audience: AudienceTeam}, nil); !errors.Is(err, ErrAudienceTeamNeedsTeam) {
		t.Fatalf("team audience with no team: %v", err)
	}
}

func TestScopedPush_ResultsMapping(t *testing.T) {
	var status, reason, mr string
	s, _ := pushSyncer(t, func(req map[string]any) (int, any) {
		id, _ := firstNode(req)["id"].(string)
		row := map[string]any{"id": id, "status": status, "reason": reason, "merge_request_id": nil}
		if mr != "" {
			row["merge_request_id"] = mr
		}
		acc := 0
		if status == PushStatusPublished {
			acc = 1
		}
		return 200, map[string]any{"accepted_nodes": acc, "conflicts": []any{}, "results": []any{row}}
	})
	ctx := context.Background()
	team := "team-b"
	entry := func(id string) ContextNodeEntry {
		return ContextNodeEntry{ID: id, Layer: "team", Kind: "guidance", Title: "t", Body: "b", TeamID: &team, Version: 2}
	}

	status, reason, mr = PushStatusPendingReview, "", "mr-1"
	res, err := s.PushEntry(ctx, entry("notes/p.md"), nil)
	if err != nil || res.Item.Status != PushStatusPendingReview || res.Item.MergeRequest() != "mr-1" {
		t.Fatalf("pending: res=%+v err=%v", res, err)
	}
	if len(s.PulledShareLocations()) != 0 {
		t.Fatal("pending_review wrote nothing on Fleet; no location may be recorded")
	}

	for _, r := range []string{"team_not_found", "previously_rejected"} {
		status, reason, mr = PushStatusRefused, r, ""
		_, err = s.PushEntry(ctx, entry("notes/r-"+r+".md"), nil)
		var rej *ContextPushRejectedError
		if !errors.As(err, &rej) || rej.Reason != r {
			t.Fatalf("refused %s: err=%v", r, err)
		}
	}
	if PushRefusalText("team_not_found") != "That team no longer exists" ||
		PushRefusalText("previously_rejected") != "This exact text was already rejected" {
		t.Fatal("plain-words refusal text changed")
	}

	status, reason, mr = PushStatusPublished, "", ""
	res, err = s.PushEntry(ctx, entry("notes/ok.md"), nil)
	if err != nil || res.Item.Status != PushStatusPublished || len(s.PulledShareLocations()) != 1 {
		t.Fatalf("published: res=%+v err=%v locs=%d", res, err, len(s.PulledShareLocations()))
	}
}

func TestScopedPush_LegacyResponseDerivesItem(t *testing.T) {
	s, _ := pushSyncer(t, func(req map[string]any) (int, any) {
		id, _ := firstNode(req)["id"].(string)
		return 200, map[string]any{"accepted_nodes": 0, "conflicts": []any{map[string]any{"node_id": id, "server_version": 3, "client_version": 1}}}
	})
	team := "team-b"
	res, err := s.PushEntry(context.Background(), ContextNodeEntry{ID: "notes/v.md", Layer: "team", Kind: "guidance", TeamID: &team}, nil)
	if err != nil || res.Item.Status != PushStatusRefused || res.Item.Reason != "version_conflict" {
		t.Fatalf("legacy conflict: res=%+v err=%v", res, err)
	}
}

func TestDefaultTeamID_FromMeTeams(t *testing.T) {
	s, _ := pushSyncer(t, nil)
	calls := 0
	s.WithMeTeams(func(context.Context) ([]MeTeam, error) {
		calls++
		return []MeTeam{{ID: "team-b"}, {ID: "root", IsDefault: true}}, nil
	})
	for i := 0; i < 3; i++ {
		if id, ok := s.DefaultTeamID(context.Background()); !ok || id != "root" {
			t.Fatalf("default team = %q %v", id, ok)
		}
	}
	if calls != 1 {
		t.Fatalf("/me read %d times, want 1 (cached)", calls)
	}
	s2, _ := pushSyncer(t, nil)
	s2.WithMeTeams(func(context.Context) ([]MeTeam, error) { return nil, errors.New("down") })
	if _, ok := s2.DefaultTeamID(context.Background()); ok {
		t.Fatal("an unknown default team must report ok=false")
	}
}

func TestUnitScoped_PushPendingOrRefusedStaysDirty(t *testing.T) {
	_, m := openUnitSyncSQLite(t)
	ctx := context.Background()
	for _, status := range []string{PushStatusPendingReview, PushStatusRefused} {
		t.Run(status, func(t *testing.T) {
			srv := &scopedPullServer{answer: func(string, int) (int, any) { return 200, pullBody(nil, "x") }}
			srv.push = func(req map[string]any) (int, any) {
				id, _ := firstNode(req)["id"].(string)
				return 200, map[string]any{"accepted_nodes": 0, "conflicts": []any{},
					"results": []any{map[string]any{"id": id, "status": status, "reason": "team_not_found"}}}
			}
			s := unitScopedSyncer(t, m, srv, testMember, "")
			u, err := m.Create(ctx, units.Unit{Kind: units.KindDoc, Scope: units.ScopeGlobal, Classification: units.ClassTeam,
				LoadPolicy: units.LoadOnDemand, Title: "mine " + status, Body: "b"})
			if err != nil {
				t.Fatal(err)
			}
			if _, err := s.PushDirty(ctx); err != nil {
				t.Fatal(err)
			}
			if _, err := m.GetSyncState(ctx, u.ID); !errors.Is(err, units.ErrSyncStateNotFound) {
				t.Fatalf("a %s unit got a sidecar (would read as synced): %v", status, err)
			}
		})
	}
}
