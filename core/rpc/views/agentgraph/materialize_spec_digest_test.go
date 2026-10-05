package agentgraph_test

// agentgraph-settings-linkage-01DOGF0D WP03 (pin P-3, backend half):
// after a restart the chat-run spec registry is empty, so a past turn
// materializes from the library graph its run_start names (tier 3). The
// run_start event now records spec_digest — which VERSION of the spec
// ran — so tier 3 can verify itself: a matching library file is the
// exact spec (no degraded banner); a mismatch or a pre-WP03 run keeps
// the library_fallback marker.
//
// Driven on the SQL event log with a FRESH manager per phase: "after a
// restart" is the whole point, and the memory log cannot outlive the
// manager that wrote it.

import (
	"context"
	"database/sql"
	"path/filepath"
	"strings"
	"testing"

	coreag "github.com/kameas-ai/kenaz-harness/core/agentgraph"
	graphview "github.com/kameas-ai/kenaz-harness/core/rpc/views/agentgraph"
	"github.com/kameas-ai/kenaz-harness/core/session"

	_ "modernc.org/sqlite"
)

func openSQLEventLog(t *testing.T) *coreag.SQLEventLog {
	t.Helper()
	db, err := sql.Open("sqlite", filepath.Join(t.TempDir(), "events.db"))
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	var ddl string
	for _, m := range session.Migrations() {
		if m.ID == "sessions/0309-agent-graph-events" {
			ddl = m.UpSource
		}
	}
	for _, stmt := range strings.Split(ddl, ";") {
		if strings.TrimSpace(stmt) == "" {
			continue
		}
		if _, err := db.Exec(stmt); err != nil {
			t.Fatalf("apply 0309: %v", err)
		}
	}
	return coreag.NewSQLEventLog(db)
}

func TestMaterializeRun_LibraryFallbackVerifiedBySpecDigest(t *testing.T) {
	t.Parallel()
	log := openSQLEventLog(t)

	// "Yesterday's process": learn the library digest.
	before, err := graphview.NewManager(graphview.WithEventLog(log))
	if err != nil {
		t.Fatalf("NewManager: %v", err)
	}
	lib, err := before.LoadGraphSpec("chat_default")
	if err != nil {
		t.Fatalf("LoadGraphSpec: %v", err)
	}
	libDigest := coreag.SpecDigest(lib)
	if !strings.HasPrefix(libDigest, "sha256:") {
		t.Fatalf("SpecDigest = %q, want sha256:<hex>", libDigest)
	}

	write := func(runID string, startPayload map[string]any) {
		ev := &fakeTurnEvents{runID: runID}
		ev.add("", coreag.EventRunStart, startPayload)
		ev.fire("history_in", "history_read")
		ev.fire("compact_history", "compact")
		ev.fire("ask_user", "ask")
		ev.add("", coreag.EventRunComplete, map[string]any{"completed_nodes": 3})
		if _, err := log.Append(ev.batch); err != nil {
			t.Fatalf("append %s: %v", runID, err)
		}
	}
	write("chat-01J0000000000000000000MATCH", map[string]any{"graph_id": "chat_default", "spec_digest": libDigest})
	write("chat-01J000000000000000000DIFFER", map[string]any{"graph_id": "chat_default", "spec_digest": "sha256:0000"})
	write("chat-01J00000000000000000LEGACY", map[string]any{"graph_id": "chat_default"})

	// "Today's process": a fresh manager on the same log, nothing tracked.
	after, err := graphview.NewManager(graphview.WithEventLog(log))
	if err != nil {
		t.Fatalf("NewManager (after restart): %v", err)
	}
	api := graphview.New(after)
	for _, tc := range []struct {
		runID string
		want  string
	}{
		{"chat-01J0000000000000000000MATCH", ""},
		{"chat-01J000000000000000000DIFFER", coreag.SpecProvenanceLibraryFallback},
		{"chat-01J00000000000000000LEGACY", coreag.SpecProvenanceLibraryFallback},
	} {
		spec, err := api.MaterializeRun(context.Background(), tc.runID)
		if err != nil {
			t.Fatalf("MaterializeRun(%s): %v", tc.runID, err)
		}
		if spec.SpecProvenance != tc.want {
			t.Errorf("%s: SpecProvenance = %q, want %q", tc.runID, spec.SpecProvenance, tc.want)
		}
	}
}

// SpecDigest must ignore presentation metadata (a layout-only edit is
// not a different spec) and must see a semantic change.
func TestSpecDigest_StableUnderLayoutSensitiveToSpec(t *testing.T) {
	t.Parallel()
	mgr, err := graphview.NewManager()
	if err != nil {
		t.Fatalf("NewManager: %v", err)
	}
	g, err := mgr.LoadGraphSpec("chat_default")
	if err != nil {
		t.Fatalf("LoadGraphSpec: %v", err)
	}
	base := coreag.SpecDigest(g)
	if again := coreag.SpecDigest(g); again != base {
		t.Fatalf("SpecDigest not deterministic: %q vs %q", base, again)
	}
	moved := g
	moved.Layout = map[string]coreag.NodeLayout{g.Nodes[0].ID: {X: 10, Y: 20}}
	if coreag.SpecDigest(moved) != base {
		t.Error("a layout-only change altered the spec digest")
	}
	edited := g
	edited.SystemPrompt = g.SystemPrompt + " (edited)"
	if coreag.SpecDigest(edited) == base {
		t.Error("a semantic change did not alter the spec digest")
	}
}
