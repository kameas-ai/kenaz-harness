package migrations

import (
	"context"
	"errors"
	"testing"
)

type emitRecord struct {
	Kind    string
	Payload map[string]any
}

func newRecordedEmitter() (*[]emitRecord, EmitFunc) {
	var records []emitRecord
	return &records, func(ctx context.Context, kind string, payload map[string]any) {
		// defensive copy
		p := make(map[string]any, len(payload))
		for k, v := range payload {
			p[k] = v
		}
		records = append(records, emitRecord{Kind: kind, Payload: p})
	}
}

func newRunner(t *testing.T) (*Registry, *fakeDB, *[]emitRecord) {
	t.Helper()
	db := newFakeDB()
	emitsPtr, emitter := newRecordedEmitter()
	reg := NewRegistry(db, func() string { return "ts" }, emitter)
	// Bootstrap the ledger so subsequent applies can write to it.
	if err := EnsureLedger(context.Background(), db); err != nil {
		t.Fatalf("EnsureLedger: %v", err)
	}
	return reg, db, emitsPtr
}

func TestApply_HappyPath(t *testing.T) {
	t.Parallel()
	reg, db, emits := newRunner(t)
	for _, m := range StorageBootstrap() {
		if err := reg.Register(m); err != nil {
			t.Fatalf("Register: %v", err)
		}
	}
	if err := reg.Apply(context.Background()); err != nil {
		t.Fatalf("Apply: %v", err)
	}
	snap := db.Snapshot()
	if len(snap.Ledger) != 2 {
		t.Fatalf("ledger size = %d, want 2", len(snap.Ledger))
	}
	if snap.Ledger[0].Version != 1 || snap.Ledger[1].Version != 2 {
		t.Errorf("ledger versions = %v %v, want 1 2", snap.Ledger[0].Version, snap.Ledger[1].Version)
	}
	for _, e := range snap.Ledger {
		if e.Action != LedgerActionApplied {
			t.Errorf("ledger action = %s, want applied", e.Action)
		}
	}
	// expect 2 migration_applied emits
	count := 0
	for _, ev := range *emits {
		if ev.Kind == "migration_applied" {
			count++
		}
	}
	if count != 2 {
		t.Errorf("migration_applied emits = %d, want 2", count)
	}
}

func TestApply_Idempotent(t *testing.T) {
	t.Parallel()
	reg, db, _ := newRunner(t)
	for _, m := range StorageBootstrap() {
		if err := reg.Register(m); err != nil {
			t.Fatal(err)
		}
	}
	if err := reg.Apply(context.Background()); err != nil {
		t.Fatal(err)
	}
	first := db.Snapshot()
	if err := reg.Apply(context.Background()); err != nil {
		t.Fatal(err)
	}
	second := db.Snapshot()
	if len(first.Ledger) != len(second.Ledger) {
		t.Fatalf("ledger grew on idempotent re-apply: %d -> %d",
			len(first.Ledger), len(second.Ledger))
	}
}

func TestApply_PartialFailure_RollsBack(t *testing.T) {
	t.Parallel()
	reg, db, emits := newRunner(t)
	failing := storageMig(3, "storage/0003-fail", "CREATE TABLE busted (id INT)")
	failing.Up = func(ctx context.Context, tx WriteTx) error {
		// pretend the DDL itself succeeded but a follow-up insert failed.
		_, _ = tx.Exec(ctx, "CREATE TABLE IF NOT EXISTS busted (id INT)")
		return errors.New("oops")
	}
	for _, m := range append(StorageBootstrap(), failing) {
		if err := reg.Register(m); err != nil {
			t.Fatal(err)
		}
	}
	err := reg.Apply(context.Background())
	if !errors.Is(err, ErrMigrationFailed) {
		t.Fatalf("got %v, want ErrMigrationFailed", err)
	}
	snap := db.Snapshot()
	// bootstrap migrations 1 and 2 should have applied successfully.
	if len(snap.Ledger) != 2 {
		t.Fatalf("ledger size = %d, want 2 (bootstrap only)", len(snap.Ledger))
	}
	// the rolled-back migration must not have left its table behind.
	if snap.Tables["busted"] {
		t.Errorf("expected busted table to be rolled back, but it persists")
	}
	// confirm migration_failed event emitted for v3
	var sawFailed bool
	for _, ev := range *emits {
		if ev.Kind == "migration_failed" && ev.Payload["version"] == 3 {
			sawFailed = true
		}
	}
	if !sawFailed {
		t.Error("missing migration_failed emit for v3")
	}
}

func TestRollback_RoundTrip(t *testing.T) {
	t.Parallel()
	reg, db, emits := newRunner(t)
	for _, m := range StorageBootstrap() {
		if err := reg.Register(m); err != nil {
			t.Fatal(err)
		}
	}
	if err := reg.Apply(context.Background()); err != nil {
		t.Fatal(err)
	}
	if err := reg.Rollback(context.Background(), 0); err != nil {
		t.Fatalf("Rollback: %v", err)
	}
	snap := db.Snapshot()
	// expect 2 applied + 2 rolled_back rows (append-only).
	if len(snap.Ledger) != 4 {
		t.Fatalf("ledger size = %d, want 4", len(snap.Ledger))
	}
	// last two should be rolled_back, in reverse version order.
	if snap.Ledger[2].Action != LedgerActionRolledBack {
		t.Errorf("Ledger[2].Action = %s, want rolled_back", snap.Ledger[2].Action)
	}
	if snap.Ledger[2].Version != 2 {
		t.Errorf("Ledger[2].Version = %d, want 2", snap.Ledger[2].Version)
	}
	if snap.Ledger[3].Version != 1 {
		t.Errorf("Ledger[3].Version = %d, want 1", snap.Ledger[3].Version)
	}
	// confirm rolled_back events emitted
	rbCount := 0
	for _, ev := range *emits {
		if ev.Kind == "migration_rolled_back" {
			rbCount++
		}
	}
	if rbCount != 2 {
		t.Errorf("migration_rolled_back emits = %d, want 2", rbCount)
	}
}

func TestVerifyLedger_Clean(t *testing.T) {
	t.Parallel()
	reg, _, _ := newRunner(t)
	for _, m := range StorageBootstrap() {
		if err := reg.Register(m); err != nil {
			t.Fatal(err)
		}
	}
	if err := reg.Apply(context.Background()); err != nil {
		t.Fatal(err)
	}
	if err := reg.VerifyLedger(context.Background()); err != nil {
		t.Errorf("VerifyLedger: %v", err)
	}
}

func TestVerifyLedger_HashMismatch(t *testing.T) {
	t.Parallel()
	reg, db, _ := newRunner(t)
	for _, m := range StorageBootstrap() {
		if err := reg.Register(m); err != nil {
			t.Fatal(err)
		}
	}
	if err := reg.Apply(context.Background()); err != nil {
		t.Fatal(err)
	}
	// Tamper with ledger row.
	db.mu.Lock()
	db.ledger[0].ContentHash = "tampered"
	db.mu.Unlock()
	err := reg.VerifyLedger(context.Background())
	if !errors.Is(err, ErrLedgerHashMismatch) {
		t.Fatalf("got %v, want ErrLedgerHashMismatch", err)
	}
}

func TestVerifyLedger_GapDetection(t *testing.T) {
	t.Parallel()
	reg, db, _ := newRunner(t)
	for _, m := range StorageBootstrap() {
		if err := reg.Register(m); err != nil {
			t.Fatal(err)
		}
	}
	// Register an extra at version 3 to widen the [min,max] window.
	extra := storageMig(3, "storage/0003-extra", "CREATE TABLE extra (id INT)")
	if err := reg.Register(extra); err != nil {
		t.Fatal(err)
	}
	if err := reg.Apply(context.Background()); err != nil {
		t.Fatal(err)
	}
	// Forge a ledger row at v5 referencing a non-existent migration.
	db.mu.Lock()
	db.ledger = append(db.ledger, LedgerEntry{
		Version: 5, ID: "ghost/0001", AppliedAt: "ts",
		ContentHash: "x", OwningMission: "ghost",
		Action: LedgerActionApplied,
	})
	db.mu.Unlock()
	err := reg.VerifyLedger(context.Background())
	if !errors.Is(err, ErrSchemaGap) {
		t.Fatalf("got %v, want ErrSchemaGap", err)
	}
}

func TestApply_PendingComputation(t *testing.T) {
	t.Parallel()
	reg, _, _ := newRunner(t)
	for _, m := range StorageBootstrap() {
		if err := reg.Register(m); err != nil {
			t.Fatal(err)
		}
	}
	pending, err := reg.Pending()
	if err != nil {
		t.Fatal(err)
	}
	if len(pending) != 2 {
		t.Errorf("Pending() len = %d, want 2", len(pending))
	}
	if err := reg.Apply(context.Background()); err != nil {
		t.Fatal(err)
	}
	pending, err = reg.Pending()
	if err != nil {
		t.Fatal(err)
	}
	if len(pending) != 0 {
		t.Errorf("Pending() after Apply len = %d, want 0", len(pending))
	}
}
