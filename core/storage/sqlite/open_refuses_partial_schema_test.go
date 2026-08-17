package sqlite_test

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/kameas-ai/kenaz-harness/core/storage"
	storagesqlite "github.com/kameas-ai/kenaz-harness/core/storage/sqlite"

	_ "modernc.org/sqlite"
)

// TestOpen_RefusesWhenAMigrationDidNotApply pins the WIRING of the post-apply
// invariant, not just the function behind it.
//
// verify_fully_applied_test.go proves verifyFullyApplied() computes the right
// answer. It says nothing about whether Open consults it — remove the call
// site from Open and every other test in this repository still passes
// (verified by mutation). That is precisely the failure class the v0.63.0 P0
// belonged to: a check that is never reached is a check that does not exist,
// and the boot-time drift detector in core/rpc/api.go DID notice this exact
// database, in a goroutine, after the UI had rendered, and logged a warning.
//
// The database below is one Apply cannot fix and does not know it cannot fix.
// An AFTER INSERT trigger removes the ledger row for sessions/0334 as it is
// written, so the migration's transaction commits, Apply returns nil, and the
// on-disk ledger still has no applied row for 334 — the same shape a broken
// selection rule leaves behind. Only the post-apply invariant is positioned to
// see it, and the user only benefits if Open refuses to hand back a database
// whose schema the code was not compiled against.
func TestOpen_RefusesWhenAMigrationDidNotApply(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	dir := t.TempDir()

	first, err := storagesqlite.Open(newConfig(dir))
	if err != nil {
		t.Fatalf("initial Open: %v", err)
	}
	if err := first.Close(ctx); err != nil {
		t.Fatalf("close: %v", err)
	}

	// Control: a database that CAN heal must still open. 0334's Up is guarded
	// by pragma_table_info probes, so deleting only its ledger row leaves a
	// re-apply that is a no-op and rewrites the row.
	raw := openRaw(t, dir)
	if _, err := raw.ExecContext(ctx, "DELETE FROM harness_migrations WHERE version = 334"); err != nil {
		t.Fatalf("delete ledger row: %v", err)
	}
	if err := raw.Close(); err != nil {
		t.Fatalf("close raw: %v", err)
	}
	healed, err := storagesqlite.Open(newConfig(dir))
	if err != nil {
		t.Fatalf("Open on a self-healing database must succeed, got: %v", err)
	}
	if err := healed.Close(ctx); err != nil {
		t.Fatalf("close: %v", err)
	}

	// The state the invariant exists for: Apply reports success and the ledger
	// row is not there afterwards.
	raw = openRaw(t, dir)
	stmts := []string{
		`CREATE TRIGGER strip_0334 AFTER INSERT ON harness_migrations
         WHEN new.version = 334 BEGIN
             DELETE FROM harness_migrations WHERE rowid = new.rowid;
         END`,
		"DELETE FROM harness_migrations WHERE version = 334",
	}
	for _, s := range stmts {
		if _, err := raw.ExecContext(ctx, s); err != nil {
			t.Fatalf("seed %q: %v", s, err)
		}
	}
	if err := raw.Close(); err != nil {
		t.Fatalf("close raw: %v", err)
	}

	db, err := storagesqlite.Open(newConfig(dir))
	if err == nil {
		_ = db.Close(ctx)
		t.Fatal("Open must refuse a database whose schema the code did not compile against")
	}
	if !errors.Is(err, storage.ErrMigrationFailed) {
		t.Fatalf("Open error = %v, want storage.ErrMigrationFailed", err)
	}
	if !strings.Contains(err.Error(), "334") {
		t.Errorf("Open error must name the version, got %q", err)
	}
}
