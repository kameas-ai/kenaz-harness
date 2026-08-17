package sqlite

import (
	"context"
	"database/sql"
	"errors"
	"net/url"
	"path/filepath"
	"testing"

	"github.com/kameas-ai/kenaz-harness/core/storage"
	"github.com/kameas-ai/kenaz-harness/core/storage/migrations"

	_ "modernc.org/sqlite"
)

// newVerifyRegistry builds a Registry over a real on-disk SQLite database
// with the ledger bootstrapped. Real sqlite, not a fake: the check under
// test reads harness_migrations through the production executor.
func newVerifyRegistry(t *testing.T) (*migrations.Registry, *sql.DB) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "verify.db")
	raw, err := sql.Open("sqlite", "file:"+url.PathEscape(path)+"?_pragma=busy_timeout(5000)")
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	raw.SetMaxOpenConns(1)
	t.Cleanup(func() { _ = raw.Close() })
	exec := newExecutor(raw)
	if err := migrations.EnsureLedger(context.Background(), exec); err != nil {
		t.Fatalf("EnsureLedger: %v", err)
	}
	return migrations.NewRegistry(exec, nil, nil), raw
}

func verifyTestMig(version int, id string) migrations.Migration {
	src := "CREATE TABLE IF NOT EXISTS verify_t" + id[len(id)-3:] + " (id INTEGER)"
	return migrations.Migration{
		ID:            id,
		Version:       version,
		OwningMission: "sessions",
		UpSource:      src,
		Up: func(ctx context.Context, tx migrations.WriteTx) error {
			_, err := tx.Exec(ctx, src)
			return err
		},
	}
}

// TestVerifyFullyApplied_Clean — after a real Apply, every registered
// migration has an applied row and the boot invariant passes.
func TestVerifyFullyApplied_Clean(t *testing.T) {
	t.Parallel()
	reg, _ := newVerifyRegistry(t)
	for _, m := range []migrations.Migration{verifyTestMig(332, "sessions/0332"), verifyTestMig(333, "sessions/0333")} {
		if err := reg.Register(m); err != nil {
			t.Fatalf("Register: %v", err)
		}
	}
	if err := reg.Apply(context.Background()); err != nil {
		t.Fatalf("Apply: %v", err)
	}
	if err := verifyFullyApplied(reg); err != nil {
		t.Fatalf("verifyFullyApplied on a fully-migrated database: %v", err)
	}
}

// TestVerifyFullyApplied_CatchesSkippedMigration is the tripwire's reason to
// exist: a registered migration with no applied ledger row after Apply is a
// hard boot failure that names the versions.
//
// The check must NOT be expressible as "Pending() is empty" — a selection
// rule that wrongly believes a migration is done reports that just as
// confidently the second time you ask. This test models exactly that: the
// ledger row for 333 is absent while the code has it registered.
func TestVerifyFullyApplied_CatchesSkippedMigration(t *testing.T) {
	t.Parallel()
	reg, raw := newVerifyRegistry(t)
	for _, m := range []migrations.Migration{verifyTestMig(332, "sessions/0332"), verifyTestMig(333, "sessions/0333")} {
		if err := reg.Register(m); err != nil {
			t.Fatalf("Register: %v", err)
		}
	}
	if err := reg.Apply(context.Background()); err != nil {
		t.Fatalf("Apply: %v", err)
	}
	deleteLedgerRow(t, raw, 333)

	err := verifyFullyApplied(reg)
	if !errors.Is(err, storage.ErrMigrationFailed) {
		t.Fatalf("verifyFullyApplied = %v, want storage.ErrMigrationFailed", err)
	}
	if got := err.Error(); !contains(got, "333") {
		t.Errorf("error must name the missing version, got %q", got)
	}
}

// TestVerifyFullyApplied_RolledBackCountsAsUnapplied — a version whose most
// recent ledger row is rolled_back has no live schema change behind it, so
// the invariant must not accept it.
func TestVerifyFullyApplied_RolledBackCountsAsUnapplied(t *testing.T) {
	t.Parallel()
	reg, _ := newVerifyRegistry(t)
	m := verifyTestMig(332, "sessions/0332")
	m.Down = func(ctx context.Context, tx migrations.WriteTx) error { return nil }
	if err := reg.Register(m); err != nil {
		t.Fatalf("Register: %v", err)
	}
	ctx := context.Background()
	if err := reg.Apply(ctx); err != nil {
		t.Fatalf("Apply: %v", err)
	}
	if err := verifyFullyApplied(reg); err != nil {
		t.Fatalf("verifyFullyApplied after Apply: %v", err)
	}
	if err := reg.Rollback(ctx, 0); err != nil {
		t.Fatalf("Rollback: %v", err)
	}
	if err := verifyFullyApplied(reg); !errors.Is(err, storage.ErrMigrationFailed) {
		t.Fatalf("verifyFullyApplied after Rollback = %v, want storage.ErrMigrationFailed", err)
	}
}

// deleteLedgerRow removes a ledger row behind the runner's back, modelling
// the on-disk shape a broken selection rule leaves: the code is registered,
// the schema change never landed, and nothing in harness_migrations says so.
func deleteLedgerRow(t *testing.T, raw *sql.DB, version int) {
	t.Helper()
	if _, err := raw.ExecContext(context.Background(),
		"DELETE FROM harness_migrations WHERE version=?", version); err != nil {
		t.Fatalf("delete ledger row v%d: %v", version, err)
	}
}

func contains(s, sub string) bool {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}
