package migrations

import (
	"context"
	"testing"
)

// dummyUp is a no-op Up function for test migrations.
func dummyUp(_ context.Context, _ WriteTx) error { return nil }

// buildDoctorRegistry returns a Registry backed by a fresh fakeDB with
// the ledger bootstrapped.
func buildDoctorRegistry(t *testing.T) (*Registry, *fakeDB) {
	t.Helper()
	db := newFakeDB()
	reg := NewRegistry(db, nil, nil)
	if err := EnsureLedger(context.Background(), db); err != nil {
		t.Fatalf("EnsureLedger: %v", err)
	}
	return reg, db
}

// seedApplied writes a raw applied ledger entry directly so we can plant
// IDs that don't match what the registry knows (simulating the renumbering
// incident).
func seedApplied(t *testing.T, db *fakeDB, e LedgerEntry) {
	t.Helper()
	err := db.WriteTx(context.Background(), func(tx WriteTx) error {
		_, err := tx.Exec(context.Background(),
			`INSERT INTO harness_migrations
                (version, id, applied_at, content_hash, owning_mission, action, rolled_back_from_version)
             VALUES (?, ?, ?, ?, ?, ?, ?)`,
			e.Version, e.ID, e.AppliedAt, e.ContentHash,
			e.OwningMission, string(e.Action), nil,
		)
		return err
	})
	if err != nil {
		t.Fatalf("seedApplied v%d: %v", e.Version, err)
	}
}

func TestDetectDrift_NoDrift(t *testing.T) {
	reg, _ := buildDoctorRegistry(t)

	// Register + apply a migration so ledger and registry agree.
	if err := reg.Register(Migration{
		ID:            "create_foo",
		Version:       1,
		OwningMission: "storage",
		Up:            dummyUp,
		UpSource:      "CREATE TABLE foo (id INTEGER PRIMARY KEY);",
	}); err != nil {
		t.Fatalf("Register: %v", err)
	}
	if err := reg.Apply(context.Background()); err != nil {
		t.Fatalf("Apply: %v", err)
	}

	report, err := DetectDrift(reg)
	if err != nil {
		t.Fatalf("DetectDrift: %v", err)
	}
	for _, d := range report.Drifts {
		if d.Kind == "id_mismatch" || d.Kind == "ledger_only" {
			t.Errorf("unexpected drift kind=%q v%d", d.Kind, d.Version)
		}
	}
}

func TestDetectDrift_IDMismatch(t *testing.T) {
	reg, db := buildDoctorRegistry(t)

	// Plant a ledger row with the OLD id (simulating the PR #71 incident).
	seedApplied(t, db, LedgerEntry{
		Version:       322,
		ID:            "branch-display-meta", // stale id
		AppliedAt:     "2026-04-01T00:00:00Z",
		ContentHash:   "deadbeef",
		OwningMission: "sessions",
		Action:        LedgerActionApplied,
	})

	// Register with the NEW id.
	if err := reg.Register(Migration{
		ID:            "last_usage_json", // current id
		Version:       322,
		OwningMission: "sessions",
		Up:            dummyUp,
		UpSource:      "ALTER TABLE sessions ADD COLUMN last_usage_json TEXT;",
	}); err != nil {
		t.Fatalf("Register: %v", err)
	}

	report, err := DetectDrift(reg)
	if err != nil {
		t.Fatalf("DetectDrift: %v", err)
	}

	var found *DriftEntry
	for i := range report.Drifts {
		if report.Drifts[i].Version == 322 {
			found = &report.Drifts[i]
		}
	}
	if found == nil {
		t.Fatal("expected a drift entry for v322, got none")
	}
	if found.Kind != "id_mismatch" {
		t.Errorf("kind = %q, want id_mismatch", found.Kind)
	}
	if found.Severity != "error" {
		t.Errorf("severity = %q, want error", found.Severity)
	}
	if found.LedgerID != "branch-display-meta" {
		t.Errorf("LedgerID = %q, want branch-display-meta", found.LedgerID)
	}
	if found.ExpectedID != "last_usage_json" {
		t.Errorf("ExpectedID = %q, want last_usage_json", found.ExpectedID)
	}
	if found.Suggestion == "" {
		t.Error("Suggestion should not be empty")
	}
}

func TestDetectDrift_LedgerOnly(t *testing.T) {
	reg, db := buildDoctorRegistry(t)

	// Plant an applied ledger row with no corresponding registered migration.
	seedApplied(t, db, LedgerEntry{
		Version:       150,
		ID:            "orphaned-migration",
		AppliedAt:     "2026-03-01T00:00:00Z",
		ContentHash:   "abc123",
		OwningMission: "event-log",
		Action:        LedgerActionApplied,
	})

	report, err := DetectDrift(reg)
	if err != nil {
		t.Fatalf("DetectDrift: %v", err)
	}

	var found *DriftEntry
	for i := range report.Drifts {
		if report.Drifts[i].Version == 150 {
			found = &report.Drifts[i]
		}
	}
	if found == nil {
		t.Fatal("expected a drift entry for v150, got none")
	}
	if found.Kind != "ledger_only" {
		t.Errorf("kind = %q, want ledger_only", found.Kind)
	}
	if found.Severity != "warning" {
		t.Errorf("severity = %q, want warning", found.Severity)
	}
}

func TestDetectDrift_CodeOnly(t *testing.T) {
	reg, _ := buildDoctorRegistry(t)

	// Register a migration but do NOT apply it.
	if err := reg.Register(Migration{
		ID:            "pending_migration",
		Version:       2,
		OwningMission: "storage",
		Up:            dummyUp,
		UpSource:      "CREATE TABLE bar (id INTEGER PRIMARY KEY);",
	}); err != nil {
		t.Fatalf("Register: %v", err)
	}

	report, err := DetectDrift(reg)
	if err != nil {
		t.Fatalf("DetectDrift: %v", err)
	}

	var found *DriftEntry
	for i := range report.Drifts {
		if report.Drifts[i].Version == 2 {
			found = &report.Drifts[i]
		}
	}
	if found == nil {
		t.Fatal("expected a drift entry for v2, got none")
	}
	if found.Kind != "code_only" {
		t.Errorf("kind = %q, want code_only", found.Kind)
	}
	if found.Severity != "info" {
		t.Errorf("severity = %q, want info", found.Severity)
	}
}

func TestDetectDrift_RolledBackSkipped(t *testing.T) {
	reg, db := buildDoctorRegistry(t)

	// A rolled-back row with no registered migration should NOT appear as
	// ledger_only — this is the expected post-rollback cleanup state.
	seedApplied(t, db, LedgerEntry{
		Version:       200,
		ID:            "some-rolled-back",
		AppliedAt:     "2026-02-01T00:00:00Z",
		ContentHash:   "0000",
		OwningMission: "secrets-keychain",
		Action:        LedgerActionRolledBack,
	})

	report, err := DetectDrift(reg)
	if err != nil {
		t.Fatalf("DetectDrift: %v", err)
	}

	for _, d := range report.Drifts {
		if d.Version == 200 {
			t.Errorf("rolled-back v200 should not appear in drift report, got kind=%q", d.Kind)
		}
	}
}

func TestDetectDrift_SortedByVersion(t *testing.T) {
	reg, db := buildDoctorRegistry(t)

	// Plant three ledger-only applied entries in reverse order.
	for _, v := range []int{300, 100, 200} {
		seedApplied(t, db, LedgerEntry{
			Version:       v,
			ID:            "mig",
			AppliedAt:     "2026-01-01T00:00:00Z",
			ContentHash:   "xx",
			OwningMission: "sessions",
			Action:        LedgerActionApplied,
		})
	}

	report, err := DetectDrift(reg)
	if err != nil {
		t.Fatalf("DetectDrift: %v", err)
	}

	// Filter to the versions we planted.
	var got []int
	for _, d := range report.Drifts {
		if d.Version == 100 || d.Version == 200 || d.Version == 300 {
			got = append(got, d.Version)
		}
	}
	want := []int{100, 200, 300}
	if len(got) != len(want) {
		t.Fatalf("got %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("position %d: got v%d, want v%d", i, got[i], want[i])
		}
	}
}
