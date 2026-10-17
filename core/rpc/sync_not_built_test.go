package rpc

// sync_not_built_test.go — provider_profiles and mcp_recipes have no
// personal sync yet (their user collector is {} and their user applier does
// nothing), so they must not upload, not be offered as "awaiting
// confirmation", and must say they are not syncing. Uses the real kinds
// from sync_categories.go.

import (
	"context"
	"net/http/httptest"
	"testing"
	"time"

	corefleet "github.com/kameas-ai/kenaz-harness/core/fleet"
	"github.com/kameas-ai/kenaz-harness/core/rpc/views/settings"
	syncview "github.com/kameas-ai/kenaz-harness/core/rpc/views/sync"
)

func TestSyncNotBuiltKinds_NeverUploadOrClaimToSync(t *testing.T) {
	if corefleet.Disabled() {
		t.Skip("HARNESS_FLEET_DISABLED=1")
	}
	fake := &consentFakeFleet{}
	srv := httptest.NewServer(fake)
	t.Cleanup(srv.Close)
	corefleet.SeedFleetConfigForTesting(srv.URL, corefleet.FleetConfig{
		Issuer: srv.URL, ClientID: "test", APIBaseURL: srv.URL, FetchedAt: time.Now().UTC(),
	})
	corefleet.SetExternalTokenSource(func() string { return "notbuilt-token" })
	t.Cleanup(func() { corefleet.SetExternalTokenSource(nil) })

	store, err := settings.NewFileStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	syncer := corefleet.NewSyncer(corefleet.NewClientForTesting(srv.URL))
	t.Cleanup(syncer.Stop)
	stubs := map[corefleet.SyncCategory]corefleet.SyncKind{
		corefleet.SyncCategoryProviderProfiles: emptyPayloadKind(corefleet.SyncCategoryProviderProfiles,
			[]corefleet.Scope{corefleet.ScopeUser, corefleet.ScopeOrg}),
		corefleet.SyncCategoryMCPRecipes: mcpRecipesKind(nil),
	}
	for cat, k := range stubs {
		syncer.RegisterCategory(cat, k.CategoryConfig())
	}
	api := syncview.NewAPI(syncer, nil)
	api.SetUploadConsent(settingsUploadConsent{store: store})
	ctx := context.Background()

	for cat := range stubs {
		if err := api.Sync_Toggle(ctx, string(cat), true); err != nil {
			t.Fatalf("Sync_Toggle(%s): %v", cat, err)
		}
	}
	rows, _ := api.Sync_Status(ctx)
	for _, r := range rows {
		if _, ok := stubs[corefleet.SyncCategory(r.Category)]; !ok {
			continue
		}
		if !r.NotSyncingYet || r.AwaitingConfirmation {
			t.Errorf("%s: not_syncing_yet=%v awaiting_confirmation=%v, want true/false",
				r.Category, r.NotSyncingYet, r.AwaitingConfirmation)
		}
	}
	if err := api.Sync_ConfirmUploads(ctx); err != nil {
		t.Fatal(err)
	}
	for cat := range stubs {
		_ = api.Sync_ForcePush(ctx, string(cat))
	}
	if n := fake.putCount(); n != 0 {
		t.Fatalf("%d PUTs of an empty {} payload for kinds that do not sync yet, want 0", n)
	}
}
