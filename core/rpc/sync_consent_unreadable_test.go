package rpc

// sync_consent_unreadable_test.go — when the settings file that records the
// upload confirmation cannot be read, uploads stay closed (fail closed) AND
// Sync_Status says why. Before the fix the gate failed closed silently: the
// panel kept asking the user to "Start syncing", which could not help.

import (
	"context"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	corefleet "github.com/kameas-ai/kenaz-harness/core/fleet"
	"github.com/kameas-ai/kenaz-harness/core/rpc/views/settings"
)

func TestSyncConsent_UnreadableSettingsSurfacedNotSilent(t *testing.T) {
	if corefleet.Disabled() {
		t.Skip("HARNESS_FLEET_DISABLED=1")
	}
	fake := &consentFakeFleet{}
	srv := httptest.NewServer(fake)
	t.Cleanup(srv.Close)
	corefleet.SeedFleetConfigForTesting(srv.URL, corefleet.FleetConfig{
		Issuer: srv.URL, ClientID: "test", APIBaseURL: srv.URL, FetchedAt: time.Now().UTC(),
	})
	corefleet.SetExternalTokenSource(func() string { return "unreadable-token" })
	t.Cleanup(func() { corefleet.SetExternalTokenSource(nil) })

	dir := t.TempDir()
	store, err := settings.NewFileStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	api, _ := newConsentRig(t, srv.URL, store)
	if err := api.Sync_Toggle(ctx, string(corefleet.SyncCategoryUITheme), true); err != nil {
		t.Fatalf("Sync_Toggle: %v", err)
	}
	if err := api.Sync_ConfirmUploads(ctx); err != nil {
		t.Fatal(err)
	}
	puts := fake.putCount()

	// The settings file becomes unreadable (corrupted on disk).
	path := filepath.Join(dir, "kenaz-harness", "settings.json")
	good, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("{not json"), 0o600); err != nil {
		t.Fatal(err)
	}

	row := statusRow(t, api, corefleet.SyncCategoryUITheme)
	if !strings.Contains(row.UploadsBlockedReason, "could not read your settings file") {
		t.Fatalf("UploadsBlockedReason = %q, want the unreadable settings named", row.UploadsBlockedReason)
	}
	if row.AwaitingConfirmation {
		t.Error("confirming cannot fix an unreadable file; the row must not ask for it")
	}
	_ = api.Sync_ForcePush(ctx, string(corefleet.SyncCategoryUITheme))
	if n := fake.putCount(); n != puts {
		t.Fatalf("%d PUTs while consent was unreadable, want none (fail closed)", n-puts)
	}

	// Readable again: the reason clears and uploads resume.
	if err := os.WriteFile(path, good, 0o600); err != nil {
		t.Fatal(err)
	}
	if r := statusRow(t, api, corefleet.SyncCategoryUITheme).UploadsBlockedReason; r != "" {
		t.Fatalf("reason still set after the file is readable: %q", r)
	}
}
