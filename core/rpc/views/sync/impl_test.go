package sync

import (
	"context"
	"strings"
	"testing"

	corefleet "github.com/kameas-ai/kenaz-harness/core/fleet"
)

// TestSyncToggleInstalledMCPUsesCanonicalCategoryID is the Go-side half of
// AC-009 (fleet-enforcement-truth-01PMZ505 WP07). Before this WP,
// SyncPanel.vue declared id: 'installed_mcp_servers' — a string that never
// matched corefleet.SyncCategoryInstalledMCP ("installed_mcp",
// core/fleet/sync.go:34), so every toggle on that row reached
// Sync_Toggle → Syncer.SetEnabled → the "unknown category" error
// (core/fleet/sync.go:157). This pins both halves of the round trip
// through the same Sync_Toggle path production code calls.
func TestSyncToggleInstalledMCPUsesCanonicalCategoryID(t *testing.T) {
	syncer := corefleet.NewSyncer(nil)
	api := NewAPI(syncer, nil)
	ctx := context.Background()

	// The historic panel id: never a member of AllSyncCategories(), so it
	// must fail with the unknown-category error regardless of this WP.
	err := api.Sync_Toggle(ctx, "installed_mcp_servers", false)
	if err == nil || !strings.Contains(err.Error(), "unknown category") {
		t.Fatalf(`Sync_Toggle(%q) = %v, want an "unknown category" error`, "installed_mcp_servers", err)
	}

	// The canonical wire value (corefleet.SyncCategoryInstalledMCP) is
	// pre-registered in Syncer.categories by NewSyncer via
	// AllSyncCategories() (sync.go:123), independent of whether a
	// collector/applier has been wired — so a category-recognition error
	// is impossible here. enabled=false so the assertion is not
	// entangled with Push's separate ErrFleetDisabled path (syncer has no
	// client in this test).
	if err := api.Sync_Toggle(ctx, string(corefleet.SyncCategoryInstalledMCP), false); err != nil {
		t.Fatalf("Sync_Toggle(%q) = %v, want nil — this is the real wire value SyncPanel.vue must use", corefleet.SyncCategoryInstalledMCP, err)
	}
}
