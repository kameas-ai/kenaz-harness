package rpc

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/kameas-ai/kenaz-harness/core/rpc/views/audit"
	storageview "github.com/kameas-ai/kenaz-harness/core/rpc/views/storage"
)

// fakeDriftStorageAPI is a minimal storageview.StorageAPI whose
// GetMigrationDriftReport returns a canned report or error, so
// runMigrationDriftCheck can be driven without a real *sqlite.DB.
type fakeDriftStorageAPI struct {
	report DriftReportStub
	err    error
}

// DriftReportStub mirrors storageview.DriftReport so the test file doesn't
// need to reach into unexported construction helpers.
type DriftReportStub = storageview.DriftReport

func (f *fakeDriftStorageAPI) GetMigrationDriftReport(context.Context) (storageview.DriftReport, error) {
	return f.report, f.err
}

func (f *fakeDriftStorageAPI) ApplyDriftFix(context.Context, int) error {
	panic("not used by runMigrationDriftCheck")
}

func driftEntry(version int, kind, severity string) storageview.DriftEntry {
	return storageview.DriftEntry{Version: version, Kind: kind, Severity: severity}
}

// TestRunMigrationDriftCheck_CodeOnlyIsSilent is FR-3b's acceptance test:
// code_only-only drift (the normal pending state) must push no audit row.
// Before this WP the boot handler counted len(report.Drifts) with no
// severity read at all, so this exact report would have pushed a WARN +
// audit entry indistinguishable from real ledger corruption.
//
// THIS is the FR-3b mutation proof. It drives the production
// runMigrationDriftCheck and observes the real audit surface, so
// collapsing the severity branch back to `len(report.Drifts) > 0` makes
// it fail. A sibling named ..._MutationProof used to sit below it and
// claimed the same thing while never calling production code at all —
// it re-declared the severity switch over local variables and asserted
// `len(drifts) > 0`, so it passed for any implementation, including a
// fully reverted one. Deleted 2026-08-18 in review: a test that lies
// about what it proves is worse than no test, because it spends the
// reviewer's trust.
func TestRunMigrationDriftCheck_CodeOnlyIsSilent(t *testing.T) {
	auditImpl := audit.NewAPI()
	storageAPI := &fakeDriftStorageAPI{report: storageview.DriftReport{
		Drifts: []storageview.DriftEntry{
			driftEntry(3, "code_only", "info"),
			driftEntry(4, "code_only", "info"),
		},
	}}
	a := &API{storageAPI: storageAPI, auditImpl: auditImpl}

	a.runMigrationDriftCheck(context.Background())

	entries, err := auditImpl.ListEntries(context.Background(), audit.Filter{})
	if err != nil {
		t.Fatalf("ListEntries: %v", err)
	}
	if len(entries) != 0 {
		t.Fatalf("code_only-only drift must push no audit row; got %d: %+v", len(entries), entries)
	}
}

// TestRunMigrationDriftCheck_IdMismatchPushesAuditAndPublishes covers the
// severity:"error" path: an audit row AND a broker publish on
// TopicMigrationDriftDetected with HasError=true (FR-3b + FR-3c).
func TestRunMigrationDriftCheck_IdMismatchPushesAuditAndPublishes(t *testing.T) {
	auditImpl := audit.NewAPI()
	rec := &recordingEmitter{}
	broker := NewStreamBroker(rec)
	storageAPI := &fakeDriftStorageAPI{report: storageview.DriftReport{
		Drifts: []storageview.DriftEntry{
			driftEntry(322, "id_mismatch", "error"),
		},
	}}
	a := &API{storageAPI: storageAPI, auditImpl: auditImpl, broker: broker}

	a.runMigrationDriftCheck(context.Background())

	entries, err := auditImpl.ListEntries(context.Background(), audit.Filter{})
	if err != nil {
		t.Fatalf("ListEntries: %v", err)
	}
	if len(entries) != 1 {
		t.Fatalf("want 1 audit entry, got %d: %+v", len(entries), entries)
	}
	if !strings.Contains(entries[0].Trailing, "has_error=true") {
		t.Errorf("audit entry Trailing should record has_error=true, got %q", entries[0].Trailing)
	}

	topics := rec.topics()
	if len(topics) != 1 || topics[0] != TopicMigrationDriftDetected {
		t.Fatalf("want one publish on %q, got %v", TopicMigrationDriftDetected, topics)
	}
	payload, ok := rec.calls[0].payload.(MigrationDriftDetectedPayload)
	if !ok {
		t.Fatalf("payload is %T, want MigrationDriftDetectedPayload", rec.calls[0].payload)
	}
	if !payload.HasError || payload.DriftCount != 1 || len(payload.Versions) != 1 || payload.Versions[0] != 322 {
		t.Errorf("unexpected payload: %+v", payload)
	}
}

// TestRunMigrationDriftCheck_LedgerOnlyAuditsButDoesNotPublish covers the
// severity:"warning" (ledger_only) path: an audit row, but NO broker
// publish — ledger_only is the normal shape of a downgrade or a removed
// migration (FR-3's explicit non-goal against treating it as fatal/urgent
// at boot), so it must not trigger the FR-3c toast.
func TestRunMigrationDriftCheck_LedgerOnlyAuditsButDoesNotPublish(t *testing.T) {
	auditImpl := audit.NewAPI()
	rec := &recordingEmitter{}
	broker := NewStreamBroker(rec)
	storageAPI := &fakeDriftStorageAPI{report: storageview.DriftReport{
		Drifts: []storageview.DriftEntry{
			driftEntry(999, "ledger_only", "warning"),
		},
	}}
	a := &API{storageAPI: storageAPI, auditImpl: auditImpl, broker: broker}

	a.runMigrationDriftCheck(context.Background())

	entries, err := auditImpl.ListEntries(context.Background(), audit.Filter{})
	if err != nil {
		t.Fatalf("ListEntries: %v", err)
	}
	if len(entries) != 1 {
		t.Fatalf("want 1 audit entry for ledger_only, got %d", len(entries))
	}
	if topics := rec.topics(); len(topics) != 0 {
		t.Fatalf("ledger_only must not publish %q, got %v", TopicMigrationDriftDetected, topics)
	}
}

// TestRunMigrationDriftCheck_AuditIDsAreUnique is FR-3d's acceptance test:
// two boots with an EQUAL drift count must not collide on the audit entry
// ID. The pre-fix ID was fmt.Sprintf("migration-drift-%d", len(versions)),
// which depends only on the count — two runs with count=1 always produced
// the identical ID "migration-drift-1", clashing in a UI that keys on it
// (AuditView.vue :key="e.id"). Call the check twice in immediate
// succession (the worst case for a nanosecond clock) to prove the fix
// holds even when the two runs are as close together as they can be.
func TestRunMigrationDriftCheck_AuditIDsAreUnique(t *testing.T) {
	auditImpl := audit.NewAPI()
	storageAPI := &fakeDriftStorageAPI{report: storageview.DriftReport{
		Drifts: []storageview.DriftEntry{
			driftEntry(322, "id_mismatch", "error"),
		},
	}}
	a := &API{storageAPI: storageAPI, auditImpl: auditImpl}

	a.runMigrationDriftCheck(context.Background())
	a.runMigrationDriftCheck(context.Background())

	entries, err := auditImpl.ListEntries(context.Background(), audit.Filter{})
	if err != nil {
		t.Fatalf("ListEntries: %v", err)
	}
	if len(entries) != 2 {
		t.Fatalf("want 2 audit entries (equal drift count, two runs), got %d", len(entries))
	}
	if entries[0].ID == entries[1].ID {
		t.Fatalf("two equal-count pushes produced the same audit ID %q — this is the FR-3d bug", entries[0].ID)
	}
}

// TestRunMigrationDriftCheck_DetectFailureIsNonFatalAndAudited is FR-3e's
// acceptance test: a failure to even read the drift report must (a) not
// panic or propagate — boot proceeds — and (b) push a non-fatal audit row
// so the failure leaves a trace instead of only a WARN log line nobody
// greps in real time.
func TestRunMigrationDriftCheck_DetectFailureIsNonFatalAndAudited(t *testing.T) {
	auditImpl := audit.NewAPI()
	storageAPI := &fakeDriftStorageAPI{err: errors.New("ledger read: disk I/O error")}
	a := &API{storageAPI: storageAPI, auditImpl: auditImpl}

	// The whole point of this test is that this call returns normally —
	// boot proceeds. A fatal implementation would panic or (if it were
	// wired to return an error) this test would need to assert an error
	// return; runMigrationDriftCheck returns nothing, by design, so
	// simply reaching the assertions below IS the "boot proceeds" proof.
	a.runMigrationDriftCheck(context.Background())

	entries, err := auditImpl.ListEntries(context.Background(), audit.Filter{})
	if err != nil {
		t.Fatalf("ListEntries: %v", err)
	}
	if len(entries) != 1 {
		t.Fatalf("want 1 audit entry recording the detect failure, got %d", len(entries))
	}
	if !strings.Contains(entries[0].Trailing, "detect_failed") {
		t.Errorf("audit entry should record the detect failure, got Trailing=%q", entries[0].Trailing)
	}
}

// TestRunMigrationDriftCheck_NilStorageAPIIsANoOp guards the nil-check at
// the top of runMigrationDriftCheck: SetContext only spawns the goroutine
// when a.storageAPI != nil, but the method itself must also be safe to
// call directly (as these tests do) without a storage view wired.
func TestRunMigrationDriftCheck_NilStorageAPIIsANoOp(t *testing.T) {
	a := &API{}
	a.runMigrationDriftCheck(context.Background()) // must not panic
}
