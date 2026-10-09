package rpc

// Org tool-exposure pins through the production wiring
// (tool-context-budget-01TCBUD01 WP07): New() loads the applied policy
// from <dataDir>/fleet at boot, hands it to the resolver (Deps.Pins) and
// to the read surfaces, and every writer refuses a pinned entry.

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/kameas-ai/kenaz-harness/core"
	"github.com/kameas-ai/kenaz-harness/core/toolexposure"
)

func TestToolExposureWiring_OrgPinsThroughNew(t *testing.T) {
	sandboxUserConfigDir(t)
	t.Setenv("HARNESS_FLEET_DISABLED", "")
	dataDir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dataDir, "fleet"), 0o700); err != nil {
		t.Fatal(err)
	}
	state := `{"bundle_id":3,"tool_exposure":{"servers":{"kenaz":{"pinned":false,"tools":{"sleep":{"tier":"off","pinned":true}}}},"budget_tokens":7000}}`
	if err := os.WriteFile(filepath.Join(dataDir, "fleet", "tool_exposure_applied.json"), []byte(state), 0o600); err != nil {
		t.Fatal(err)
	}
	c, err := core.New(core.Options{DataDir: dataDir})
	if err != nil {
		t.Fatalf("core.New: %v", err)
	}
	api := New(c)
	assertSettingsStoreIsSandboxed(t, api)
	t.Cleanup(func() {
		api.Shutdown()
		_ = c.Shutdown(context.Background())
	})
	ctx := context.Background()

	rec, err := c.SessionManager().Create(ctx, "zz-org")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := api.Sessions().LoadTools(ctx, rec.ID, nil, []string{"kenaz__sleep"}, true); !errors.Is(err, toolexposure.ErrPinnedByOrg) {
		t.Fatalf("Sessions LoadTools(org-off tool) err = %v, want ErrPinnedByOrg", err)
	}

	on := toolexposure.Exposure{Servers: map[string]toolexposure.ServerExposure{
		toolexposure.BuiltinServer: {Tools: map[string]toolexposure.Tier{"sleep": toolexposure.TierFull}},
	}}
	if err := api.Settings().SetToolExposure(ctx, toolexposure.Settings{Exposure: on}); !errors.Is(err, toolexposure.ErrPinnedByOrg) {
		t.Errorf("Settings SetToolExposure(pinned) err = %v, want ErrPinnedByOrg", err)
	}
	if err := api.Settings().SetToolExposure(ctx, toolexposure.Settings{SchemaBudgetTokens: 9000}); !errors.Is(err, toolexposure.ErrPinnedByOrg) {
		t.Errorf("Settings SetToolExposure(budget) err = %v, want ErrPinnedByOrg", err)
	}
	if err := api.Sessions().SetToolExposure(ctx, rec.ID, on); !errors.Is(err, toolexposure.ErrPinnedByOrg) {
		t.Errorf("Sessions SetToolExposure(pinned) err = %v, want ErrPinnedByOrg", err)
	}
	proj, err := c.ProjectManager().Create(ctx, "zz-org-proj", "")
	if err != nil {
		t.Fatal(err)
	}
	if err := api.Projects().SetToolExposure(ctx, proj.ID, on); !errors.Is(err, toolexposure.ErrPinnedByOrg) {
		t.Errorf("Projects SetToolExposure(pinned) err = %v, want ErrPinnedByOrg", err)
	}

	want := toolexposure.OrgSetting{Server: toolexposure.BuiltinServer, Tool: "sleep", Tier: toolexposure.TierOff, Pinned: true, PinnedBy: toolexposure.PinnedByOrg}
	st, err := api.Sessions().GetToolExposure(ctx, rec.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(st.Org.Settings) != 1 || st.Org.Settings[0] != want || st.Org.BundleID != 3 {
		t.Errorf("session Org = %+v, want %+v from bundle 3", st.Org, want)
	}
	ts, err := api.Settings().GetToolExposure(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(ts.Org.Settings) != 1 || ts.Org.Settings[0] != want || ts.EffectiveSchemaBudgetTokens != 7000 {
		t.Errorf("settings Org = %+v effective budget %d, want %+v and 7000", ts.Org, ts.EffectiveSchemaBudgetTokens, want)
	}
}
