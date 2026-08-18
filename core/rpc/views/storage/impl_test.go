package storage

import (
	"context"
	"strings"
	"testing"

	corestorage "github.com/kameas-ai/kenaz-harness/core/storage"
)

// fakeMigrationRegistry is a minimal corestorage.MigrationRegistry fake:
// GetMigrationDriftReport only calls Applied() and All(), so the other
// interface methods panic if reached — a sign this test needs updating,
// not a valid code path today.
type fakeMigrationRegistry struct {
	ledger     []corestorage.LedgerEntry
	registered []corestorage.Migration
}

func (f *fakeMigrationRegistry) Register(corestorage.Migration) error {
	panic("not used by GetMigrationDriftReport")
}
func (f *fakeMigrationRegistry) All() []corestorage.Migration { return f.registered }
func (f *fakeMigrationRegistry) Applied() ([]corestorage.LedgerEntry, error) {
	return f.ledger, nil
}
func (f *fakeMigrationRegistry) Pending() ([]corestorage.Migration, error) {
	panic("not used by GetMigrationDriftReport")
}
func (f *fakeMigrationRegistry) Apply(context.Context) error {
	panic("not used by GetMigrationDriftReport")
}
func (f *fakeMigrationRegistry) Rollback(context.Context, int) error {
	panic("not used by GetMigrationDriftReport")
}

// fakeDB implements just enough of corestorage.DB to exercise
// GetMigrationDriftReport / ApplyDriftFix: only Migrations() and WriteTx()
// are ever reached by those two methods.
type fakeDB struct {
	reg     *fakeMigrationRegistry
	writeTx func(ctx context.Context, fn func(tx corestorage.WriteTx) error) error
}

func (f *fakeDB) Reader() corestorage.Reader { return nil }
func (f *fakeDB) WriteTx(ctx context.Context, fn func(tx corestorage.WriteTx) error) error {
	if f.writeTx != nil {
		return f.writeTx(ctx, fn)
	}
	return nil
}
func (f *fakeDB) Migrations() corestorage.MigrationRegistry { return f.reg }
func (f *fakeDB) VectorStore() corestorage.VectorStore      { return nil }
func (f *fakeDB) Backup() corestorage.BackupTaker           { return nil }
func (f *fakeDB) Diagnostics() corestorage.Diagnostics      { return nil }
func (f *fakeDB) SetEventSink(context.Context, corestorage.EventSink) error {
	return nil
}
func (f *fakeDB) Close(context.Context) error { return nil }

// TestGetMigrationDriftReport_DelegatesAndTranslates proves the RPC view's
// GetMigrationDriftReport is a thin translation layer over
// migrations.DetectDrift, not a second classification implementation
// (upgrade-path-coverage-01PMUG01 WP04, FR-3f). It seeds a ledger/registry
// pair that produces one entry of each Kind and asserts the wire-typed
// DriftReport this method returns carries the same Kind/Severity that
// migrations.DetectDrift assigns — i.e. that the storage.* -> migrations.*
// -> DriftReport translation round-trips without dropping or renaming
// anything.
func TestGetMigrationDriftReport_DelegatesAndTranslates(t *testing.T) {
	reg := &fakeMigrationRegistry{
		ledger: []corestorage.LedgerEntry{
			// v1: id_mismatch — ledger has "old-id", registry expects "new-id".
			{Version: 1, ID: "old-id", Action: corestorage.LedgerActionApplied},
			// v2: ledger_only — applied, no matching registered migration.
			{Version: 2, ID: "orphan-id", Action: corestorage.LedgerActionApplied},
		},
		registered: []corestorage.Migration{
			{Version: 1, ID: "new-id", OwningMission: "storage", UpSource: "CREATE TABLE t(id INTEGER);"},
			// v3: code_only — registered, never applied.
			{Version: 3, ID: "pending-id", OwningMission: "storage", UpSource: "CREATE TABLE u(id INTEGER);"},
		},
	}
	api := NewAPI(&fakeDB{reg: reg}, "")

	report, err := api.GetMigrationDriftReport(context.Background())
	if err != nil {
		t.Fatalf("GetMigrationDriftReport: %v", err)
	}
	if len(report.Drifts) != 3 {
		t.Fatalf("want 3 drift entries, got %d: %+v", len(report.Drifts), report.Drifts)
	}

	byVersion := map[int]DriftEntry{}
	for _, d := range report.Drifts {
		byVersion[d.Version] = d
	}

	v1, ok := byVersion[1]
	if !ok {
		t.Fatal("missing v1 (id_mismatch) entry")
	}
	if v1.Kind != "id_mismatch" || v1.Severity != "error" {
		t.Errorf("v1: want kind=id_mismatch severity=error, got kind=%q severity=%q", v1.Kind, v1.Severity)
	}
	if v1.LedgerID != "old-id" || v1.ExpectedID != "new-id" {
		t.Errorf("v1: want ledgerID=old-id expectedID=new-id, got ledgerID=%q expectedID=%q", v1.LedgerID, v1.ExpectedID)
	}

	v2, ok := byVersion[2]
	if !ok {
		t.Fatal("missing v2 (ledger_only) entry")
	}
	if v2.Kind != "ledger_only" || v2.Severity != "warning" {
		t.Errorf("v2: want kind=ledger_only severity=warning, got kind=%q severity=%q", v2.Kind, v2.Severity)
	}

	v3, ok := byVersion[3]
	if !ok {
		t.Fatal("missing v3 (code_only) entry")
	}
	if v3.Kind != "code_only" || v3.Severity != "info" {
		t.Errorf("v3: want kind=code_only severity=info, got kind=%q severity=%q", v3.Kind, v3.Severity)
	}
	// FR-3a: the code_only suggestion must not promise automatic
	// application — Open refuses to start on a registered-but-unapplied
	// migration since v0.63.1 (verifyFullyApplied).
	if strings.Contains(v3.Suggestion, "automatically") {
		t.Errorf("v3 suggestion still promises automatic application: %q", v3.Suggestion)
	}
	if !strings.Contains(v3.Suggestion, "refuses to start") {
		t.Errorf("v3 suggestion does not name the refuse-to-start behaviour: %q", v3.Suggestion)
	}
}
