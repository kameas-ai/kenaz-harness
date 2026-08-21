package sqlite_test

// wp21_test.go — controls-and-readouts-that-tell-the-truth-01PMZ808
// UNIT-16 / WP21 (FR-030). AC-054 and AC-055.

import (
	"context"
	"database/sql"
	"testing"

	"github.com/kameas-ai/kenaz-harness/core/storage"
	storagesqlite "github.com/kameas-ai/kenaz-harness/core/storage/sqlite"
)

// sqlHandle mirrors the structural interface core/rpc/api.go and
// core/trust/anchor_sqlite.go already use to reach the raw *sql.DB
// without storage.DB itself growing a public SQL() method.
type sqlHandle interface{ SQL() *sql.DB }

func pragma(t *testing.T, db storage.DB, name string) string {
	t.Helper()
	raw := db.(sqlHandle).SQL()
	var v string
	if err := raw.QueryRow("PRAGMA " + name).Scan(&v); err != nil {
		t.Fatalf("PRAGMA %s: %v", name, err)
	}
	return v
}

// TestConfig_WAL_False_OpensWithoutWAL is AC-054.
//
// Mutation: restore the literal "journal_mode(WAL)" DSN fragment (drop
// the cfg.WAL conditional). Must fail — journal_mode would read "wal"
// regardless of the Config value.
func TestConfig_WAL_False_OpensWithoutWAL(t *testing.T) {
	dir := t.TempDir()
	f := false
	cfg := storage.Config{
		DataDir:          dir,
		EncryptionStatus: storage.EncryptionStatusDisabledWithDiskEncryption,
		WAL:              &f,
	}
	db := mustOpenCfg(t, cfg)
	got := pragma(t, db, "journal_mode")
	if got == "wal" {
		t.Fatalf("journal_mode = %q with Config.WAL=false, want anything but wal", got)
	}
}

// TestConfig_WAL_Default_OpensWithWAL is the companion positive case:
// the zero-value / unset Config.WAL must still default to WAL (the
// documented default), proving the wire didn't flip the fallback.
func TestConfig_WAL_Default_OpensWithWAL(t *testing.T) {
	dir := t.TempDir()
	db := mustOpen(t, dir)
	got := pragma(t, db, "journal_mode")
	if got != "wal" {
		t.Fatalf("journal_mode = %q with default Config, want wal", got)
	}
}

// TestConfig_ForeignKeys_False_StillEnforced is AC-055 — asserting the
// INVARIANT, not the dial. Config.ForeignKeys is deliberately never
// honoured (spec R-12 / D-6): the 0327/0332 scratch-table rebuild
// migrations depend on foreign_keys=1 being unconditional
// (migration_0327_test.go, artifacts_rebuild_test.go both assert "the
// production DSN always sets foreign_keys(1)"). Honouring
// ForeignKeys=false would open a supported one-field path to run those
// rebuilds without the CASCADE they depend on — the exact hazard
// docs/unwired-ledger.md records as having already destroyed user data
// unrecoverably on schema <=326 installs before the v0.63.1 repair.
//
// This test exists so that a future "helpful" wire of ForeignKeys (the
// obvious-looking, wrong fix) fails loudly instead of silently
// re-opening that hazard.
func TestConfig_ForeignKeys_False_StillEnforced(t *testing.T) {
	dir := t.TempDir()
	f := false
	cfg := storage.Config{
		DataDir:          dir,
		EncryptionStatus: storage.EncryptionStatusDisabledWithDiskEncryption,
		ForeignKeys:      &f,
	}
	db := mustOpenCfg(t, cfg)
	got := pragma(t, db, "foreign_keys")
	if got != "1" {
		t.Fatalf("foreign_keys = %q with Config.ForeignKeys=false, want 1 (unconditional invariant — see D-6)", got)
	}
}

// mustOpenCfg is mustOpen's sibling for callers that need a non-default
// Config (mustOpen in sqlite_test.go always builds its own).
func mustOpenCfg(t *testing.T, cfg storage.Config) storage.DB {
	t.Helper()
	db, err := storagesqlite.Open(cfg)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() { _ = db.Close(context.Background()) })
	return db
}
