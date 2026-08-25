package capabilities_test

// cache_sqlite_upgrade_test.go — model-settings-reach-the-model-01PMZ101
// WP14, AC-017(a) + AC-PI-1. This is the load-bearing persistence
// assertion for the SQLite CapabilityCache backend: it boots from
// core/storage/sqlite/testdata/upgrade/v0.70.0/ (the newest committed
// snapshot in this tree as of this WP — see this WP's report for the
// v0.71.0 gap, which is a pre-existing release-ritual item this WP did
// not cause), never `Open` on an empty directory (CLAUDE.md blind spot
// #3 / WP-PI template AC-PI-1). provider_capabilities is present in
// that snapshot's schema but has never had a row written to it by any
// production code before this WP — this test is what first exercises
// that path against a database a previous release actually produced.
//
// Materialisation follows core/storage/sqlite/upgrade_path_test.go's
// own pattern (upgradesnap.Materialize + storagesqlite.Open) rather
// than hand-rolling a second one, per that file's own header comment.

import (
	"context"
	"database/sql"
	"net/url"
	"os"
	"path/filepath"
	"testing"

	llm "github.com/kameas-ai/kenaz-harness/core/llm"
	"github.com/kameas-ai/kenaz-harness/core/llm/capabilities"
	"github.com/kameas-ai/kenaz-harness/core/storage"
	storagesqlite "github.com/kameas-ai/kenaz-harness/core/storage/sqlite"
	"github.com/kameas-ai/kenaz-harness/core/storage/sqlite/upgradesnap"
)

const wp14SnapshotTag = "v0.70.0"

func wp14OpenUpgradedDB(t *testing.T) storage.DB {
	t.Helper()
	ctx := context.Background()
	dir := t.TempDir()

	dumpPath := filepath.Join("..", "..", "storage", "sqlite", "testdata", "upgrade", wp14SnapshotTag, "dump.sql")
	dumpText, err := os.ReadFile(dumpPath)
	if err != nil {
		t.Fatalf("read %s: %v (AC-PI-1 fixture missing)", dumpPath, err)
	}

	rawPath := filepath.Join(dir, "data.db")
	dsn := "file:" + url.PathEscape(rawPath) + "?_pragma=foreign_keys(1)&_pragma=busy_timeout(5000)"
	raw, err := sql.Open("sqlite", dsn)
	if err != nil {
		t.Fatalf("open raw sqlite: %v", err)
	}
	raw.SetMaxOpenConns(1)
	if err := upgradesnap.Materialize(ctx, raw, string(dumpText)); err != nil {
		t.Fatalf("materialise %s snapshot: %v", wp14SnapshotTag, err)
	}
	if err := raw.Close(); err != nil {
		t.Fatalf("close raw after materialise: %v", err)
	}

	db, err := storagesqlite.Open(storage.Config{
		DataDir:          dir,
		EncryptionStatus: storage.EncryptionStatusDisabledWithDiskEncryption,
	})
	if err != nil {
		t.Fatalf("Open on the %s snapshot failed: %v", wp14SnapshotTag, err)
	}
	t.Cleanup(func() { _ = db.Close(context.Background()) })
	return db
}

// TestSQLiteCache_WritesToRealProviderCapabilitiesTable is AC-017(a)'s
// own stated failure mode: "fails if the assertion is cache-agnostic —
// DefaultCache() returns a MemoryCache for 'sqlite' today, so the test
// passes with no backend written at all. Assert the row is in sqlite."
// This test reads the row back through a SEPARATE raw connection to
// the same file, not through SQLiteCache.Get, so a Put that only wrote
// to an in-memory fallback cannot make it pass.
func TestSQLiteCache_WritesToRealProviderCapabilitiesTable(t *testing.T) {
	db := wp14OpenUpgradedDB(t)
	cache := capabilities.NewSQLiteCache(db)

	caps := llm.ProviderCapabilities{
		Provider:    "ollama",
		Model:       "llama3.1",
		Streaming:   true,
		ToolCalling: true,
	}
	if err := cache.Put(context.Background(), "wp14-profile", "llama3.1", caps); err != nil {
		t.Fatalf("Put: %v", err)
	}

	row := db.Reader().QueryRow(context.Background(),
		`SELECT flags_json, capability_schema_version FROM provider_capabilities WHERE profile_id = ? AND model_id = ?`,
		"wp14-profile", "llama3.1",
	)
	var flagsJSON string
	var version int
	if err := row.Scan(&flagsJSON, &version); err != nil {
		t.Fatalf("row not found via a raw query against provider_capabilities — Put did not reach real sqlite: %v", err)
	}
	if version != llm.CapabilitySchemaVersion {
		t.Errorf("capability_schema_version = %d, want %d", version, llm.CapabilitySchemaVersion)
	}
	if flagsJSON == "" {
		t.Error("flags_json is empty")
	}
}

// TestSQLiteCache_GetRoundTripsThroughRealSQLite drives Put then Get
// through SQLiteCache itself (not a raw query) against the upgraded
// database, and via a SECOND SQLiteCache instance backed by the SAME
// on-disk file after closing and reopening — proving the record
// survived on disk, not merely in a driver-level statement cache.
func TestSQLiteCache_GetRoundTripsThroughRealSQLite(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	dumpPath := filepath.Join("..", "..", "storage", "sqlite", "testdata", "upgrade", wp14SnapshotTag, "dump.sql")
	dumpText, err := os.ReadFile(dumpPath)
	if err != nil {
		t.Fatalf("read dump.sql: %v", err)
	}
	rawPath := filepath.Join(dir, "data.db")
	dsn := "file:" + url.PathEscape(rawPath) + "?_pragma=foreign_keys(1)&_pragma=busy_timeout(5000)"
	raw, err := sql.Open("sqlite", dsn)
	if err != nil {
		t.Fatalf("open raw sqlite: %v", err)
	}
	raw.SetMaxOpenConns(1)
	if err := upgradesnap.Materialize(ctx, raw, string(dumpText)); err != nil {
		t.Fatalf("materialise snapshot: %v", err)
	}
	if err := raw.Close(); err != nil {
		t.Fatalf("close raw: %v", err)
	}

	cfg := storage.Config{DataDir: dir, EncryptionStatus: storage.EncryptionStatusDisabledWithDiskEncryption}

	db1, err := storagesqlite.Open(cfg)
	if err != nil {
		t.Fatalf("first Open: %v", err)
	}
	cache1 := capabilities.NewSQLiteCache(db1)
	want := llm.ProviderCapabilities{Provider: "ollama", Model: "llava", Vision: true, ToolCalling: false}
	if err := cache1.Put(ctx, "wp14-p2", "llava", want); err != nil {
		t.Fatalf("Put: %v", err)
	}
	if err := db1.Close(ctx); err != nil {
		t.Fatalf("close db1: %v", err)
	}

	db2, err := storagesqlite.Open(cfg)
	if err != nil {
		t.Fatalf("second Open: %v", err)
	}
	t.Cleanup(func() { _ = db2.Close(context.Background()) })
	cache2 := capabilities.NewSQLiteCache(db2)

	got, ok := cache2.Get(ctx, "wp14-p2", "llava")
	if !ok {
		t.Fatal("Get after reopen: cache miss — the record did not survive the close/reopen, so it never reached disk")
	}
	if !got.Vision || got.ToolCalling {
		t.Errorf("Get after reopen = %+v, want Vision=true ToolCalling=false", got)
	}
}

// TestDefaultCache_SqliteEnvSelectsRealSQLiteBackend is R-4's own
// stated failure mode, driven end to end: "sqlite" falls to
// MemoryCache today. This test sets HARNESS_LLM_CAPABILITY_CACHE=sqlite,
// calls DefaultCache with a REAL storage.DB (the upgraded snapshot),
// Puts through whatever DefaultCache returned, and reads the row back
// via a raw query — so a regression back to the pre-WP14 shape (silent
// MemoryCache fallback) fails this test even though DefaultCache's
// return type is the CapabilityCache interface, not a concrete type a
// naive type-assertion could dodge.
func TestDefaultCache_SqliteEnvSelectsRealSQLiteBackend(t *testing.T) {
	t.Setenv(capabilities.EnvCapabilityCache, "sqlite")
	db := wp14OpenUpgradedDB(t)
	cache := capabilities.DefaultCache(db)

	if err := cache.Put(context.Background(), "wp14-defaultcache", "m", llm.ProviderCapabilities{Provider: "ollama", Streaming: true}); err != nil {
		t.Fatalf("Put: %v", err)
	}
	row := db.Reader().QueryRow(context.Background(),
		`SELECT flags_json FROM provider_capabilities WHERE profile_id = ? AND model_id = ?`,
		"wp14-defaultcache", "m",
	)
	var flagsJSON string
	if err := row.Scan(&flagsJSON); err != nil {
		t.Fatalf("DefaultCache(db) with HARNESS_LLM_CAPABILITY_CACHE=sqlite did not write to real sqlite — "+
			"it is returning a MemoryCache (the pre-WP14 regression): %v", err)
	}
}

// TestDefaultCache_SqliteEnvWithNilDBDegradesToMemory covers the
// documented degrade path: "sqlite" with no DB handle available (e.g.
// the nil-core test chassis) must not panic — it falls back to
// MemoryCache, which this test proves by using the cache successfully
// (a nil-embedding SQLiteCache would panic on first Reader() call).
func TestDefaultCache_SqliteEnvWithNilDBDegradesToMemory(t *testing.T) {
	t.Setenv(capabilities.EnvCapabilityCache, "sqlite")
	cache := capabilities.DefaultCache(nil)
	if err := cache.Put(context.Background(), "p", "m", llm.ProviderCapabilities{Streaming: true}); err != nil {
		t.Fatalf("Put on the nil-db degrade path: %v", err)
	}
	if _, ok := cache.Get(context.Background(), "p", "m"); !ok {
		t.Fatal("expected a cache hit from the MemoryCache degrade path")
	}
}

// TestDefaultCache_OffReturnsNullCache and
// TestDefaultCache_UnsetReturnsUsableCache round out DefaultCache's
// other two branches, which R-4 didn't touch but which a future edit
// to this switch could still regress silently without some coverage
// here.
func TestDefaultCache_OffReturnsNullCache(t *testing.T) {
	t.Setenv(capabilities.EnvCapabilityCache, "off")
	cache := capabilities.DefaultCache(nil)
	_ = cache.Put(context.Background(), "p", "m", llm.ProviderCapabilities{Streaming: true})
	if _, ok := cache.Get(context.Background(), "p", "m"); ok {
		t.Fatal("HARNESS_LLM_CAPABILITY_CACHE=off must always miss")
	}
}

func TestDefaultCache_UnsetReturnsUsableCache(t *testing.T) {
	cache := capabilities.DefaultCache(nil)
	if err := cache.Put(context.Background(), "p", "m", llm.ProviderCapabilities{Streaming: true}); err != nil {
		t.Fatalf("Put: %v", err)
	}
	if _, ok := cache.Get(context.Background(), "p", "m"); !ok {
		t.Fatal("expected a cache hit with HARNESS_LLM_CAPABILITY_CACHE unset")
	}
}

// TestSQLiteCache_InvalidateOnRealSQLite exercises Invalidate against
// the upgraded database, asserting it removes only the target
// profile's rows.
func TestSQLiteCache_InvalidateOnRealSQLite(t *testing.T) {
	db := wp14OpenUpgradedDB(t)
	cache := capabilities.NewSQLiteCache(db)
	ctx := context.Background()

	_ = cache.Put(ctx, "wp14-inv-a", "m1", llm.ProviderCapabilities{Provider: "ollama", Model: "m1"})
	_ = cache.Put(ctx, "wp14-inv-a", "m2", llm.ProviderCapabilities{Provider: "ollama", Model: "m2"})
	_ = cache.Put(ctx, "wp14-inv-b", "m1", llm.ProviderCapabilities{Provider: "ollama", Model: "m1"})

	if err := cache.Invalidate(ctx, "wp14-inv-a"); err != nil {
		t.Fatalf("Invalidate: %v", err)
	}
	if _, ok := cache.Get(ctx, "wp14-inv-a", "m1"); ok {
		t.Error("wp14-inv-a/m1 should be invalidated")
	}
	if _, ok := cache.Get(ctx, "wp14-inv-a", "m2"); ok {
		t.Error("wp14-inv-a/m2 should be invalidated")
	}
	if _, ok := cache.Get(ctx, "wp14-inv-b", "m1"); !ok {
		t.Error("wp14-inv-b/m1 should survive invalidation of wp14-inv-a")
	}
}
