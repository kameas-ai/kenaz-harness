// fleet_merge_test.go — unit tests for Library.MergeFleetEntries and
// related fleet-sync helpers.
//
// (harness-fleet-sync-activation-01NSYNC01 NFR-004)
package contexts_test

import (
	"testing"

	contextpack "github.com/kameas-ai/kenaz-harness/core/context/pack"
	"github.com/kameas-ai/kenaz-harness/core/contexts"
)

// ── MergeFleetEntries ─────────────────────────────────────────────────────────

// TestMergeFleetEntries_PersonalLayerSkipped verifies that personal-layer
// entries are silently ignored (NFR-003: personal-stays-local).
func TestMergeFleetEntries_PersonalLayerSkipped(t *testing.T) {
	lib, _ := newLib(t)

	lib.MergeFleetEntries([]contexts.FleetEntry{
		{ID: "p1", Layer: contextpack.LayerPersonal, Kind: "guidance", Title: "Private", Body: "secret"},
	})

	if entries := lib.FleetEntries(); len(entries) != 0 {
		t.Errorf("personal entry leaked into fleet layer: %v", entries)
	}
}

// TestMergeFleetEntries_Tombstone verifies that tombstoned entries are
// removed from the fleet entry cache when received.
func TestMergeFleetEntries_Tombstone(t *testing.T) {
	lib, _ := newLib(t)

	// First upsert a live entry.
	lib.MergeFleetEntries([]contexts.FleetEntry{
		{ID: "e1", Layer: contextpack.LayerTeam, Kind: "guidance", Title: "Live", Body: "body", Version: 1},
	})
	if got := lib.FleetEntries(); len(got) != 1 {
		t.Fatalf("expected 1 entry, got %d", len(got))
	}

	// Now tombstone it.
	deletedAt := "2026-06-10T00:00:00Z"
	lib.MergeFleetEntries([]contexts.FleetEntry{
		{ID: "e1", Layer: contextpack.LayerTeam, DeletedAt: &deletedAt},
	})
	if got := lib.FleetEntries(); len(got) != 0 {
		t.Errorf("tombstoned entry still present: %v", got)
	}
}

// TestMergeFleetEntries_NormalUpsert verifies that team/org entries are
// inserted and that a newer version overwrites an older one.
func TestMergeFleetEntries_NormalUpsert(t *testing.T) {
	lib, _ := newLib(t)

	lib.MergeFleetEntries([]contexts.FleetEntry{
		{ID: "e1", Layer: contextpack.LayerTeam, Kind: "guidance", Title: "v1", Body: "first", Version: 1},
	})
	lib.MergeFleetEntries([]contexts.FleetEntry{
		{ID: "e1", Layer: contextpack.LayerTeam, Kind: "guidance", Title: "v2", Body: "second", Version: 2},
	})

	entries := lib.FleetEntries()
	if len(entries) != 1 {
		t.Fatalf("expected 1 entry after upsert, got %d", len(entries))
	}
	if entries[0].Title != "v2" {
		t.Errorf("Title=%q, want v2 (newer upsert should win)", entries[0].Title)
	}
}

// TestMergeFleetEntries_OrgLayerAccepted verifies org-layer entries work.
func TestMergeFleetEntries_OrgLayerAccepted(t *testing.T) {
	lib, _ := newLib(t)

	lib.MergeFleetEntries([]contexts.FleetEntry{
		{ID: "org1", Layer: contextpack.LayerOrg, Kind: "fact", Title: "Org fact", Version: 1},
	})

	entries := lib.FleetEntries()
	if len(entries) != 1 {
		t.Fatalf("expected 1 org entry, got %d", len(entries))
	}
	if entries[0].ID != "org1" {
		t.Errorf("ID=%q, want org1", entries[0].ID)
	}
}

// ── Conflict detection (FR-013) ───────────────────────────────────────────────

// TestMergeFleetEntries_ConflictPreservesLocalEdit verifies that when the
// server carries a version strictly less than the locally-edited version,
// the local edit is preserved and a conflict event is emitted.
func TestMergeFleetEntries_ConflictPreservesLocalEdit(t *testing.T) {
	lib, _ := newLib(t)

	// Seed the fleet cache with version 3.
	lib.MergeFleetEntries([]contexts.FleetEntry{
		{ID: "c1", Layer: contextpack.LayerTeam, Title: "server-v3", Body: "server body", Version: 3},
	})

	// Mark that the user edited locally at version 5.
	lib.MarkLocalVersion("c1", 5)

	// Wire a conflict notifier.
	var conflictFired bool
	var gotConflict contexts.ContextConflict
	lib.SetConflictNotifier(func(c contexts.ContextConflict) {
		conflictFired = true
		gotConflict = c
	})

	// Server now sends version 4 — still older than local edit (v5).
	lib.MergeFleetEntries([]contexts.FleetEntry{
		{ID: "c1", Layer: contextpack.LayerTeam, Title: "server-v4", Body: "server body v4", Version: 4},
	})

	if !conflictFired {
		t.Error("conflict notifier must fire when server version < local edit version")
	}
	if gotConflict.NodeID != "c1" {
		t.Errorf("NodeID=%q, want c1", gotConflict.NodeID)
	}
	if gotConflict.ServerVersion != 4 {
		t.Errorf("ServerVersion=%d, want 4", gotConflict.ServerVersion)
	}
	if gotConflict.ClientVersion != 5 {
		t.Errorf("ClientVersion=%d, want 5", gotConflict.ClientVersion)
	}

	// The local entry must NOT have been overwritten.
	entries := lib.FleetEntries()
	for _, e := range entries {
		if e.ID == "c1" && e.Title != "server-v3" {
			// The merge from before marking local version set the title to "server-v3".
			// After the conflict the title should remain "server-v3" (not "server-v4").
			t.Errorf("local edit overwritten: Title=%q, want server-v3", e.Title)
		}
	}
}

// TestMergeFleetEntries_NoConflict_ServerNewer verifies that when the server
// version is greater than or equal to the local version the update is applied.
func TestMergeFleetEntries_NoConflict_ServerNewer(t *testing.T) {
	lib, _ := newLib(t)

	lib.MarkLocalVersion("e2", 2)

	conflictFired := false
	lib.SetConflictNotifier(func(_ contexts.ContextConflict) { conflictFired = true })

	// Server sends version 3 — newer than local (2). Should upsert without conflict.
	lib.MergeFleetEntries([]contexts.FleetEntry{
		{ID: "e2", Layer: contextpack.LayerTeam, Title: "server-v3", Version: 3},
	})

	if conflictFired {
		t.Error("conflict must not fire when server version >= local version")
	}
	entries := lib.FleetEntries()
	if len(entries) != 1 || entries[0].Title != "server-v3" {
		t.Errorf("FleetEntries=%v, want [{server-v3}]", entries)
	}
}

// ── MarkLocalVersion / FleetEntries helpers ───────────────────────────────────

// TestMarkLocalVersion_ZeroClearsRecord verifies that MarkLocalVersion(id, 0)
// removes the local version record so subsequent merges no longer conflict.
func TestMarkLocalVersion_ZeroClearsRecord(t *testing.T) {
	lib, _ := newLib(t)

	lib.MarkLocalVersion("e3", 10)

	conflictFired := false
	lib.SetConflictNotifier(func(_ contexts.ContextConflict) { conflictFired = true })

	// Clear the local version record.
	lib.MarkLocalVersion("e3", 0)

	// Server sends version 5 — would have conflicted with local=10 if not cleared.
	lib.MergeFleetEntries([]contexts.FleetEntry{
		{ID: "e3", Layer: contextpack.LayerTeam, Title: "from-server", Version: 5},
	})

	if conflictFired {
		t.Error("conflict fired after local version was cleared with MarkLocalVersion(id,0)")
	}
}

// TestFleetEntries_TombstonesExcluded verifies FleetEntries() excludes
// any entry that has a non-nil DeletedAt even if still in the internal map.
// (Regression guard: the cache only holds live entries after MergeFleetEntries
// removes tombstones, so this verifies the remove path.)
func TestFleetEntries_TombstonesExcluded(t *testing.T) {
	lib, _ := newLib(t)

	// Insert two live entries.
	lib.MergeFleetEntries([]contexts.FleetEntry{
		{ID: "alive", Layer: contextpack.LayerTeam, Title: "alive", Version: 1},
		{ID: "dead", Layer: contextpack.LayerTeam, Title: "will die", Version: 1},
	})

	// Tombstone the second entry.
	deletedAt := "2026-06-10T00:00:00Z"
	lib.MergeFleetEntries([]contexts.FleetEntry{
		{ID: "dead", Layer: contextpack.LayerTeam, DeletedAt: &deletedAt},
	})

	entries := lib.FleetEntries()
	if len(entries) != 1 {
		t.Fatalf("expected 1 live entry, got %d: %v", len(entries), entries)
	}
	if entries[0].ID != "alive" {
		t.Errorf("surviving entry ID=%q, want alive", entries[0].ID)
	}
}
