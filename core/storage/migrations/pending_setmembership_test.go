package migrations

import (
	"context"
	"errors"
	"testing"
)

// execFactory names an Executor implementation the behavioural suite runs
// against. Every test below runs on BOTH the in-memory fake and a real
// on-disk SQLite database — see the realDB doc comment for why.
type execFactory struct {
	name string
	new  func(t *testing.T) Executor
}

func executorFactories() []execFactory {
	return []execFactory{
		{"fakedb", func(t *testing.T) Executor { return newFakeDB() }},
		{"sqlite", func(t *testing.T) Executor { return newRealDB(t) }},
	}
}

// newRegistryOn builds a Registry over an existing Executor, bootstrapping
// the ledger tables. Multiple registries can be built over one executor —
// that is how the tests model "the user upgraded the app": same database,
// a newer build with more migrations registered.
func newRegistryOn(t *testing.T, exec Executor) *Registry {
	t.Helper()
	if err := EnsureLedger(context.Background(), exec); err != nil {
		t.Fatalf("EnsureLedger: %v", err)
	}
	return NewRegistry(exec, func() string { return "ts" }, nil)
}

// missionMig builds a migration owned by an arbitrary mission so the tests
// can exercise the SHARED version space (sessions 300-399 vs units
// 1100-1199) that broke the max-based rule.
func missionMig(mission string, version int, id, source string) Migration {
	return Migration{
		ID:            id,
		Version:       version,
		OwningMission: mission,
		UpSource:      source,
		Up: func(ctx context.Context, tx WriteTx) error {
			_, err := tx.Exec(ctx, source)
			return err
		},
		Down: func(ctx context.Context, tx WriteTx) error { return nil },
	}
}

func mustRegister(t *testing.T, reg *Registry, migs ...Migration) {
	t.Helper()
	for _, m := range migs {
		if err := reg.Register(m); err != nil {
			t.Fatalf("Register %s: %v", m.ID, err)
		}
	}
}

func pendingVersions(t *testing.T, reg *Registry) []int {
	t.Helper()
	pend, err := reg.Pending()
	if err != nil {
		t.Fatalf("Pending: %v", err)
	}
	out := make([]int, 0, len(pend))
	for _, m := range pend {
		out = append(out, m.Version)
	}
	return out
}

func appliedVersions(t *testing.T, reg *Registry) []int {
	t.Helper()
	rows, err := reg.Applied()
	if err != nil {
		t.Fatalf("Applied: %v", err)
	}
	out := make([]int, 0, len(rows))
	for _, e := range rows {
		out = append(out, e.Version)
	}
	return out
}

func equalInts(a, b []int) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// TestPending_LowerVersionFromAnotherMissionIsNotSkipped is THE regression
// test for the v0.63.0 P0.
//
// The shape it pins is exactly the shipped failure: a HIGH-numbered
// migration from mission A (units/1100, applied June 2026) sits in the
// ledger, and a LOWER-numbered migration from mission B (sessions/0332+,
// written afterwards) has never been applied. Pending() must return B's.
//
// Under the old max-based rule maxApplied=1100 and every sessions migration
// numbered below it was declared already-done — permanently, on every
// upgraded install. `Start session` then died on
// "no such column: move_history_mode".
//
// MUTATION: restore `if m.Version > maxApplied` in Pending() and this test
// fails on both executors (Pending() returns []).
func TestPending_LowerVersionFromAnotherMissionIsNotSkipped(t *testing.T) {
	t.Parallel()
	for _, f := range executorFactories() {
		t.Run(f.name, func(t *testing.T) {
			exec := f.new(t)
			ctx := context.Background()

			// Build 1: only the units mission exists. Its migration applies.
			old := newRegistryOn(t, exec)
			mustRegister(t, old, missionMig("units", 1100, "units/1100-init",
				"CREATE TABLE IF NOT EXISTS units (id INTEGER)"))
			if err := old.Apply(ctx); err != nil {
				t.Fatalf("apply build-1: %v", err)
			}

			// Build 2 (the upgrade): the same database, a newer binary that
			// also carries lower-numbered sessions migrations written AFTER
			// units/1100 landed.
			upgraded := newRegistryOn(t, exec)
			mustRegister(t, upgraded,
				missionMig("units", 1100, "units/1100-init",
					"CREATE TABLE IF NOT EXISTS units (id INTEGER)"),
				missionMig("sessions", 334, "sessions/0334-move-fidelity-columns",
					"CREATE TABLE IF NOT EXISTS move_fidelity (id INTEGER)"),
				missionMig("sessions", 335, "sessions/0335-search-fts-exclude-tool-rows",
					"CREATE TABLE IF NOT EXISTS fts_tool_rows (id INTEGER)"),
			)

			got := pendingVersions(t, upgraded)
			if !equalInts(got, []int{334, 335}) {
				t.Fatalf("Pending() = %v, want [334 335] — a lower-numbered "+
					"migration from another mission must not be skipped because "+
					"a higher-numbered one from a different mission is applied", got)
			}

			// And the runner must actually land them.
			if err := upgraded.Apply(ctx); err != nil {
				t.Fatalf("apply build-2: %v", err)
			}
			if got := appliedVersions(t, upgraded); !equalInts(got, []int{1100, 334, 335}) {
				t.Fatalf("ledger versions = %v, want [1100 334 335]", got)
			}
			if got := pendingVersions(t, upgraded); len(got) != 0 {
				t.Fatalf("Pending() after Apply = %v, want empty", got)
			}
		})
	}
}

// TestPending_AscendingOrderAcrossMissions pins that Apply's input is
// ascending by version regardless of which mission owns each migration, and
// regardless of registration order.
func TestPending_AscendingOrderAcrossMissions(t *testing.T) {
	t.Parallel()
	for _, f := range executorFactories() {
		t.Run(f.name, func(t *testing.T) {
			reg := newRegistryOn(t, f.new(t))
			mustRegister(t, reg,
				missionMig("units", 1101, "units/1101", "CREATE TABLE IF NOT EXISTS u1101 (id INTEGER)"),
				missionMig("sessions", 333, "sessions/0333", "CREATE TABLE IF NOT EXISTS s333 (id INTEGER)"),
				missionMig("units", 1100, "units/1100", "CREATE TABLE IF NOT EXISTS u1100 (id INTEGER)"),
				missionMig("storage", 3, "storage/0003", "CREATE TABLE IF NOT EXISTS s3 (id INTEGER)"),
			)
			if got := pendingVersions(t, reg); !equalInts(got, []int{3, 333, 1100, 1101}) {
				t.Fatalf("Pending() = %v, want [3 333 1100 1101]", got)
			}
			if err := reg.Apply(context.Background()); err != nil {
				t.Fatalf("Apply: %v", err)
			}
			if got := appliedVersions(t, reg); !equalInts(got, []int{3, 333, 1100, 1101}) {
				t.Fatalf("ledger insertion order = %v, want ascending [3 333 1100 1101]", got)
			}
		})
	}
}

// TestPending_RolledBackIsPendingAgain — a version whose most recent ledger
// row is action=rolled_back is not applied, so it is pending again.
//
// MUTATION: drop the `e.Action == LedgerActionApplied` filter in
// effectiveAppliedVersions and this test fails (Pending() returns []).
func TestPending_RolledBackIsPendingAgain(t *testing.T) {
	t.Parallel()
	for _, f := range executorFactories() {
		t.Run(f.name, func(t *testing.T) {
			ctx := context.Background()
			reg := newRegistryOn(t, f.new(t))
			mustRegister(t, reg, missionMig("sessions", 333, "sessions/0333",
				"CREATE TABLE IF NOT EXISTS s333 (id INTEGER)"))
			if err := reg.Apply(ctx); err != nil {
				t.Fatalf("Apply: %v", err)
			}
			if got := pendingVersions(t, reg); len(got) != 0 {
				t.Fatalf("Pending() after Apply = %v, want empty", got)
			}
			if err := reg.Rollback(ctx, 0); err != nil {
				t.Fatalf("Rollback: %v", err)
			}
			if got := pendingVersions(t, reg); !equalInts(got, []int{333}) {
				t.Fatalf("Pending() after Rollback = %v, want [333]", got)
			}
			// Re-apply, then the rolled_back row must no longer win: the
			// most recent row for v333 is applied again.
			if err := reg.Apply(ctx); err != nil {
				t.Fatalf("re-Apply: %v", err)
			}
			if got := pendingVersions(t, reg); len(got) != 0 {
				t.Fatalf("Pending() after re-Apply = %v, want empty", got)
			}
		})
	}
}

// TestPending_UnknownLedgerEntriesAreInert — a ledger row for a version with
// no registered migration (a mission whose code was removed) must neither
// panic nor influence the pending selection.
func TestPending_UnknownLedgerEntriesAreInert(t *testing.T) {
	t.Parallel()
	for _, f := range executorFactories() {
		t.Run(f.name, func(t *testing.T) {
			ctx := context.Background()
			exec := f.new(t)
			reg := newRegistryOn(t, exec)

			// Seed ledger rows nothing in the registry knows about — one
			// applied, one rolled back, both far above the registered set.
			seedLedger(t, exec,
				LedgerEntry{Version: 1103, ID: "ghost/1103", AppliedAt: "ts",
					ContentHash: "x", OwningMission: "units", Action: LedgerActionApplied},
				LedgerEntry{Version: 1199, ID: "ghost/1199", AppliedAt: "ts",
					ContentHash: "x", OwningMission: "units", Action: LedgerActionRolledBack},
			)

			mustRegister(t, reg, missionMig("sessions", 332, "sessions/0332",
				"CREATE TABLE IF NOT EXISTS s332 (id INTEGER)"))

			if got := pendingVersions(t, reg); !equalInts(got, []int{332}) {
				t.Fatalf("Pending() = %v, want [332]", got)
			}
			if err := reg.Apply(ctx); err != nil {
				t.Fatalf("Apply: %v", err)
			}
			if got := pendingVersions(t, reg); len(got) != 0 {
				t.Fatalf("Pending() after Apply = %v, want empty", got)
			}
		})
	}
}

// TestPending_DuplicateLedgerRowsCollapse — the ledger is append-only and can
// legitimately hold several rows per version. The most recent row wins.
func TestPending_DuplicateLedgerRowsCollapse(t *testing.T) {
	t.Parallel()
	for _, f := range executorFactories() {
		t.Run(f.name, func(t *testing.T) {
			exec := f.new(t)
			reg := newRegistryOn(t, exec)
			mig := missionMig("sessions", 332, "sessions/0332",
				"CREATE TABLE IF NOT EXISTS s332 (id INTEGER)")
			mustRegister(t, reg, mig)

			// applied, applied (duplicate) -> not pending.
			seedLedger(t, exec,
				LedgerEntry{Version: 332, ID: mig.ID, AppliedAt: "ts",
					ContentHash: "x", OwningMission: "sessions", Action: LedgerActionApplied},
				LedgerEntry{Version: 332, ID: mig.ID, AppliedAt: "ts",
					ContentHash: "x", OwningMission: "sessions", Action: LedgerActionApplied},
			)
			if got := pendingVersions(t, reg); len(got) != 0 {
				t.Fatalf("Pending() with duplicate applied rows = %v, want empty", got)
			}

			// …then a rolled_back row lands last -> pending again, once.
			rb := 332
			seedLedger(t, exec, LedgerEntry{Version: 332, ID: mig.ID, AppliedAt: "ts",
				ContentHash: "x", OwningMission: "sessions",
				Action: LedgerActionRolledBack, RolledBackFromVersion: &rb})
			if got := pendingVersions(t, reg); !equalInts(got, []int{332}) {
				t.Fatalf("Pending() after trailing rolled_back = %v, want exactly [332]", got)
			}
		})
	}
}

// TestApply_AppliedMigrationsNeverReRun pins idempotency by counting Up
// invocations, not just ledger rows: a second Apply must not touch the
// database at all.
func TestApply_AppliedMigrationsNeverReRun(t *testing.T) {
	t.Parallel()
	for _, f := range executorFactories() {
		t.Run(f.name, func(t *testing.T) {
			ctx := context.Background()
			reg := newRegistryOn(t, f.new(t))
			var ups int
			m := missionMig("sessions", 334, "sessions/0334",
				"CREATE TABLE IF NOT EXISTS s334 (id INTEGER)")
			inner := m.Up
			m.Up = func(ctx context.Context, tx WriteTx) error {
				ups++
				return inner(ctx, tx)
			}
			mustRegister(t, reg, m)

			for i := 0; i < 3; i++ {
				if err := reg.Apply(ctx); err != nil {
					t.Fatalf("Apply #%d: %v", i+1, err)
				}
			}
			if ups != 1 {
				t.Fatalf("Up ran %d times across 3 Applies, want 1", ups)
			}
			if got := appliedVersions(t, reg); !equalInts(got, []int{334}) {
				t.Fatalf("ledger = %v, want [334]", got)
			}
		})
	}
}

// TestVerifyLedger_CrossMissionInterleavingIsClean — a pending sessions/0332
// sitting BELOW an applied units/1100 is the normal state of every healthy
// upgraded install, not a schema gap.
//
// MUTATION: restore the global "every registered migration in [min,max]
// must be applied" rule and this test fails with ErrSchemaGap.
func TestVerifyLedger_CrossMissionInterleavingIsClean(t *testing.T) {
	t.Parallel()
	for _, f := range executorFactories() {
		t.Run(f.name, func(t *testing.T) {
			ctx := context.Background()
			exec := f.new(t)

			old := newRegistryOn(t, exec)
			mustRegister(t, old,
				missionMig("storage", 1, "storage/0001", "CREATE TABLE IF NOT EXISTS s1 (id INTEGER)"),
				missionMig("units", 1100, "units/1100", "CREATE TABLE IF NOT EXISTS u1100 (id INTEGER)"),
			)
			if err := old.Apply(ctx); err != nil {
				t.Fatalf("Apply: %v", err)
			}

			upgraded := newRegistryOn(t, exec)
			mustRegister(t, upgraded,
				missionMig("storage", 1, "storage/0001", "CREATE TABLE IF NOT EXISTS s1 (id INTEGER)"),
				missionMig("units", 1100, "units/1100", "CREATE TABLE IF NOT EXISTS u1100 (id INTEGER)"),
				missionMig("sessions", 332, "sessions/0332", "CREATE TABLE IF NOT EXISTS s332 (id INTEGER)"),
			)
			if err := upgraded.VerifyLedger(ctx); err != nil {
				t.Fatalf("VerifyLedger on a healthy cross-mission ledger: %v", err)
			}
		})
	}
}

// TestVerifyLedger_GapWithinMissionStillDetected — the check must keep real
// teeth: a sessions migration skipped while a HIGHER sessions migration is
// applied is a genuine gap.
//
// MUTATION: delete the per-mission contiguity loop in VerifyLedger and this
// test fails (VerifyLedger returns nil).
func TestVerifyLedger_GapWithinMissionStillDetected(t *testing.T) {
	t.Parallel()
	for _, f := range executorFactories() {
		t.Run(f.name, func(t *testing.T) {
			ctx := context.Background()
			exec := f.new(t)
			reg := newRegistryOn(t, exec)

			low := missionMig("sessions", 333, "sessions/0333", "CREATE TABLE IF NOT EXISTS s333 (id INTEGER)")
			high := missionMig("sessions", 334, "sessions/0334", "CREATE TABLE IF NOT EXISTS s334 (id INTEGER)")
			mustRegister(t, reg, low, high)

			// Apply only the HIGH one by seeding its ledger row directly —
			// 0333 is registered but never landed.
			hashed, _ := reg.Lookup(334)
			seedLedger(t, exec, LedgerEntry{
				Version: 334, ID: high.ID, AppliedAt: "ts",
				ContentHash: hashed.ContentHash, OwningMission: "sessions",
				Action: LedgerActionApplied,
			})

			err := reg.VerifyLedger(ctx)
			if !errors.Is(err, ErrSchemaGap) {
				t.Fatalf("VerifyLedger = %v, want ErrSchemaGap for a skipped in-mission migration", err)
			}
		})
	}
}

// TestVerifyLedger_UnknownAppliedEntryStillDetected keeps the second half of
// the contract: an applied ledger row with no registered migration is still
// ErrSchemaGap.
func TestVerifyLedger_UnknownAppliedEntryStillDetected(t *testing.T) {
	t.Parallel()
	for _, f := range executorFactories() {
		t.Run(f.name, func(t *testing.T) {
			exec := f.new(t)
			reg := newRegistryOn(t, exec)
			mustRegister(t, reg, missionMig("sessions", 332, "sessions/0332",
				"CREATE TABLE IF NOT EXISTS s332 (id INTEGER)"))
			if err := reg.Apply(context.Background()); err != nil {
				t.Fatalf("Apply: %v", err)
			}
			seedLedger(t, exec, LedgerEntry{
				Version: 399, ID: "ghost/0399", AppliedAt: "ts",
				ContentHash: "x", OwningMission: "sessions", Action: LedgerActionApplied,
			})
			err := reg.VerifyLedger(context.Background())
			if !errors.Is(err, ErrSchemaGap) {
				t.Fatalf("VerifyLedger = %v, want ErrSchemaGap for an unknown applied entry", err)
			}
		})
	}
}

// seedLedger appends raw ledger rows, bypassing Apply. It is how the tests
// model a database written by a different build.
func seedLedger(t *testing.T, exec Executor, entries ...LedgerEntry) {
	t.Helper()
	ctx := context.Background()
	for _, e := range entries {
		entry := e
		if err := exec.WriteTx(ctx, func(tx WriteTx) error {
			return appendLedger(ctx, tx, entry)
		}); err != nil {
			t.Fatalf("seedLedger v%d: %v", entry.Version, err)
		}
	}
}
