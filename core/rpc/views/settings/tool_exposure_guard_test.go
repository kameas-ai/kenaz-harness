package settings

import (
	"context"
	"errors"
	"testing"

	"github.com/kameas-ai/kenaz-harness/core/toolexposure"
)

// refusingGuard refuses every layer write and counts the checks.
type refusingGuard struct{ calls int }

var errGuardRefused = errors.New("refused by guard")

func (g *refusingGuard) CheckLayerWrite(context.Context, toolexposure.LayerWrite) error {
	g.calls++
	return errGuardRefused
}

// The whole-settings save (Settings_Set -> SaveAll) is a tool-exposure
// write too: a changed exposure layer passes the FR-E3 guard, while an
// unrelated save carrying the stored layer unchanged does not consult
// it, so it is never refused over a layer the user did not touch.
func TestToolExposure_SetConsultsGuardOnlyWhenExposureChanges(t *testing.T) {
	ctx := context.Background()
	store, err := NewFileStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	off := toolexposure.Exposure{Servers: map[string]toolexposure.ServerExposure{
		toolexposure.BuiltinServer: {Tools: map[string]toolexposure.Tier{"load_tools": toolexposure.TierOff}},
	}}
	// A layer already on disk (hand edit, older build), written past the
	// guard.
	if err := store.SaveToolExposure(toolexposure.Settings{Exposure: off}); err != nil {
		t.Fatal(err)
	}
	api := NewAPI(store)
	g := &refusingGuard{}
	api.SetToolExposureGuard(g)

	cur, err := store.LoadAll()
	if err != nil {
		t.Fatal(err)
	}
	cur.LastRoute = "/settings"
	if err := api.Set(ctx, cur); err != nil || g.calls != 0 {
		t.Fatalf("unrelated save: err %v, guard calls %d; want saved without a check", err, g.calls)
	}

	changed := off.Clone()
	changed.Servers["outlook"] = toolexposure.ServerExposure{Tier: toolexposure.TierSummary}
	cur.ToolExposure = &changed
	if err := api.Set(ctx, cur); !errors.Is(err, errGuardRefused) || g.calls != 1 {
		t.Fatalf("exposure-changing save: err %v, guard calls %d; want refused after one check", err, g.calls)
	}
	stored, _ := store.LoadToolExposure()
	if _, ok := stored.Exposure.Servers["outlook"]; ok {
		t.Fatal("refused save reached the file")
	}
}
