package log_test

import (
	"context"
	"testing"

	eventlog "github.com/kameas-ai/kenaz-harness/core/event/log"
	"github.com/kameas-ai/kenaz-harness/core/storage"
	storagesqlite "github.com/kameas-ai/kenaz-harness/core/storage/sqlite"
)

// openTestDB is openTestBackend's sibling, returning the raw storage.DB
// ReadRetentionPolicy/WriteRetentionPolicy need (they read/write a
// second table, retention_config, not just events).
func openTestDB(t *testing.T) storage.DB {
	t.Helper()
	cfg := storage.Config{
		DataDir:          t.TempDir(),
		EncryptionStatus: storage.EncryptionStatusDisabledWithDiskEncryption,
	}
	db, err := storagesqlite.Open(cfg)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() { _ = db.Close(context.Background()) })
	return db
}

// TestReadRetentionPolicy_SeededDefault_DegradesSafely proves E-003's
// safety property against the SHIPPED, real migration seed: a freshly
// migrated database (event-log/0103's actual seed row, BEFORE 106's
// fix runs — this asserts ReadRetentionPolicy's own defence, not that
// 106 already patched it) must never be read as anything other than
// RetentionKeepForever, even though the seeded JSON's "kind" value is
// not a real RetentionStrategy.
func TestReadRetentionPolicy_SeededDefault_DegradesSafely(t *testing.T) {
	db := openTestDB(t)
	ctx := context.Background()

	// Registration + Open already applied 100-106 (production Open path)
	// — 106 fixes the seed. Force the pre-106 seed back to prove
	// ReadRetentionPolicy's OWN defence independent of 106 having run,
	// simulating a hypothetical future bad value (a downgrade, manual
	// DB surgery, or a value 106 does not anticipate) reaching the row.
	if err := db.WriteTx(ctx, func(tx storage.WriteTx) error {
		_, err := tx.Exec(ctx, `UPDATE retention_config SET policy = '{"kind":"keep_all"}' WHERE version = 1`)
		return err
	}); err != nil {
		t.Fatalf("force bad seed: %v", err)
	}

	got, err := eventlog.ReadRetentionPolicy(ctx, db)
	if err != nil {
		t.Fatalf("ReadRetentionPolicy: %v", err)
	}
	if got.Kind != eventlog.RetentionKeepForever {
		t.Errorf("Kind = %q, want %q (E-003: never silently start deleting on an unrecognised value)",
			got.Kind, eventlog.RetentionKeepForever)
	}
}

// TestReadRetentionPolicy_AfterMigration106_ReadsTheFixedSeed proves
// migration 106's own UPDATE reaches ReadRetentionPolicy: a database
// opened fresh (106 runs as part of Open, before this read) must report
// keep_forever WITHOUT this test having forced anything — the migration
// fixed it.
func TestReadRetentionPolicy_AfterMigration106_ReadsTheFixedSeed(t *testing.T) {
	db := openTestDB(t)
	got, err := eventlog.ReadRetentionPolicy(context.Background(), db)
	if err != nil {
		t.Fatalf("ReadRetentionPolicy: %v", err)
	}
	if got.Kind != eventlog.RetentionKeepForever {
		t.Errorf("Kind = %q, want %q (migration 106 must have fixed the shipped '\"kind\":\"keep_all\"' seed)",
			got.Kind, eventlog.RetentionKeepForever)
	}
}

// TestWriteReadRetentionPolicy_RoundTrip proves the write path: set a
// real strategy + window, reopen semantics not required here (that is
// AC-013, exercised at the RPC layer) — this is the direct unit-level
// round trip through the SAME functions AC-013 depends on.
func TestWriteReadRetentionPolicy_RoundTrip(t *testing.T) {
	db := openTestDB(t)
	ctx := context.Background()

	want := eventlog.PersistedPolicy{Kind: eventlog.RetentionDeleteAfterWindow, WindowDays: 45}
	if err := eventlog.WriteRetentionPolicy(ctx, db, want); err != nil {
		t.Fatalf("WriteRetentionPolicy: %v", err)
	}
	got, err := eventlog.ReadRetentionPolicy(ctx, db)
	if err != nil {
		t.Fatalf("ReadRetentionPolicy: %v", err)
	}
	if got != want {
		t.Errorf("round trip = %+v, want %+v", got, want)
	}

	// A second write (upsert, not a duplicate row) — the table has a
	// single versioned row, not an append log.
	want2 := eventlog.PersistedPolicy{Kind: eventlog.RetentionKeepForever}
	if err := eventlog.WriteRetentionPolicy(ctx, db, want2); err != nil {
		t.Fatalf("WriteRetentionPolicy (second): %v", err)
	}
	got2, err := eventlog.ReadRetentionPolicy(ctx, db)
	if err != nil {
		t.Fatalf("ReadRetentionPolicy (second): %v", err)
	}
	if got2 != want2 {
		t.Errorf("second round trip = %+v, want %+v", got2, want2)
	}
	var n int
	if err := db.Reader().QueryRow(ctx, "SELECT COUNT(*) FROM retention_config").Scan(&n); err != nil {
		t.Fatalf("count: %v", err)
	}
	if n != 1 {
		t.Errorf("retention_config row count = %d, want 1 (upsert, not append)", n)
	}
}

// TestReadRetentionPolicy_MalformedJSON_DegradesSafely covers the other
// ReadRetentionPolicy failure mode: a policy column that is not even
// valid JSON.
func TestReadRetentionPolicy_MalformedJSON_DegradesSafely(t *testing.T) {
	db := openTestDB(t)
	ctx := context.Background()
	if err := db.WriteTx(ctx, func(tx storage.WriteTx) error {
		_, err := tx.Exec(ctx, `UPDATE retention_config SET policy = 'not json at all' WHERE version = 1`)
		return err
	}); err != nil {
		t.Fatalf("force malformed policy: %v", err)
	}
	got, err := eventlog.ReadRetentionPolicy(ctx, db)
	if err != nil {
		t.Fatalf("ReadRetentionPolicy: %v", err)
	}
	if got.Kind != eventlog.RetentionKeepForever {
		t.Errorf("Kind = %q, want %q", got.Kind, eventlog.RetentionKeepForever)
	}
}
