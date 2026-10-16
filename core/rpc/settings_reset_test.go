package rpc

// settings_reset_test.go — "Reset settings" recovers from a corrupt
// settings.json: the bad file is kept as a 0600 backup, the defaults load,
// and settings sync is no longer paused. Drives a real FileStore on disk.

import (
	"context"
	"errors"
	"net/http/httptest"
	"os"
	"path/filepath"
	"regexp"
	"testing"
	"time"

	corefleet "github.com/kameas-ai/kenaz-harness/core/fleet"
	"github.com/kameas-ai/kenaz-harness/core/rpc/views/settings"
)

func TestSettingsResetUnreadable_RecoversCorruptFile(t *testing.T) {
	if corefleet.Disabled() {
		t.Skip("HARNESS_FLEET_DISABLED=1")
	}
	fake := &consentFakeFleet{}
	srv := httptest.NewServer(fake)
	t.Cleanup(srv.Close)
	corefleet.SeedFleetConfigForTesting(srv.URL, corefleet.FleetConfig{
		Issuer: srv.URL, ClientID: "test", APIBaseURL: srv.URL, FetchedAt: time.Now().UTC(),
	})
	corefleet.SetExternalTokenSource(func() string { return "reset-token" })
	t.Cleanup(func() { corefleet.SetExternalTokenSource(nil) })

	dir := t.TempDir()
	store, err := settings.NewFileStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	syncAPI, syncer := newConsentRig(t, srv.URL, store)
	if err := syncAPI.Sync_Toggle(ctx, string(corefleet.SyncCategoryUITheme), true); err != nil {
		t.Fatalf("Sync_Toggle: %v", err)
	}
	if err := syncAPI.Sync_ConfirmUploads(ctx); err != nil {
		t.Fatal(err)
	}

	path := filepath.Join(dir, "kenaz-harness", "settings.json")
	corrupt := []byte("{not json")
	if err := os.WriteFile(path, corrupt, 0o644); err != nil {
		t.Fatal(err)
	}
	if row := statusRow(t, syncAPI, corefleet.SyncCategoryUITheme); row.UploadsBlockedReason == "" || !row.SettingsUnreadable {
		t.Fatalf("before reset: row = %+v, want sync paused on an unreadable file", row)
	}

	a := &API{settingsImpl: settings.NewAPI(store), settingsSyncer: syncer}
	b := NewBindings(a)
	res, err := b.Settings_ResetUnreadable()
	if err != nil {
		t.Fatalf("Settings_ResetUnreadable: %v", err)
	}

	// The bad file is kept, byte for byte, owner-only, next to settings.json.
	if filepath.Dir(res.BackupPath) != filepath.Dir(path) ||
		!regexp.MustCompile(`^settings\.json\.corrupt-\d{8}T\d{6}Z$`).MatchString(filepath.Base(res.BackupPath)) {
		t.Fatalf("backup path = %q", res.BackupPath)
	}
	got, err := os.ReadFile(res.BackupPath)
	if err != nil || string(got) != string(corrupt) {
		t.Fatalf("backup = %q, %v; want the corrupt bytes", got, err)
	}
	if info, _ := os.Stat(res.BackupPath); info.Mode().Perm() != 0o600 {
		t.Fatalf("backup mode = %o, want 600", info.Mode().Perm())
	}

	// Defaults load.
	s, err := store.LoadAll()
	if err != nil {
		t.Fatalf("LoadAll after reset: %v", err)
	}
	if s.Theme != "system" || s.LastRoute != "/sessions" || s.SettingsSyncUploadsConfirmed || len(s.SettingsSyncEnabledCategories) != 0 {
		t.Fatalf("settings after reset = %+v, want defaults", s)
	}

	// Sync is no longer paused, and the live opt-in matches the file.
	row := statusRow(t, syncAPI, corefleet.SyncCategoryUITheme)
	if row.UploadsBlockedReason != "" || row.SettingsUnreadable {
		t.Fatalf("after reset: row = %+v, want sync not paused", row)
	}
	if row.Enabled {
		t.Fatal("ui_theme still enabled live although the reset settings have it off")
	}

	// A readable file is not reset again.
	if _, err := b.Settings_ResetUnreadable(); !errors.Is(err, settings.ErrSettingsReadable) {
		t.Fatalf("second reset = %v, want ErrSettingsReadable", err)
	}
	if matches, _ := filepath.Glob(path + ".corrupt-*"); len(matches) != 1 {
		t.Fatalf("backups = %v, want exactly one", matches)
	}
}
