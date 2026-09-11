package rpc

// finding-58-cedar-decision-persistence: the Cedar audit-decision log
// (Engine.decisions) had no persistent backing in production.
// Options.Decisions was omitted at BOTH real cedar.NewEngine call sites
// (buildCedarEngineOrNil, the WP05 hoist site's builder, and
// buildCedarGate), so Engine always fell back to
// NewMemoryDecisionStore(0): a 256-entry in-memory ring, silently
// truncated while running and wholly lost on every restart. This is the
// audit trail for every permission-gate decision — trust/compliance-
// relevant per this repo's disposition rules.
//
// This file drives the REAL rpc.New boot path end to end (not a
// standalone cedar.SQLDecisionStore unit test — see
// core/policy/cedar/sql_decision_store_test.go for that) so it proves
// the WIRING, not just the store implementation in isolation.

import (
	"context"
	"database/sql"
	"path/filepath"
	"testing"

	"github.com/kameas-ai/kenaz-harness/core"
	"github.com/kameas-ai/kenaz-harness/core/policy/cedar"

	_ "modernc.org/sqlite"
)

// TestCedarDecisionPersistence_SurvivesCloseAndReopen is the headline
// acceptance test. It boots a real API over a real DataDir, evaluates a
// gated action (producing a Decision), shuts the whole process-
// equivalent down (api.Shutdown flushes the decision store's
// background writer; core.Shutdown closes the underlying storage.DB
// and releases its lock file), then boots a BRAND NEW core.New +
// rpc.New against the SAME DataDir — a distinct *cedar.Engine and a
// distinct *cedar.SQLDecisionStore, sharing nothing in-process with the
// first — and asserts the decision is still there. It also queries
// policy_decisions directly with an independent sqlite connection so
// the assertion cannot be satisfied by an in-memory cache alone.
//
// Falsifiability: this was run against the production wiring reverted
// (New()'s `a.cedarDecisions = buildCedarDecisionStore(c)` /
// buildCedarEngineOrNil's `Decisions: decisions` field removed) and
// confirmed RED — see the mission report for the pasted failure output.
func TestCedarDecisionPersistence_SurvivesCloseAndReopen(t *testing.T) {
	sandboxUserConfigDir(t)
	dataDir := t.TempDir()

	c1, err := core.New(core.Options{DataDir: dataDir})
	if err != nil {
		t.Fatalf("core.New: %v", err)
	}
	api1 := New(c1)
	if api1.cedarEngine == nil {
		t.Fatal("cedarEngine is nil over a real DataDir — cannot exercise persistence")
	}
	if api1.cedarDecisions == nil {
		t.Fatal("cedarDecisions is nil over a real DataDir — the durable decision store did not wire (finding #58 regression)")
	}

	ctx := context.Background()
	if err := cedar.CheckMemoryWrite(ctx, api1.cedarGate(), "global"); err != nil {
		t.Fatalf("memory_write denied on a default install: %v", err)
	}
	before := api1.cedarEngine.RecentDecisions(10)
	if len(before) == 0 {
		t.Fatal("Evaluate produced no in-memory decision — nothing to test persistence of")
	}

	// Flush the decision store's background writer, then close the
	// storage layer entirely (releases the sqlite lock file so a second
	// core.New against the same DataDir below can succeed).
	api1.Shutdown()
	if err := c1.Shutdown(ctx); err != nil {
		t.Fatalf("c1.Shutdown: %v", err)
	}

	c2, err := core.New(core.Options{DataDir: dataDir})
	if err != nil {
		t.Fatalf("core.New (reopen): %v", err)
	}
	api2 := New(c2)
	t.Cleanup(api2.Shutdown)
	t.Cleanup(func() { _ = c2.Shutdown(ctx) })

	after := api2.cedarEngine.RecentDecisions(10)
	if len(after) == 0 {
		t.Fatal("RecentDecisions is empty on the reopened engine — the decision did not survive a restart")
	}
	found := false
	for _, d := range after {
		if d.Action == before[0].Action && d.Outcome == before[0].Outcome && d.Resource == before[0].Resource {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("reopened engine's RecentDecisions does not contain the pre-shutdown decision: got %+v, want one matching %+v", after, before[0])
	}

	// Independent verification, bypassing BOTH engines' in-memory
	// caches: a fresh sqlite connection reading the table directly.
	rawPath := filepath.Join(dataDir, "data.db")
	raw, openErr := sql.Open("sqlite", "file:"+rawPath+"?_pragma=busy_timeout(5000)")
	if openErr != nil {
		t.Fatalf("open raw sqlite for independent verification: %v", openErr)
	}
	defer raw.Close()
	var count int
	if err := raw.QueryRowContext(ctx, "SELECT COUNT(*) FROM policy_decisions").Scan(&count); err != nil {
		t.Fatalf("query policy_decisions directly: %v", err)
	}
	if count == 0 {
		t.Fatal("policy_decisions has zero rows after shutdown — nothing was actually written to disk")
	}
}
