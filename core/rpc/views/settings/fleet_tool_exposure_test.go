package settings

// Org tool-exposure pins (tool-context-budget-01TCBUD01 WP07): the
// bundle's tool_exposure section reaches the settings surface through the
// real compositeConfigApplier, pinned entries refuse the user's writes,
// and the policy drops away when the next bundle no longer carries it.

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/kameas-ai/kenaz-harness/core/fleet"
	"github.com/kameas-ai/kenaz-harness/core/toolexposure"
)

func newPinnedSettingsAPI(t *testing.T) (*API, *compositeConfigApplier, string) {
	t.Helper()
	store, err := NewFileStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	dataDir := t.TempDir()
	api := NewAPI(store)
	api.fleet = newFleetState()
	api.fleet.dataDir = dataDir
	api.fleet.toolExposurePins = fleet.LoadToolExposurePins(dataDir)
	return api, &compositeConfigApplier{state: api.fleet}, dataDir
}

func pinnedBundle(id int64) *fleet.Bundle {
	return &fleet.Bundle{BundleID: id, ToolExposure: &fleet.BundleToolExposure{
		Servers: map[string]fleet.BundleToolExposureServer{
			"outlook": {Tier: "off", Pinned: true},
			"fetch":   {Tier: "full", Pinned: false},
		},
		BudgetTokens: 5000,
	}}
}

func TestToolExposure_OrgPinsThroughApplierAndWriters(t *testing.T) {
	ctx := context.Background()
	api, applier, dataDir := newPinnedSettingsAPI(t)
	if err := api.SetToolExposure(ctx, toolexposure.Settings{
		Exposure:           toolexposure.Exposure{Servers: map[string]toolexposure.ServerExposure{"outlook": {Tier: toolexposure.TierFull}}},
		SchemaBudgetTokens: 9000,
	}); err != nil {
		t.Fatalf("pre-pin write: %v", err)
	}

	if errs, _ := applier.ApplyBundleItems(ctx, pinnedBundle(1)); len(errs) != 0 {
		t.Fatalf("apply: %v", errs)
	}
	got, err := api.GetToolExposure(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if got.EffectiveSchemaBudgetTokens != 5000 || got.SchemaBudgetTokens != 9000 || got.Org.SchemaBudgetTokens != 5000 || got.Org.BundleID != 1 {
		t.Fatalf("budget: stored %d effective %d org %d (bundle %d), want 9000 / 5000 / 5000 (1)",
			got.SchemaBudgetTokens, got.EffectiveSchemaBudgetTokens, got.Org.SchemaBudgetTokens, got.Org.BundleID)
	}
	wantOrg := []toolexposure.OrgSetting{
		{Server: "fetch", Tier: toolexposure.TierFull},
		{Server: "outlook", Tier: toolexposure.TierOff, Pinned: true, PinnedBy: toolexposure.PinnedByOrg},
	}
	if len(got.Org.Settings) != 2 || got.Org.Settings[0] != wantOrg[0] || got.Org.Settings[1] != wantOrg[1] {
		t.Fatalf("Org.Settings = %+v, want %+v", got.Org.Settings, wantOrg)
	}

	// A no-edit write-back is allowed: nothing pinned changes.
	if err := api.SetToolExposure(ctx, got); err != nil {
		t.Fatalf("no-edit write-back refused: %v", err)
	}
	// Changing the pinned server or the pinned budget is refused.
	edit := got
	edit.Exposure = toolexposure.Exposure{Servers: map[string]toolexposure.ServerExposure{"outlook": {Tier: toolexposure.TierSummary}}}
	if err := api.SetToolExposure(ctx, edit); !errors.Is(err, toolexposure.ErrPinnedByOrg) || !strings.Contains(err.Error(), "server outlook") {
		t.Fatalf("pinned server change: err = %v", err)
	}
	edit = got
	edit.SchemaBudgetTokens = 12000
	if err := api.SetToolExposure(ctx, edit); !errors.Is(err, toolexposure.ErrPinnedByOrg) || !strings.Contains(err.Error(), "5000 tokens") {
		t.Fatalf("pinned budget change: err = %v", err)
	}
	// Overriding an org default is the user's call.
	edit = got
	edit.Exposure = toolexposure.Exposure{Servers: map[string]toolexposure.ServerExposure{
		"outlook": {Tier: toolexposure.TierFull},
		"fetch":   {Tier: toolexposure.TierOff},
	}}
	if err := api.SetToolExposure(ctx, edit); err != nil {
		t.Fatalf("org default override refused: %v", err)
	}

	// The next verified bundle no longer carries the field: everything is
	// the user's again, and the state file is gone so a restart agrees.
	if errs, _ := applier.ApplyBundleItems(ctx, &fleet.Bundle{BundleID: 2}); len(errs) != 0 {
		t.Fatalf("apply without the field: %v", errs)
	}
	if p := fleet.LoadToolExposurePins(dataDir).Policy(); !p.IsZero() {
		t.Fatalf("restart after the field disappeared still pins: %+v", p)
	}
	got, _ = api.GetToolExposure(ctx)
	if len(got.Org.Settings) != 0 || got.EffectiveSchemaBudgetTokens != 9000 {
		t.Fatalf("after disappearance: org %+v effective budget %d, want none / the user's 9000", got.Org, got.EffectiveSchemaBudgetTokens)
	}
	edit = got
	edit.Exposure = toolexposure.Exposure{Servers: map[string]toolexposure.ServerExposure{"outlook": {Tier: toolexposure.TierSummary}}}
	edit.SchemaBudgetTokens = 12000
	if err := api.SetToolExposure(ctx, edit); err != nil {
		t.Fatalf("write after the pin lifted: %v", err)
	}
}

// A refused entry is an apply error for the ACK; the rest of the section
// still applies.
func TestToolExposure_RefusedEntryIsApplyError(t *testing.T) {
	ctx := context.Background()
	api, applier, _ := newPinnedSettingsAPI(t)
	b := pinnedBundle(1)
	b.ToolExposure.Servers["github"] = fleet.BundleToolExposureServer{Tier: "readonly", Pinned: true}
	errs, _ := applier.ApplyBundleItems(ctx, b)
	if len(errs) != 1 || !errors.Is(errs[0], fleet.ErrToolExposureEntryRefused) || !strings.Contains(errs[0].Error(), `"github"`) {
		t.Fatalf("errs = %v, want one refusal naming github", errs)
	}
	p, _ := api.ToolExposurePolicy(ctx)
	if p.Pins.TierFor("outlook", "x") != toolexposure.TierOff {
		t.Fatalf("the valid entries did not apply: %+v", p)
	}
}

// Without a store (fleet disabled) a bundle carrying the section is a
// named apply error, never a silent drop; without the section it is not.
func TestToolExposure_NoStoreWired(t *testing.T) {
	applier := &compositeConfigApplier{state: &fleetState{dataDir: t.TempDir()}}
	if errs, _ := applier.ApplyBundleItems(context.Background(), pinnedBundle(1)); len(errs) != 1 || !strings.Contains(errs[0].Error(), "no policy store wired") {
		t.Fatalf("errs = %v", errs)
	}
	if errs, _ := applier.ApplyBundleItems(context.Background(), &fleet.Bundle{BundleID: 2}); len(errs) != 0 {
		t.Fatalf("errs without the section = %v", errs)
	}
}

func TestToolExposure_SignOutClearsPins(t *testing.T) {
	ctx := context.Background()
	api, applier, dataDir := newPinnedSettingsAPI(t)
	if errs, _ := applier.ApplyBundleItems(ctx, pinnedBundle(1)); len(errs) != 0 {
		t.Fatal(errs)
	}
	api.clearToolExposurePins("sign_out")
	if p, _ := api.ToolExposurePolicy(ctx); !p.IsZero() {
		t.Fatalf("policy after sign-out = %+v", p)
	}
	if p := fleet.LoadToolExposurePins(dataDir).Policy(); !p.IsZero() {
		t.Fatalf("state file survived sign-out: %+v", p)
	}
}
