package sqlite_test

// upgrade_knobs_default_test.go — model-settings-reach-the-model-01PMZ101
// UNIT-6 / WP10, AC-009 (spec.md "from
// core/storage/sqlite/testdata/upgrade/v0.64.0/ ... write the column,
// start a turn, and assert the GenerationRequest the adapter receives
// carries the stored Knobs").
//
// Per CLAUDE.md blind spot #3 (upgrade-path-untested) and spec §8 rules 1
// and 2, this drives REAL sqlite (storagesqlite.Open + session.NewSQLStore)
// against a database a PREVIOUS RELEASE produced — never
// session.NewMemoryStore, never Open() on an empty directory. Migration
// sessions/0330-knobs shipped long before the newest committed snapshot
// (verified: `knobs_default` is a projected column in every INSERT INTO
// "sessions" row in testdata/upgrade/v0.78.1/dump.sql), so Open() here
// applies zero pending migrations — this test exercises the read/write
// path against an ALREADY-upgraded column, exactly the case that is
// structurally invisible to a fresh-database test.
//
// This file covers the STORE half (Get/SetKnobsDefault round-trip on the
// upgraded schema). The SEND-PATH half — that the stored value actually
// reaches GenerationRequest.Knobs — is covered by
// core/rpc/views/agentgraph/chat's TestGenerate_MergesSessionKnobsDefault,
// which is not persistence-bearing (it drives an in-memory fake resolver,
// per spec §8 rule 3: a pure seam-boundary unit has no SQL to bypass) and
// so lives with the adapter it tests rather than here.

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/kameas-ai/kenaz-harness/core/llm"
	"github.com/kameas-ai/kenaz-harness/core/session"
	storagesqlite "github.com/kameas-ai/kenaz-harness/core/storage/sqlite"
	"github.com/kameas-ai/kenaz-harness/core/storage/sqlite/upgradesnap"

	_ "modernc.org/sqlite"
)

// knobsDefaultUpgradeSnapshotTag is the newest committed snapshot as of
// this WP. check-upgrade-snapshot-present.sh (wired into pr.yml) fails
// the build whenever the committed chain falls behind the newest git
// tag, so this literal does not silently drift unnoticed; bumping it is
// expected release-ritual maintenance, not a bug in this test.
const knobsDefaultUpgradeSnapshotTag = "v0.78.1"

// TestKnobsDefault_RoundTrips_AcrossUpgrade is AC-009's persistence half:
// session.Manager.{Get,Set}KnobsDefault must work against a database a
// PREVIOUS RELEASE produced, not only a schema HEAD's own migrations
// created fresh. Migration sessions/0330-knobs shipped well before
// v0.78.1, so Open() applies zero pending migrations here — this
// exercises the read/write path against an already-upgraded column.
func TestKnobsDefault_RoundTrips_AcrossUpgrade(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	dir := t.TempDir()

	dumpText, err := os.ReadFile(filepath.Join("testdata", "upgrade", knobsDefaultUpgradeSnapshotTag, "dump.sql"))
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
		t.Fatalf("Open on the %s snapshot failed: %v", knobsDefaultUpgradeSnapshotTag, err)
	}
	t.Cleanup(func() { _ = db.Close(context.Background()) })

	pending, err := db.Migrations().Pending()
	if err != nil {
		t.Fatalf("Pending: %v", err)
	}
	if len(pending) != 0 {
		t.Fatalf("Pending() after Open = %v, want none — the %s snapshot should already carry every migration up to HEAD's registry", pending, knobsDefaultUpgradeSnapshotTag)
	}

	mgr := session.NewManager(session.NewSQLStore(session.NewStorageDB(db)))

	// "seed-session-1" is baked into the committed snapshot with
	// knobs_default = NULL (verified: it is the last projected column in
	// the seeded INSERT rows and carries no non-NULL trailer) — i.e. a
	// session that existed BEFORE this feature and has never had the
	// tune panel opened. This is exactly the upgrade case AC-009 exists
	// to prove: the column, the migration, and the read/write path all
	// function against a real previously-shipped row, not a fixture this
	// binary created from scratch.
	const seedSessionID = "seed-session-1"

	// Before any Set, GetKnobsDefault must report "no override" (nil),
	// not an error and not a zero-value struct that looks like a real
	// (if empty) override.
	before, err := mgr.GetKnobsDefault(ctx, seedSessionID)
	if err != nil {
		t.Fatalf("GetKnobsDefault before Set: %v", err)
	}
	if before != nil {
		t.Fatalf("GetKnobsDefault before Set = %+v, want nil (seed row has knobs_default = NULL)", before)
	}

	effort := "high"
	want := &llm.RequestKnobs{
		Reasoning: &llm.ReasoningConfig{OpenAIEffort: effort},
	}
	if err := mgr.SetKnobsDefault(ctx, seedSessionID, want); err != nil {
		t.Fatalf("SetKnobsDefault on upgraded schema: %v", err)
	}

	got, err := mgr.GetKnobsDefault(ctx, seedSessionID)
	if err != nil {
		t.Fatalf("GetKnobsDefault on upgraded schema: %v", err)
	}
	if got == nil {
		t.Fatal("GetKnobsDefault on the upgraded snapshot: nil; want the persisted override to decode. " +
			"This is the model-settings-reach-the-model WP10 defect surfacing on a REAL upgraded database, " +
			"not just a fresh one.")
	}
	if got.Reasoning == nil || got.Reasoning.OpenAIEffort != effort {
		t.Errorf("GetKnobsDefault on upgraded schema = %+v, want Reasoning.OpenAIEffort=%q", got, effort)
	}

	// Reset (nil) must clear the override, not merely no-op — matching
	// SessionTunePanel's onReset contract.
	if err := mgr.SetKnobsDefault(ctx, seedSessionID, nil); err != nil {
		t.Fatalf("SetKnobsDefault(nil) on upgraded schema: %v", err)
	}
	afterReset, err := mgr.GetKnobsDefault(ctx, seedSessionID)
	if err != nil {
		t.Fatalf("GetKnobsDefault after reset: %v", err)
	}
	if afterReset != nil {
		t.Errorf("GetKnobsDefault after reset = %+v, want nil", afterReset)
	}
}
