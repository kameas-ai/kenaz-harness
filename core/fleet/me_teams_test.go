package fleet

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
)

// Spec 018 /me teams[] as Fleet serves it from 2026-11-14: root first,
// then the caller's other teams. Path excludes the root.
const meTeamsTreeBody = `{
  "user_id": "u1",
  "org_id": "o1",
  "teams": [
    {"id": "t-root", "name": "Everyone", "is_default": true, "role": "member", "parent_team_id": null, "depth": 0, "path": []},
    {"id": "t-eng", "name": "Engineering", "is_default": false, "role": "lead", "parent_team_id": null, "depth": 1, "path": []},
    {"id": "t-design", "name": "Design", "is_default": false, "role": "member", "parent_team_id": null, "depth": 1, "path": []},
    {"id": "t-backend", "name": "Backend", "is_default": false, "role": "member", "parent_team_id": "t-eng", "depth": 2, "path": ["t-eng"]},
    {"id": "t-db", "name": "Databases", "is_default": false, "role": "member", "parent_team_id": "t-backend", "depth": 3, "path": ["t-eng", "t-backend"]}
  ]
}`

// The pre-018 shape: {id, name} only, default first.
const meTeamsFlatBody = `{"user_id": "u1", "teams": [{"id": "t-root", "name": "Everyone"}, {"id": "t-eng", "name": "Engineering"}]}`

func decodeTeams(t *testing.T, body string) []MeTeam {
	t.Helper()
	teams, err := DecodeMeTeams(strings.NewReader(body))
	if err != nil {
		t.Fatalf("DecodeMeTeams: %v", err)
	}
	return teams
}

type treeRow struct {
	id     string
	depth  int
	parent string
}

func rows(entries []TeamTreeEntry) []treeRow {
	out := make([]treeRow, len(entries))
	for i, e := range entries {
		out[i] = treeRow{e.ID, e.TreeDepth, e.TreeParentID}
	}
	return out
}

func assertRows(t *testing.T, got []TeamTreeEntry, want []treeRow) {
	t.Helper()
	g := rows(got)
	if len(g) != len(want) {
		t.Fatalf("got %d rows %+v, want %d %+v", len(g), g, len(want), want)
	}
	for i := range want {
		if g[i] != want[i] {
			t.Fatalf("row %d = %+v, want %+v (all: %+v)", i, g[i], want[i], g)
		}
	}
}

func strp(s string) *string { return &s }

func TestDecodeMeTeams_NewShape(t *testing.T) {
	teams := decodeTeams(t, meTeamsTreeBody)
	if len(teams) != 5 {
		t.Fatalf("len = %d, want 5", len(teams))
	}
	root := teams[0]
	if !root.IsDefault || root.ParentTeamID != nil || root.Depth == nil || *root.Depth != 0 || len(root.Path) != 0 {
		t.Fatalf("root decoded wrong: %+v", root)
	}
	db := teams[4]
	if db.ParentTeamID == nil || *db.ParentTeamID != "t-backend" || db.Depth == nil || *db.Depth != 3 {
		t.Fatalf("db decoded wrong: %+v", db)
	}
	if strings.Join(db.Path, ",") != "t-eng,t-backend" {
		t.Fatalf("db path = %v", db.Path)
	}
	if teams[1].Role != "lead" {
		t.Fatalf("eng role = %q", teams[1].Role)
	}
}

func TestDecodeMeTeams_OldShape(t *testing.T) {
	teams := decodeTeams(t, meTeamsFlatBody)
	if len(teams) != 2 || teams[0].ID != "t-root" || teams[0].Name != "Everyone" {
		t.Fatalf("teams = %+v", teams)
	}
	for _, tm := range teams {
		if tm.IsDefault || tm.ParentTeamID != nil || tm.Depth != nil || tm.Path != nil || tm.Role != "" {
			t.Fatalf("old shape grew tree fields: %+v", tm)
		}
	}
	// A flat list: served order, nothing indented.
	assertRows(t, OrderTeamTree(teams), []treeRow{{"t-root", 0, ""}, {"t-eng", 0, ""}})
}

func TestDecodeMeTeams_NoTeamsAndFutureFields(t *testing.T) {
	if got := decodeTeams(t, `{"user_id":"u1"}`); got == nil || len(got) != 0 {
		t.Fatalf("absent teams = %#v, want empty non-nil", got)
	}
	if got := decodeTeams(t, `{"teams":null}`); got == nil || len(got) != 0 {
		t.Fatalf("null teams = %#v, want empty non-nil", got)
	}
	// slug and membership are announced, not served yet; tolerate them and
	// any field Fleet adds later.
	got := decodeTeams(t, `{"teams":[{"id":"t1","name":"A","slug":"a","membership":"inherited","child_count":2}]}`)
	if got[0].Slug != "a" || got[0].Membership != "inherited" {
		t.Fatalf("future fields = %+v", got[0])
	}
	if _, err := DecodeMeTeams(strings.NewReader(`{"teams":`)); err == nil {
		t.Fatal("truncated body decoded without error")
	}
}

func TestOrderTeamTree_DefaultRootAndNesting(t *testing.T) {
	// Serve children before parents to prove ordering is by the tree, not
	// by input position.
	teams := decodeTeams(t, meTeamsTreeBody)
	shuffled := []MeTeam{teams[4], teams[3], teams[2], teams[0], teams[1]}
	assertRows(t, OrderTeamTree(shuffled), []treeRow{
		{"t-root", 0, ""},
		{"t-design", 1, "t-root"},
		{"t-eng", 1, "t-root"},
		{"t-backend", 2, "t-eng"},
		{"t-db", 3, "t-backend"},
	})
	got := OrderTeamTree(teams)
	assertRows(t, got, []treeRow{
		{"t-root", 0, ""},
		{"t-eng", 1, "t-root"},
		{"t-backend", 2, "t-eng"},
		{"t-db", 3, "t-backend"},
		{"t-design", 1, "t-root"},
	})
	if !got[0].IsDefault || got[0].Name != "Everyone" {
		t.Fatalf("root entry lost its fields: %+v", got[0])
	}
}

func TestOrderTeamTree_DefaultNeverAChildOrParent(t *testing.T) {
	// A stray parent on the root, and a team naming the root as parent, are
	// both drawn at the root's level rules: root at 0, the team under it.
	teams := []MeTeam{
		{ID: "t-a", Name: "A", ParentTeamID: strp("t-root")},
		{ID: "t-root", Name: "Everyone", IsDefault: true, ParentTeamID: strp("t-a")},
	}
	assertRows(t, OrderTeamTree(teams), []treeRow{{"t-root", 0, ""}, {"t-a", 1, "t-root"}})
}

func TestOrderTeamTree_OrphanParentNotInList(t *testing.T) {
	// The caller is in Backend but not Engineering: Backend's parent is not
	// in the list, so it is drawn directly under the root.
	teams := []MeTeam{
		{ID: "t-root", Name: "Everyone", IsDefault: true},
		{ID: "t-backend", Name: "Backend", ParentTeamID: strp("t-eng"), Path: []string{"t-eng"}},
		{ID: "t-db", Name: "Databases", ParentTeamID: strp("t-backend")},
	}
	assertRows(t, OrderTeamTree(teams), []treeRow{
		{"t-root", 0, ""},
		{"t-backend", 1, "t-root"},
		{"t-db", 2, "t-backend"},
	})
	// No root in the list at all: top level is 0.
	assertRows(t, OrderTeamTree(teams[1:]), []treeRow{{"t-backend", 0, ""}, {"t-db", 1, "t-backend"}})
}

func TestOrderTeamTree_CyclesDefended(t *testing.T) {
	cases := []struct {
		name  string
		teams []MeTeam
		want  []treeRow
	}{
		{
			name:  "self parent",
			teams: []MeTeam{{ID: "t-a", ParentTeamID: strp("t-a")}},
			want:  []treeRow{{"t-a", 0, ""}},
		},
		{
			name: "two-cycle under a root",
			teams: []MeTeam{
				{ID: "t-root", IsDefault: true},
				{ID: "t-a", ParentTeamID: strp("t-b")},
				{ID: "t-b", ParentTeamID: strp("t-a")},
			},
			want: []treeRow{{"t-root", 0, ""}, {"t-a", 1, "t-root"}, {"t-b", 2, "t-a"}},
		},
		{
			name: "three-cycle with a tail hanging off it",
			teams: []MeTeam{
				{ID: "t-tail", ParentTeamID: strp("t-c")},
				{ID: "t-a", ParentTeamID: strp("t-c")},
				{ID: "t-b", ParentTeamID: strp("t-a")},
				{ID: "t-c", ParentTeamID: strp("t-b")},
			},
			// Cut at the first cycle member in served order: t-a.
			want: []treeRow{{"t-a", 0, ""}, {"t-b", 1, "t-a"}, {"t-c", 2, "t-b"}, {"t-tail", 3, "t-c"}},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := OrderTeamTree(tc.teams)
			assertRows(t, got, tc.want)
		})
	}
}

func TestOrderTeamTree_KeyedByIDNotName(t *testing.T) {
	// Two teams share a display name; the tree tells them apart by id, and a
	// repeated id (not a repeated name) is what gets deduplicated.
	teams := []MeTeam{
		{ID: "t-root", Name: "Everyone", IsDefault: true},
		{ID: "t-1", Name: "Platform"},
		{ID: "t-2", Name: "Platform", ParentTeamID: strp("t-1")},
		{ID: "t-1", Name: "Renamed duplicate"},
		{ID: "", Name: "No id"},
	}
	got := OrderTeamTree(teams)
	assertRows(t, got, []treeRow{{"t-root", 0, ""}, {"t-1", 1, "t-root"}, {"t-2", 2, "t-1"}})
	if got[1].Name != "Platform" {
		t.Fatalf("first occurrence not kept: %+v", got[1])
	}
}

func TestOrderTeamTree_Empty(t *testing.T) {
	if got := OrderTeamTree(nil); len(got) != 0 {
		t.Fatalf("got %+v", got)
	}
}

// meTeamsServer serves /api/v1/me and records the paths it saw. Race-safe:
// the handler runs on the server's goroutines, the test reads via snapshot.
type meTeamsServer struct {
	mu     sync.Mutex
	paths  []string
	status int
	body   string
}

func (s *meTeamsServer) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	s.mu.Lock()
	s.paths = append(s.paths, r.URL.Path)
	status, body := s.status, s.body
	s.mu.Unlock()
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_, _ = w.Write([]byte(body))
}

func (s *meTeamsServer) snapshot() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]string(nil), s.paths...)
}

func TestGetMeTeams_HTTP(t *testing.T) {
	// Not parallel: uses the keychain singleton via makeOnboardingClient.
	fake := &meTeamsServer{status: http.StatusOK, body: meTeamsTreeBody}
	srv := httptest.NewServer(fake)
	defer srv.Close()
	c := makeOnboardingClient(t, srv.URL)

	teams, err := c.GetMeTeams(context.Background())
	if err != nil {
		t.Fatalf("GetMeTeams: %v", err)
	}
	if len(teams) != 5 || !teams[0].IsDefault {
		t.Fatalf("teams = %+v", teams)
	}
	if p := fake.snapshot(); len(p) != 1 || p[0] != "/api/v1/me" {
		t.Fatalf("paths = %v", p)
	}

	fake.mu.Lock()
	fake.status, fake.body = http.StatusUnauthorized, `{}`
	fake.mu.Unlock()
	// The client refreshes on 401; with no token server the refresh fails,
	// and that surfaces as an error, never as an empty team list.
	if teams, err := c.GetMeTeams(context.Background()); err == nil {
		t.Fatalf("401 returned teams %+v and no error", teams)
	}

	fake.mu.Lock()
	fake.status, fake.body = http.StatusInternalServerError, `{"error":"x"}`
	fake.mu.Unlock()
	if _, err := c.GetMeTeams(context.Background()); err == nil {
		t.Fatal("500 returned no error")
	}
}

func TestGetMeTeams_NopClient(t *testing.T) {
	var c *Client
	if _, err := c.GetMeTeams(context.Background()); !errors.Is(err, ErrFleetDisabled) {
		t.Fatalf("nil client err = %v", err)
	}
	if _, err := (&Client{isNop: true}).GetMeTeams(context.Background()); !errors.Is(err, ErrFleetDisabled) {
		t.Fatalf("nop client err = %v", err)
	}
}

func TestOrderTeamTree_OrphanAttachesToClosestListedAncestor(t *testing.T) {
	// Engineering > Backend > Databases > Postgres. The caller is in
	// Engineering and Postgres only: Postgres's parent (Databases) and
	// grandparent (Backend) are not listed, so it attaches to Engineering,
	// the closest listed ancestor on its path.
	teams := []MeTeam{
		{ID: "t-root", Name: "Everyone", IsDefault: true},
		{ID: "t-pg", Name: "Postgres", ParentTeamID: strp("t-db"), Path: []string{"t-eng", "t-backend", "t-db"}},
		{ID: "t-eng", Name: "Engineering"},
		// No listed ancestor on the path at all: directly under the root.
		{ID: "t-ops", Name: "Ops", ParentTeamID: strp("t-x"), Path: []string{"t-y", "t-x"}},
		// A path naming the team itself or the root is ignored.
		{ID: "t-odd", Name: "Odd", ParentTeamID: strp("t-z"), Path: []string{"t-root", "t-odd"}},
	}
	assertRows(t, OrderTeamTree(teams), []treeRow{
		{"t-root", 0, ""},
		{"t-eng", 1, "t-root"},
		{"t-pg", 2, "t-eng"},
		{"t-ops", 1, "t-root"},
		{"t-odd", 1, "t-root"},
	})
}

func TestOrderTeamTree_OnlyRootIsDefault(t *testing.T) {
	teams := []MeTeam{
		{ID: "t-root", Name: "Everyone", IsDefault: true},
		{ID: "t-dup", Name: "Also default", IsDefault: true},
	}
	got := OrderTeamTree(teams)
	if !got[0].IsDefault || got[1].IsDefault {
		t.Fatalf("IsDefault = %v, %v; want only the root", got[0].IsDefault, got[1].IsDefault)
	}
}
