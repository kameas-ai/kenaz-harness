package settings

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/kameas-ai/kenaz-harness/core/fleet"
)

// TestFleetTeams_NotWired: no fleet client means no teams and the
// disabled sentinel, never an empty list that reads as "no teams".
func TestFleetTeams_NotWired(t *testing.T) {
	store, err := NewFileStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	a := NewAPI(store)
	if got, err := a.FleetTeams(context.Background()); !errors.Is(err, fleet.ErrFleetDisabled) || got != nil {
		t.Fatalf("FleetTeams() = %v, %v; want nil, ErrFleetDisabled", got, err)
	}
}

// TestFleetTeams_TreeFromMe: the binding reads /api/v1/me and returns the
// teams root first, parent before children, with the drawn indent level
// (kenaz-fleet spec 018 H-1).
func TestFleetTeams_TreeFromMe(t *testing.T) {
	// NOT parallel: SetExternalTokenSource is global.
	if fleet.Disabled() {
		t.Skip("HARNESS_FLEET_DISABLED=1")
	}
	var srv *httptest.Server
	srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.URL.Path == "/config.json" {
			_, _ = w.Write([]byte(`{"api_base_url":"` + srv.URL + `"}`))
			return
		}
		if r.URL.Path != "/api/v1/me" {
			http.NotFound(w, r)
			return
		}
		_, _ = w.Write([]byte(`{"teams":[
			{"id":"t-root","name":"Everyone","is_default":true,"role":"member","parent_team_id":null,"depth":0,"path":[]},
			{"id":"t-backend","name":"Backend","role":"member","parent_team_id":"t-eng","depth":2,"path":["t-eng"]},
			{"id":"t-eng","name":"Engineering","role":"lead","parent_team_id":null,"depth":1,"path":[]},
			{"id":"t-ops","name":"Ops","role":"member","parent_team_id":"t-elsewhere","depth":2,"path":["t-elsewhere"]}
		]}`))
	}))
	t.Cleanup(srv.Close)

	store, err := NewFileStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	dataDir := t.TempDir()
	r := &sessionRig{dataDir: dataDir}
	fleet.SetExternalTokenSource(r.token)
	t.Cleanup(func() { fleet.SetExternalTokenSource(nil) })
	r.setToken(jwtFor("sub-alice", "zitadel-org-1"))
	a := NewAPI(store)
	a.SetFleetClient(fleet.NewClientForTestingWithDataDir(srv.URL, dataDir), dataDir)
	t.Cleanup(a.StopFleetBackground)

	got, err := a.FleetTeams(context.Background())
	if err != nil {
		t.Fatalf("FleetTeams: %v", err)
	}
	want := []FleetTeamView{
		{ID: "t-root", Name: "Everyone", IsDefault: true, Role: "member", TreeDepth: 0},
		{ID: "t-eng", Name: "Engineering", Role: "lead", ParentTeamID: "t-root", TreeDepth: 1},
		{ID: "t-backend", Name: "Backend", Role: "member", ParentTeamID: "t-eng", TreeDepth: 2},
		// Parent not among the caller's teams: drawn directly under the root.
		{ID: "t-ops", Name: "Ops", Role: "member", ParentTeamID: "t-root", TreeDepth: 1},
	}
	if len(got) != len(want) {
		t.Fatalf("got %+v, want %+v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("row %d = %+v, want %+v", i, got[i], want[i])
		}
	}
}
