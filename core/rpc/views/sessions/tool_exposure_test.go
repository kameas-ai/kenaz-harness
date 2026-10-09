package sessions

// Sessions_{Get,Set}ToolExposure through the view against REAL sqlite
// (tool-context-budget-01TCBUD01 WP02, WP-PI): the override survives a
// genuine close-and-reopen, activations come back as an empty array
// rather than null, and an unknown tier is refused without a write.

import (
	"context"
	"reflect"
	"testing"

	"github.com/kameas-ai/kenaz-harness/core/toolexposure"
)

func TestToolExposure_SurvivesReload(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	dir := t.TempDir()

	api1, mgr1, close1 := openRealSessionAPI(t, dir)
	rec, err := api1.Create(ctx, "exposure")
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	fresh, err := api1.GetToolExposure(ctx, rec.ID)
	if err != nil {
		t.Fatalf("GetToolExposure on a fresh session: %v", err)
	}
	if !fresh.Exposure.IsZero() || fresh.Activations == nil || len(fresh.Activations) != 0 {
		t.Fatalf("fresh session = %+v, want zero override and an empty (non-nil) activation list", fresh)
	}

	layer := toolexposure.Exposure{Servers: map[string]toolexposure.ServerExposure{
		"outlook": {Tier: toolexposure.TierFull, Tools: map[string]toolexposure.Tier{"delete-mail": toolexposure.TierOff}},
	}}
	if err := api1.SetToolExposure(ctx, rec.ID, layer); err != nil {
		t.Fatalf("SetToolExposure: %v", err)
	}
	acts := []toolexposure.Activation{{Name: "fetch__fetch", Server: "fetch", LastUsedTurn: 1}}
	if err := mgr1.SetToolActivations(ctx, rec.ID, acts); err != nil {
		t.Fatalf("SetToolActivations: %v", err)
	}
	bad := toolexposure.Exposure{Servers: map[string]toolexposure.ServerExposure{"outlook": {Tier: "loud"}}}
	if err := api1.SetToolExposure(ctx, rec.ID, bad); err == nil {
		t.Fatal("unknown tier accepted")
	}
	close1()

	api2, _, close2 := openRealSessionAPI(t, dir)
	defer close2()
	got, err := api2.GetToolExposure(ctx, rec.ID)
	if err != nil {
		t.Fatalf("GetToolExposure after reload: %v", err)
	}
	if !reflect.DeepEqual(got.Exposure, layer) || !reflect.DeepEqual(got.Activations, acts) {
		t.Fatalf("after reload = %+v, want override %+v and activations %+v", got, layer, acts)
	}
	if _, err := api2.GetToolExposure(ctx, "no-such-session"); err == nil {
		t.Fatal("GetToolExposure on a missing session succeeded")
	}
}
