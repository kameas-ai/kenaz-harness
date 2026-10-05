package sqlite_test

// feat/graph-resolved-spec WP01: migration
// sessions/0343-agent-graph-run-specs on a database a PREVIOUS RELEASE
// produced (CLAUDE.md blind spot #3), driven through the production Open
// path and the production writer (the kernel, over the SQL event log on
// the same handle core/rpc/api.go's buildAgentGraphEventLog uses).
//
// Pins:
//   - the v0.86.0 snapshot has no agent_graph_run_specs table (so the
//     test really exercises 0343's CREATE), and Open leaves nothing
//     pending;
//   - a kernel run on the upgraded database stores its resolved spec
//     exactly once, retrievable by run id, digest-checked;
//   - re-open after a ledger rewind (0343's ledger row deleted, as the
//     1104 tests rewind units/1104) re-applies the migration WITHOUT
//     touching the stored row — the CREATE is IF NOT EXISTS.

import (
	"context"
	"database/sql"
	"os"
	"path/filepath"
	"testing"

	coreag "github.com/kameas-ai/kenaz-harness/core/agentgraph"
	"github.com/kameas-ai/kenaz-harness/core/storage"
	storagesqlite "github.com/kameas-ai/kenaz-harness/core/storage/sqlite"
	"github.com/kameas-ai/kenaz-harness/core/storage/sqlite/upgradesnap"

	_ "modernc.org/sqlite"
)

const migration0343ID = "sessions/0343-agent-graph-run-specs"

func runSpecFixtureGraph() coreag.Graph {
	return coreag.Graph{
		SpecVersion: coreag.SpecVersion,
		ID:          "run_spec_fixture",
		Name:        "Run spec fixture",
		Entrypoints: []string{"first"},
		Nodes: []coreag.Node{
			{ID: "first", Kind: coreag.NodeKindTransform, Title: "First", Attrs: coreag.TransformAttrs{Name: "concat"}},
			{ID: "second", Kind: coreag.NodeKindTransform, Title: "Second", Attrs: coreag.TransformAttrs{Name: "concat"}},
		},
		Edges: []coreag.Edge{
			{From: coreag.EndpointRef{Node: "first", Port: "out"}, To: coreag.EndpointRef{Node: "second", Port: "in"}},
		},
	}
}

func runSpecSQL(t *testing.T, db storage.DB) *sql.DB {
	t.Helper()
	h, ok := db.(interface{ SQL() *sql.DB })
	if !ok {
		t.Fatal("storage handle does not expose SQL()")
	}
	return h.SQL()
}

func TestMigration0343_RunSpecsOnPreviousReleaseSnapshot_SurvivesLedgerRewind(t *testing.T) {
	ctx := context.Background()
	dumpPath := filepath.Join("testdata", "upgrade", "v0.86.0", "dump.sql")
	dumpText, err := os.ReadFile(dumpPath)
	if err != nil {
		t.Fatalf("read v0.86.0 snapshot: %v", err)
	}
	dir := t.TempDir()
	raw := openRawSQLiteAt(t, filepath.Join(dir, "data.db"))
	if err := upgradesnap.Materialize(ctx, raw, string(dumpText)); err != nil {
		t.Fatalf("materialize v0.86.0 snapshot: %v", err)
	}
	var n int
	if err := raw.QueryRowContext(ctx,
		"SELECT COUNT(*) FROM sqlite_master WHERE type='table' AND name='agent_graph_run_specs'").Scan(&n); err != nil {
		t.Fatalf("pre-Open table check: %v", err)
	}
	if n != 0 {
		t.Fatal("v0.86.0 snapshot already has agent_graph_run_specs — this test no longer proves what it claims")
	}
	if err := raw.Close(); err != nil {
		t.Fatalf("close raw: %v", err)
	}

	db, err := storagesqlite.Open(newConfig(dir))
	if err != nil {
		t.Fatalf("Open on the v0.86.0 snapshot: %v", err)
	}
	if pending, err := db.Migrations().Pending(); err != nil || len(pending) != 0 {
		t.Fatalf("Pending after Open = %v (err %v), want none", pending, err)
	}

	// The production writer: the kernel, over the SQL event log.
	log := coreag.NewSQLEventLog(runSpecSQL(t, db))
	g := runSpecFixtureGraph()
	const runID = "chat-01J0000000000000000UPGRADE"
	env := &coreag.Env{RunID: runID, Graph: &g}
	if err := coreag.NewKernel(coreag.WithEventLog(log)).Run(ctx, env); err != nil {
		t.Fatalf("kernel run: %v", err)
	}
	got, found, err := log.LoadRunSpec(runID)
	if err != nil || !found {
		t.Fatalf("LoadRunSpec after run = found %v, err %v; want the stored spec", found, err)
	}
	if coreag.SpecDigest(got) != coreag.SpecDigest(g) {
		t.Errorf("stored spec digest %s, want %s", coreag.SpecDigest(got), coreag.SpecDigest(g))
	}
	// Once per run, not per event.
	var rows int
	if err := db.Reader().QueryRow(ctx, "SELECT COUNT(*) FROM agent_graph_run_specs").Scan(&rows); err != nil {
		t.Fatalf("count: %v", err)
	}
	if rows != 1 {
		t.Errorf("agent_graph_run_specs rows = %d after one run, want 1", rows)
	}
	if err := db.Close(ctx); err != nil {
		t.Fatalf("close: %v", err)
	}

	// Rewind 0343's ledger row; the reopen must re-apply it cleanly and
	// keep the row.
	raw = openRaw(t, dir)
	if _, err := raw.ExecContext(ctx, "DELETE FROM harness_migrations WHERE id = ?", migration0343ID); err != nil {
		t.Fatalf("rewind ledger: %v", err)
	}
	if err := raw.Close(); err != nil {
		t.Fatalf("close raw: %v", err)
	}
	db2, err := storagesqlite.Open(newConfig(dir))
	if err != nil {
		t.Fatalf("reopen after ledger rewind: %v", err)
	}
	t.Cleanup(func() { _ = db2.Close(context.Background()) })
	var applied int
	if err := db2.Reader().QueryRow(ctx,
		"SELECT COUNT(*) FROM harness_migrations WHERE id = ? AND action = 'applied'", migration0343ID).Scan(&applied); err != nil {
		t.Fatalf("ledger check: %v", err)
	}
	if applied != 1 {
		t.Errorf("0343 ledger rows after reopen = %d, want 1 (re-applied)", applied)
	}
	again, found, err := coreag.NewSQLEventLog(runSpecSQL(t, db2)).LoadRunSpec(runID)
	if err != nil || !found {
		t.Fatalf("stored spec lost across the rewind/re-apply: found %v err %v", found, err)
	}
	if coreag.SpecDigest(again) != coreag.SpecDigest(g) {
		t.Error("stored spec changed across the rewind/re-apply")
	}
}
