package migrations

import (
	"context"
	"testing"
)

// TestRollback_HonoursTheCollapsedLedger pins Rollback's own reading of the
// append-only ledger, independently of Pending's.
//
// v0.63.1 moved Rollback onto the shared latestLedgerRows helper. The
// extraction was byte-identical to the loop it replaced, but nothing in the
// suite asserted Rollback's collapse rule directly — swap it for a
// first-row-wins map and every test still passed (verified by mutation).
// That matters because Rollback runs Down: a version whose most recent row is
// already rolled_back must NOT be rolled back a second time, and Down
// functions in this tree are not written to be re-entrant.
func TestRollback_HonoursTheCollapsedLedger(t *testing.T) {
	t.Parallel()
	for _, f := range executorFactories() {
		t.Run(f.name, func(t *testing.T) {
			ctx := context.Background()
			reg := newRegistryOn(t, f.new(t))

			var downs int
			m := missionMig("sessions", 333, "sessions/0333",
				"CREATE TABLE IF NOT EXISTS s333 (id INTEGER)")
			m.Down = func(ctx context.Context, tx WriteTx) error {
				downs++
				return nil
			}
			mustRegister(t, reg, m)

			if err := reg.Apply(ctx); err != nil {
				t.Fatalf("Apply: %v", err)
			}
			if err := reg.Rollback(ctx, 0); err != nil {
				t.Fatalf("Rollback: %v", err)
			}
			if downs != 1 {
				t.Fatalf("Down ran %d times on the first Rollback, want 1", downs)
			}

			// Second Rollback over the same range: the version's most recent
			// ledger row is rolled_back, so there is nothing to undo.
			if err := reg.Rollback(ctx, 0); err != nil {
				t.Fatalf("second Rollback: %v", err)
			}
			if downs != 1 {
				t.Fatalf("Down ran %d times across two Rollbacks, want 1 — "+
					"an already-rolled-back version must not be undone again", downs)
			}
			if got := len(appliedVersions(t, reg)); got != 2 {
				t.Fatalf("ledger rows = %d, want 2 (one applied, one rolled_back)", got)
			}

			// Re-apply, then roll back once more: the most recent row is
			// applied again, so Down must run a second time.
			if err := reg.Apply(ctx); err != nil {
				t.Fatalf("re-Apply: %v", err)
			}
			if err := reg.Rollback(ctx, 0); err != nil {
				t.Fatalf("third Rollback: %v", err)
			}
			if downs != 2 {
				t.Fatalf("Down ran %d times after a re-Apply, want 2", downs)
			}
		})
	}
}

// TestVerifyLedger_AttributesMissionFromTheRegistryNotTheLedger pins the
// reason the per-mission contiguity check reads OwningMission off the
// REGISTERED migration rather than the ledger row's owning_mission text.
//
// The ledger's text is data written by whichever build applied the row. If a
// row for sessions/0334 carries owning_mission='units' — a renumbering, a
// hand-edited ledger, a mission that was renamed — attributing by that text
// files 334's high-water mark under units and the genuinely-skipped
// sessions/0333 stops looking like a gap. Attributing by the registry cannot
// be fooled that way: the code that is running is the authority on which
// mission owns a version.
func TestVerifyLedger_AttributesMissionFromTheRegistryNotTheLedger(t *testing.T) {
	t.Parallel()
	for _, f := range executorFactories() {
		t.Run(f.name, func(t *testing.T) {
			exec := f.new(t)
			reg := newRegistryOn(t, exec)

			low := missionMig("sessions", 333, "sessions/0333",
				"CREATE TABLE IF NOT EXISTS s333 (id INTEGER)")
			high := missionMig("sessions", 334, "sessions/0334",
				"CREATE TABLE IF NOT EXISTS s334 (id INTEGER)")
			mustRegister(t, reg, low, high)

			// 0334 applied, 0333 skipped — but the ledger row misattributes
			// 334 to a different mission.
			hashed, _ := reg.Lookup(334)
			seedLedger(t, exec, LedgerEntry{
				Version: 334, ID: high.ID, AppliedAt: "ts",
				ContentHash:   hashed.ContentHash,
				OwningMission: "units", // <- drifted text, not the registry's answer
				Action:        LedgerActionApplied,
			})

			if err := reg.VerifyLedger(context.Background()); err == nil {
				t.Fatal("VerifyLedger accepted a skipped sessions/0333 because the " +
					"ledger row for 0334 claimed a different owning_mission")
			}
		})
	}
}
