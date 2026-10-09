package loadtools

import (
	"context"
	"testing"

	"github.com/kameas-ai/kenaz-harness/core/toolexposure"
)

type fakePins struct{ e toolexposure.Exposure }

func (f fakePins) ToolExposurePins(context.Context) (toolexposure.Exposure, error) { return f.e, nil }

func costsFixture(t *testing.T, user toolexposure.Exposure, projects fakeProjects, sess *fakeSessions, pins toolexposure.PinSource) *Service {
	t.Helper()
	entries := catalogFixture()
	for i := range entries {
		entries[i].TokenEst = 100 + i
	}
	resolver, err := toolexposure.NewResolver(toolexposure.Deps{
		Settings: fakeSettings{s: toolexposure.Settings{Exposure: user}},
		Sessions: sess,
		Projects: projects,
		Pins:     pins,
	})
	if err != nil {
		t.Fatal(err)
	}
	svc, err := NewService(Deps{
		Catalog:     fakeCatalog{entries: entries},
		Resolver:    resolver,
		Servers:     fakeServers{list: serversFixture()},
		Activations: sess,
	})
	if err != nil {
		t.Fatal(err)
	}
	return svc
}

func costFor(t *testing.T, costs []ServerCost, server string) ServerCost {
	t.Helper()
	for _, c := range costs {
		if c.Server == server {
			return c
		}
	}
	t.Fatalf("no cost row for %q in %+v", server, costs)
	return ServerCost{}
}

// TestSchemaCosts_UserScope covers the Capabilities rows with no project
// selected: per-server token sums, tool counts, the resolved tier and its
// layer, a mixed server, and a stopped server reported with no tools.
func TestSchemaCosts_UserScope(t *testing.T) {
	svc := costsFixture(t, userOffSecretAndMixedDrop(), fakeProjects{}, &fakeSessions{}, nil)
	costs, err := svc.SchemaCosts(context.Background(), "", "")
	if err != nil {
		t.Fatal(err)
	}
	outlook := costFor(t, costs, "outlook")
	// catalogFixture indexes 3..8 are outlook: 103+104+…+108.
	if outlook.ToolCount != 6 || outlook.TokenEst != 633 {
		t.Errorf("outlook count/tokens = %d/%d, want 6/633", outlook.ToolCount, outlook.TokenEst)
	}
	if outlook.Tier != "summary" || outlook.Source != toolexposure.LevelDefault || outlook.SendableTokenEst != 0 {
		t.Errorf("outlook = %+v, want summary from default with nothing sendable", outlook)
	}
	if got := costFor(t, costs, "secret"); got.Tier != "off" || got.Source != toolexposure.LevelUser {
		t.Errorf("secret = %+v, want off from user", got)
	}
	if got := costFor(t, costs, "mixed"); got.Tier != TierMixed || got.Source != "" {
		t.Errorf("mixed = %+v, want mixed tier with no single source", got)
	}
	gh := costFor(t, costs, "github")
	if gh.Running || gh.ToolCount != 0 || gh.State != "failed" || len(gh.Tools) != 0 {
		t.Errorf("github = %+v, want a stopped server with no listed tools", gh)
	}
	kenaz := costFor(t, costs, toolexposure.BuiltinServer)
	if kenaz.Tier != TierMixed {
		t.Errorf("kenaz tier = %q, want mixed (hot set full, monitor summary)", kenaz.Tier)
	}
	for _, tc := range kenaz.Tools {
		if tc.Name == "read_file" && (!tc.Sendable || tc.Tier != toolexposure.TierFull) {
			t.Errorf("kenaz read_file = %+v, want full and sendable", tc)
		}
	}
}

// TestSchemaCosts_ProjectScopeReadsTheProjectLayer: the project's stored
// layer decides the tier and makes the whole server sendable.
func TestSchemaCosts_ProjectScopeReadsTheProjectLayer(t *testing.T) {
	projects := fakeProjects{"p1": {Servers: map[string]toolexposure.ServerExposure{
		"outlook": {Tier: toolexposure.TierFull},
	}}}
	svc := costsFixture(t, toolexposure.Exposure{}, projects, &fakeSessions{}, nil)
	costs, err := svc.SchemaCosts(context.Background(), "", "p1")
	if err != nil {
		t.Fatal(err)
	}
	outlook := costFor(t, costs, "outlook")
	if outlook.Tier != "full" || outlook.Source != toolexposure.LevelProject || outlook.SendableTokenEst != outlook.TokenEst {
		t.Errorf("outlook in p1 = %+v, want full from project, all sendable", outlook)
	}
	user, err := svc.SchemaCosts(context.Background(), "", "")
	if err != nil {
		t.Fatal(err)
	}
	if got := costFor(t, user, "outlook"); got.Tier != "summary" {
		t.Errorf("outlook with no project = %q, want summary (project layer must not leak)", got.Tier)
	}
}

// TestSchemaCosts_SessionScopeShowsActivations: a session's activated
// tool is reported activated and counted as sendable; the rest of the
// server is not.
func TestSchemaCosts_SessionScopeShowsActivations(t *testing.T) {
	sess := &fakeSessions{acts: []toolexposure.Activation{{Name: "outlook__send-mail", Server: "outlook", LastUsedTurn: 1, Sticky: true}}}
	svc := costsFixture(t, toolexposure.Exposure{}, fakeProjects{}, sess, nil)
	costs, err := svc.SchemaCosts(context.Background(), "s1", "")
	if err != nil {
		t.Fatal(err)
	}
	outlook := costFor(t, costs, "outlook")
	// outlook__send-mail is catalogFixture index 3 → 103 tokens.
	if outlook.SendableTokenEst != 103 {
		t.Errorf("outlook sendable = %d, want 103 (send-mail only)", outlook.SendableTokenEst)
	}
	for _, tc := range outlook.Tools {
		want := tc.Name == "send-mail"
		if tc.Activated != want || tc.Sendable != want {
			t.Errorf("outlook %s activated/sendable = %v/%v, want %v", tc.Name, tc.Activated, tc.Sendable, want)
		}
	}
}

// TestSchemaCosts_OrgPinMarksPinned: a server an organisation pin
// decides is reported pinned, so the UI renders it read-only.
func TestSchemaCosts_OrgPinMarksPinned(t *testing.T) {
	pins := fakePins{e: toolexposure.Exposure{Servers: map[string]toolexposure.ServerExposure{
		"fetch": {Tier: toolexposure.TierFull},
	}}}
	svc := costsFixture(t, toolexposure.Exposure{}, fakeProjects{}, &fakeSessions{}, pins)
	costs, err := svc.SchemaCosts(context.Background(), "", "")
	if err != nil {
		t.Fatal(err)
	}
	if got := costFor(t, costs, "fetch"); !got.Pinned || got.Source != toolexposure.LevelOrgPin {
		t.Errorf("fetch = %+v, want pinned by org", got)
	}
	if got := costFor(t, costs, "outlook"); got.Pinned {
		t.Errorf("outlook = %+v, want not pinned", got)
	}
}
