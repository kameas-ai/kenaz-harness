package sqlite_test

// scroll_position_upgrade_test.go —
// controls-and-readouts-that-tell-the-truth-01PMZ808 UNIT-8 (WP13),
// AC-036 / AC-PI-1.
//
// core/session.Manager.SaveScrollPosition, Store.UpdateScrollPosition and
// the scroll_position column all pre-date this WP with zero non-test
// callers — WP13 built the missing client/binding/serve chain on top of
// them (mirroring SaveDraft). This test proves the part of that chain
// that lives below the RPC layer — Manager -> Store -> SQL — actually
// round-trips against a database a PREVIOUS RELEASE produced, not an
// empty one Open() creates fresh. Per CLAUDE.md blind spot #3, a fresh
// database's migration high-water mark starts at 0 and cannot exercise
// an upgrade path; scroll_position is not new schema, but the discipline
// still applies to any WP-PI persistence claim.

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/kameas-ai/kenaz-harness/core/session"
	"github.com/kameas-ai/kenaz-harness/core/storage/sqlite/upgradesnap"
)

// TestScrollPosition_RoundTripsAgainstAPreviousReleaseDatabase is AC-036.
//
// Falsifiable: this is the SAME production chain
// TestWP03_AC005_V0640SettingsFixture_ResolvesCollapseDefaultTrue's
// settings-half sibling exercises for the settings surface — reverting
// the production SaveScrollPosition/UpdateScrollPosition wiring (e.g.
// making SaveScrollPosition a no-op, matching what the WP13 doc records
// as the pre-fix state of the higher layers) would leave this failing,
// because Get() would still return the session's original (zero)
// ScrollPosition after the save.
func TestScrollPosition_RoundTripsAgainstAPreviousReleaseDatabase(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()

	dumpText, err := os.ReadFile(filepath.Join("testdata", "upgrade", "v0.66.0", "dump.sql"))
	if err != nil {
		t.Fatalf("read v0.66.0 fixture: %v", err)
	}

	rawPath := filepath.Join(dir, "data.db")
	raw := openRawSQLiteAt(t, rawPath)
	if err := upgradesnap.Materialize(ctx, raw, string(dumpText)); err != nil {
		t.Fatalf("materialise v0.66.0 snapshot: %v", err)
	}
	if err := raw.Close(); err != nil {
		t.Fatalf("close raw after materialise: %v", err)
	}

	db := mustOpen(t, dir)
	t.Cleanup(func() { _ = db.Close(ctx) })

	store := session.NewSQLStore(session.NewStorageDB(db))
	mgr := session.NewManager(store)

	sessions, err := mgr.List(ctx)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(sessions) == 0 {
		t.Fatal("v0.66.0 snapshot has no seeded sessions to test against")
	}
	id := sessions[0].ID

	before, err := mgr.Get(ctx, id)
	if err != nil {
		t.Fatalf("Get (before): %v", err)
	}
	if before.ScrollPosition != 0 {
		t.Fatalf("seeded session %q already has ScrollPosition=%d, want 0 (test assumes an unset baseline)", id, before.ScrollPosition)
	}

	// The user scrolls to offset 400 and the app persists it — the
	// exact call MessageList.vue's debounced onScroll now drives through
	// the client/binding/serve chain WP13 built.
	if err := mgr.SaveScrollPosition(ctx, id, 400); err != nil {
		t.Fatalf("SaveScrollPosition: %v", err)
	}

	// "Restart against a database a previous release produced, reopen
	// the session" — close and reopen the SAME on-disk database (not a
	// fresh one) to prove this is a durable SQL write, not an in-memory
	// artefact of the same store instance.
	if err := db.Close(ctx); err != nil {
		t.Fatalf("close: %v", err)
	}
	db2 := mustOpen(t, dir)
	t.Cleanup(func() { _ = db2.Close(ctx) })
	store2 := session.NewSQLStore(session.NewStorageDB(db2))
	mgr2 := session.NewManager(store2)

	after, err := mgr2.Get(ctx, id)
	if err != nil {
		t.Fatalf("Get (after reopen): %v", err)
	}
	if after.ScrollPosition != 400 {
		t.Fatalf("ScrollPosition after reopen = %d, want 400", after.ScrollPosition)
	}
}
