package sqlite_test

// upgrade_last_usage_read_test.go — chat-turn-integrity-01PMZ606 WP11,
// AC-004-relative / AC-PI-1 (spec.md §5.7 WP11 test bullet 3, §11):
// "boot from testdata/upgrade/v0.64.0/ — 0322 is an applied migration
// and the column exists in the snapshot; assert a snapshot session with
// a populated column decodes."
//
// Per CLAUDE.md blind spot #3 (upgrade-path-untested), a migration or
// read path that has only ever run against a database HEAD created from
// scratch is not proven — the migration high-water mark starts at 0 on
// an empty database and everything applies in one ascending pass, which
// cannot exercise "does the READ path work against a column a PREVIOUS
// RELEASE'S schema already carries." This test boots the newest
// committed snapshot (a previous release's real, materialized database)
// via the SAME upgradesnap.Materialize / storagesqlite.Open path
// TestUpgradePath uses (upgrade_path_test.go) rather than hand-rolling a
// second materialiser, per spec.md §8 rule 2.

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/kameas-ai/kenaz-harness/core/rpc/views/sessions"
	"github.com/kameas-ai/kenaz-harness/core/session"
	storagesqlite "github.com/kameas-ai/kenaz-harness/core/storage/sqlite"
	"github.com/kameas-ai/kenaz-harness/core/storage/sqlite/upgradesnap"

	_ "modernc.org/sqlite"
)

// newestUpgradeSnapshotTag is the newest committed snapshot as of this
// WP (v0.64.0 in the mission's own spec text is stale — the base this
// mission actually landed on already carries v0.65.0 through v0.72.0).
// check-upgrade-snapshot-present.sh (wired into pr.yml) fails the build
// whenever the committed chain falls behind the newest git tag, so this
// literal does not silently drift unnoticed; bumping it is expected
// release-ritual maintenance, not a bug in this test.
const newestUpgradeSnapshotTag = "v0.72.0"

// TestGet_LastUsage_SurvivesReload_AcrossUpgrade is AC-004-relative's
// WP11 half: session.Manager.GetLastUsage and the rpc Get() read path
// (core/rpc/views/sessions/impl.go, wired this WP) must work against a
// database a PREVIOUS RELEASE produced, not only a schema HEAD's own
// migrations created fresh. Migration sessions/0322-last-usage-json
// shipped long before v0.72.0, so Open() applies zero pending migrations
// here — everything this test exercises is the read/write path against
// an ALREADY-upgraded column.
func TestGet_LastUsage_SurvivesReload_AcrossUpgrade(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	dir := t.TempDir()

	dumpText, err := os.ReadFile(filepath.Join("testdata", "upgrade", newestUpgradeSnapshotTag, "dump.sql"))
	if err != nil {
		t.Fatalf("read dump.sql: %v", err)
	}
	rawPath := filepath.Join(dir, "data.db")
	raw := openRawSQLiteAt(t, rawPath)
	if err := upgradesnap.Materialize(ctx, raw, string(dumpText)); err != nil {
		t.Fatalf("materialise snapshot: %v", err)
	}
	if err := raw.Close(); err != nil {
		t.Fatalf("close raw after materialise: %v", err)
	}

	db, err := storagesqlite.Open(newConfig(dir))
	if err != nil {
		t.Fatalf("Open on the %s snapshot failed: %v", newestUpgradeSnapshotTag, err)
	}
	t.Cleanup(func() { _ = db.Close(context.Background()) })

	pending, err := db.Migrations().Pending()
	if err != nil {
		t.Fatalf("Pending: %v", err)
	}
	if len(pending) != 0 {
		t.Fatalf("Pending() after Open = %v, want none — the %s snapshot should already carry every migration up to HEAD's registry", pending, newestUpgradeSnapshotTag)
	}

	mgr := session.NewManager(session.NewSQLStore(session.NewStorageDB(db)))
	api := sessions.NewManagerAPI(mgr)

	// "seed-session-1" is one of the two sessions baked into the
	// committed snapshot. Both carry last_usage_json = NULL in the dump
	// (verified: `grep last_usage_json testdata/upgrade/v0.72.0/dump.sql`
	// shows NULL on both seeded INSERT rows) — i.e. a session that
	// existed BEFORE the upgrade and has never had a turn since. Setting
	// LastUsage on it and reading it back through Get() is exactly what
	// "a snapshot session with a populated column decodes" means: the
	// write path AND the WP11 read path both function against the real
	// upgraded schema, not a schema HEAD created from scratch.
	const seedSessionID = "seed-session-1"
	want := session.LastUsage{
		PromptTokens:     2048,
		CompletionTokens: 256,
		TotalTokens:      2304,
		CostUSD:          0.0117,
		CostSource:       "provider",
	}
	if err := mgr.SetLastUsage(ctx, seedSessionID, want); err != nil {
		t.Fatalf("SetLastUsage on upgraded schema: %v", err)
	}

	got, err := api.Get(ctx, seedSessionID)
	if err != nil {
		t.Fatalf("Get on upgraded schema: %v", err)
	}
	if got.LastUsage == nil {
		t.Fatal("Get on the upgraded snapshot: LastUsage is nil; want the persisted snapshot to decode. " +
			"This is the CHAT-turn-integrity WP11 defect surfacing on a REAL upgraded database, not just a fresh one.")
	}
	wantWire := sessions.LastUsage{
		PromptTokens:     want.PromptTokens,
		CompletionTokens: want.CompletionTokens,
		TotalTokens:      want.TotalTokens,
		CostUSD:          want.CostUSD,
		CostSource:       want.CostSource,
	}
	if *got.LastUsage != wantWire {
		t.Errorf("Get LastUsage on upgraded schema = %+v, want %+v", *got.LastUsage, wantWire)
	}
}
