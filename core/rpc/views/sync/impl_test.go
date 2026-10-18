package sync

import (
	"context"
	"strings"
	"testing"
	"time"

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

// ── WP06: Sync_Status registry enrichment (Scopes + org provenance) ────────

func testSyncKind(id string, scopes ...corefleet.Scope) corefleet.SyncKind {
	return corefleet.SyncKind{
		ID:             id,
		Scopes:         scopes,
		Transport:      corefleet.TransportLWWCategory,
		SecretPolicy:   corefleet.SecretPolicyMustNotContainSecrets,
		ConflictPolicy: corefleet.ConflictPolicyLWW,
	}
}

// TestSyncStatus_EnrichesWithScopesFromRegistry proves Sync_Status reads
// live Scopes off the wired KindRegistry rather than leaving them empty —
// the FR-007 "lists every registered kind with scope" requirement.
func TestSyncStatus_EnrichesWithScopesFromRegistry(t *testing.T) {
	syncer := corefleet.NewSyncer(nil)
	registry := corefleet.NewKindRegistry()
	kind := testSyncKind("mcp_recipes", corefleet.ScopeUser, corefleet.ScopeOrg)
	if err := registry.Register(kind); err != nil {
		t.Fatalf("Register: %v", err)
	}
	syncer.RegisterCategory(corefleet.SyncCategoryMCPRecipes, kind.CategoryConfig())

	api := NewAPI(syncer, nil)
	api.SetSyncKindRegistry(registry)

	views, err := api.Sync_Status(context.Background())
	if err != nil {
		t.Fatalf("Sync_Status: %v", err)
	}
	var found *SyncStatusView
	for i := range views {
		if views[i].Category == "mcp_recipes" {
			found = &views[i]
		}
	}
	if found == nil {
		t.Fatal("expected a mcp_recipes row in Sync_Status")
	}
	if len(found.Scopes) != 2 || found.Scopes[0] != "user" || found.Scopes[1] != "org" {
		t.Errorf("Scopes = %v, want [user org]", found.Scopes)
	}
}

// TestSyncStatus_WithoutRegistry_LeavesScopesEmpty is the mutation-proof
// counterpart: with no registry wired, rows must not fabricate Scopes —
// this preserves the pre-WP06 SyncStatusView shape for callers/tests that
// never call SetSyncKindRegistry.
func TestSyncStatus_WithoutRegistry_LeavesScopesEmpty(t *testing.T) {
	syncer := corefleet.NewSyncer(nil)
	api := NewAPI(syncer, nil) // no SetSyncKindRegistry call

	views, err := api.Sync_Status(context.Background())
	if err != nil {
		t.Fatalf("Sync_Status: %v", err)
	}
	for _, v := range views {
		if len(v.Scopes) != 0 {
			t.Errorf("category %q Scopes = %v, want empty (no registry wired)", v.Category, v.Scopes)
		}
		if v.OrgAppliedAt != "" {
			t.Errorf("category %q OrgAppliedAt = %q, want empty (no registry wired)", v.Category, v.OrgAppliedAt)
		}
	}
}

// TestSyncStatus_IncludesOrgAppliedAtFromRegistry proves the WP03
// provenance tracker (KindRegistry.MarkOrgApplied) is actually consumed
// here — a kind that has never been org-applied shows an empty
// OrgAppliedAt, and one that has shows a real RFC3339 timestamp.
func TestSyncStatus_IncludesOrgAppliedAtFromRegistry(t *testing.T) {
	syncer := corefleet.NewSyncer(nil)
	registry := corefleet.NewKindRegistry()
	mcpKind := testSyncKind("mcp_recipes", corefleet.ScopeUser, corefleet.ScopeOrg)
	themeKind := testSyncKind("ui_theme", corefleet.ScopeUser)
	if err := registry.Register(mcpKind); err != nil {
		t.Fatalf("Register(mcp_recipes): %v", err)
	}
	if err := registry.Register(themeKind); err != nil {
		t.Fatalf("Register(ui_theme): %v", err)
	}
	syncer.RegisterCategory(corefleet.SyncCategoryMCPRecipes, mcpKind.CategoryConfig())
	syncer.RegisterCategory(corefleet.SyncCategoryUITheme, themeKind.CategoryConfig())
	registry.MarkOrgApplied("mcp_recipes", time.Date(2026, 9, 1, 12, 0, 0, 0, time.UTC))

	api := NewAPI(syncer, nil)
	api.SetSyncKindRegistry(registry)

	views, err := api.Sync_Status(context.Background())
	if err != nil {
		t.Fatalf("Sync_Status: %v", err)
	}
	byCategory := map[string]SyncStatusView{}
	for _, v := range views {
		byCategory[v.Category] = v
	}
	if got := byCategory["mcp_recipes"].OrgAppliedAt; got != "2026-09-01T12:00:00Z" {
		t.Errorf("mcp_recipes OrgAppliedAt = %q, want 2026-09-01T12:00:00Z", got)
	}
	if got := byCategory["ui_theme"].OrgAppliedAt; got != "" {
		t.Errorf("ui_theme OrgAppliedAt = %q, want empty (never org-applied)", got)
	}
}
