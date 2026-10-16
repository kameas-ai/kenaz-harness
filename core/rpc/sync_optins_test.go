package rpc

// sync_optins_test.go — per-category sync opt-ins survive a restart, through
// REAL settings persistence (a settings.FileStore on disk, reopened), and a
// restored opt-in is not upload consent (owner ruling H1).
//
// A "restart" here is what a relaunch does: a new FileStore on the same
// directory, a new fleet.Syncer (every category starts off), and the same
// boot glue rpc.New runs (wireSyncOptIns).

import (
	"context"
	"errors"
	"net/http/httptest"
	"testing"
	"time"

	corefleet "github.com/kameas-ai/kenaz-harness/core/fleet"
	"github.com/kameas-ai/kenaz-harness/core/rpc/views/settings"
	syncview "github.com/kameas-ai/kenaz-harness/core/rpc/views/sync"
)

func (f *consentFakeFleet) getCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.gets
}

// optInsFleet starts the strict fake Fleet and points the fleet client at it.
func optInsFleet(t *testing.T) (*consentFakeFleet, string) {
	t.Helper()
	if corefleet.Disabled() {
		t.Skip("HARNESS_FLEET_DISABLED=1")
	}
	fake := &consentFakeFleet{}
	ts := httptest.NewServer(fake)
	t.Cleanup(ts.Close)
	srv := ts.URL
	corefleet.SeedFleetConfigForTesting(srv, corefleet.FleetConfig{
		Issuer: srv, ClientID: "test", APIBaseURL: srv, FetchedAt: time.Now().UTC(),
	})
	corefleet.SetExternalTokenSource(func() string { return "optins-token" })
	t.Cleanup(func() { corefleet.SetExternalTokenSource(nil) })
	return fake, srv
}

// launch opens the settings directory and boots the sync view as rpc.New
// does: categories, upload gate, then wireSyncOptIns.
func launch(t *testing.T, baseURL, dir string) (*syncview.API, *corefleet.Syncer, settings.SettingsStore) {
	t.Helper()
	store, err := settings.NewFileStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	api, syncer := newConsentRig(t, baseURL, store)
	wireSyncOptIns(api, store)
	return api, syncer, store
}

func statusRow(t *testing.T, api *syncview.API, cat corefleet.SyncCategory) syncview.SyncStatusView {
	t.Helper()
	rows, err := api.Sync_Status(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	for _, r := range rows {
		if r.Category == string(cat) {
			return r
		}
	}
	t.Fatalf("no status row for %s", cat)
	return syncview.SyncStatusView{}
}

// TestSyncOptIns_SurviveRestart_StillHeldUntilConfirmed is the regression
// for the in-memory-only enabled flag: before the fix, the restarted syncer
// reports ui_theme off.
func TestSyncOptIns_SurviveRestart_StillHeldUntilConfirmed(t *testing.T) {
	fake, srv := optInsFleet(t)
	dir := t.TempDir()
	ctx := context.Background()
	theme := corefleet.SyncCategoryUITheme

	// Launch 1: the user opts in; uploads are held (not confirmed yet).
	api1, _, _ := launch(t, srv, dir)
	if err := api1.Sync_Toggle(ctx, string(theme), true); err != nil {
		t.Fatalf("Sync_Toggle: %v", err)
	}
	if n := fake.putCount(); n != 0 {
		t.Fatalf("%d PUTs before confirmation, want 0", n)
	}

	// Launch 2: the choice is back, and still held.
	api2, syncer2, _ := launch(t, srv, dir)
	if !syncer2.IsEnabled(theme) {
		t.Fatal("ui_theme opt-in did not survive the restart")
	}
	row := statusRow(t, api2, theme)
	if !row.Enabled || !row.AwaitingConfirmation {
		t.Fatalf("restored row = %+v, want enabled and awaiting confirmation", row)
	}
	if err := api2.Sync_ForcePush(ctx, string(theme)); err == nil {
		t.Error("a restored opt-in must not upload before confirmation")
	}
	if err := syncer2.PushHeld(ctx); !errors.Is(err, corefleet.ErrSyncUploadNotConfirmed) {
		t.Fatalf("PushHeld before confirmation = %v, want ErrSyncUploadNotConfirmed", err)
	}
	if n := fake.putCount(); n != 0 {
		t.Fatalf("%d PUTs after restart without confirmation, want 0", n)
	}
	// Pulls are not gated: a restored category pulls again.
	if err := api2.Sync_ForcePull(ctx, string(theme)); err != nil {
		t.Fatalf("Sync_ForcePull: %v", err)
	}
	if n := fake.getCount(); n != 1 {
		t.Errorf("GETs after restored pull = %d, want 1", n)
	}

	// Confirming uploads the restored category.
	if err := api2.Sync_ConfirmUploads(ctx); err != nil {
		t.Fatalf("Sync_ConfirmUploads: %v", err)
	}
	if n := fake.putCount(); n != 1 {
		t.Fatalf("PUTs after confirmation = %d, want 1", n)
	}

	// Launch 3 (confirmed): restoring itself sends nothing; the next upload
	// goes straight through.
	api3, syncer3, _ := launch(t, srv, dir)
	if !syncer3.IsEnabled(theme) {
		t.Fatal("opt-in lost on the second restart")
	}
	if n := fake.putCount(); n != 1 {
		t.Fatalf("restore sent %d PUTs, want none", n-1)
	}
	if statusRow(t, api3, theme).AwaitingConfirmation {
		t.Error("a confirmed user must not be asked again")
	}
	if err := api3.Sync_ForcePush(ctx, string(theme)); err != nil {
		t.Fatalf("Sync_ForcePush after confirmed restart: %v", err)
	}
	if n := fake.putCount(); n != 2 {
		t.Errorf("PUTs after confirmed restart push = %d, want 2", n)
	}
}

// TestSyncOptIns_DisablePersists_StopsPushAndPull: turning a category off is
// remembered too, and a disabled category neither pushes nor pulls.
func TestSyncOptIns_DisablePersists_StopsPushAndPull(t *testing.T) {
	fake, srv := optInsFleet(t)
	dir := t.TempDir()
	ctx := context.Background()
	theme := corefleet.SyncCategoryUITheme

	api1, _, store1 := launch(t, srv, dir)
	if err := api1.Sync_ConfirmUploads(ctx); err != nil {
		t.Fatal(err)
	}
	if err := api1.Sync_Toggle(ctx, string(theme), true); err != nil {
		t.Fatal(err)
	}
	if n := fake.putCount(); n != 1 {
		t.Fatalf("PUTs after confirmed opt-in = %d, want 1", n)
	}
	// A whole-settings save from a copy loaded before the opt-off must not
	// bring the opt-in back.
	stale, _ := store1.LoadAll()
	if err := api1.Sync_Toggle(ctx, string(theme), false); err != nil {
		t.Fatal(err)
	}
	if err := settings.NewAPI(store1).Set(ctx, stale); err != nil {
		t.Fatalf("Set(stale): %v", err)
	}

	api2, syncer2, store2 := launch(t, srv, dir)
	if syncer2.IsEnabled(theme) {
		t.Fatal("disabled category came back on after restart")
	}
	if got, _ := store2.LoadAll(); len(got.SettingsSyncEnabledCategories) != 0 {
		t.Fatalf("persisted opt-ins = %v, want none", got.SettingsSyncEnabledCategories)
	}
	if statusRow(t, api2, theme).Enabled {
		t.Error("status reports a disabled category as enabled")
	}
	puts, gets := fake.putCount(), fake.getCount()
	if err := api2.Sync_ForcePush(ctx, string(theme)); err != nil {
		t.Fatalf("ForcePush on disabled: %v", err)
	}
	if err := api2.Sync_ForcePull(ctx, string(theme)); err != nil {
		t.Fatalf("ForcePull on disabled: %v", err)
	}
	if fake.putCount() != puts || fake.getCount() != gets {
		t.Errorf("disabled category sent requests: PUT %d->%d, GET %d->%d",
			puts, fake.putCount(), gets, fake.getCount())
	}
}

// TestSyncOptIns_UnknownCategoryNotPersisted: a toggle the syncer rejects is
// not written, so nothing on disk claims an opt-in that never took effect.
func TestSyncOptIns_UnknownCategoryNotPersisted(t *testing.T) {
	_, srv := optInsFleet(t)
	dir := t.TempDir()
	api, _, store := launch(t, srv, dir)
	if err := api.Sync_Toggle(context.Background(), "no_such_category", true); err == nil {
		t.Fatal("unknown category must be refused")
	}
	if got, _ := store.LoadAll(); len(got.SettingsSyncEnabledCategories) != 0 {
		t.Fatalf("persisted %v for an unknown category", got.SettingsSyncEnabledCategories)
	}
}
