package agentgraph_test

// feat/graph-resolved-spec WP01: the RunSpecStore half of both shipped
// EventLogs, and the kernel's write through it. The SQL cases run on a
// real sqlite file with the schema taken from the production migrations
// (sessions/0309 + sessions/0343), not a hand-copied DDL that can drift.

import (
	"context"
	"database/sql"
	"errors"
	"path/filepath"
	"strings"
	"testing"

	coreag "github.com/kameas-ai/kenaz-harness/core/agentgraph"
	"github.com/kameas-ai/kenaz-harness/core/session"

	_ "modernc.org/sqlite"
)

func openRunSpecDB(t *testing.T) *sql.DB {
	t.Helper()
	db, err := sql.Open("sqlite", filepath.Join(t.TempDir(), "events.db"))
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	db.SetMaxOpenConns(1)
	t.Cleanup(func() { _ = db.Close() })
	applied := 0
	for _, m := range session.Migrations() {
		if m.ID != "sessions/0309-agent-graph-events" && m.ID != "sessions/0343-agent-graph-run-specs" {
			continue
		}
		for _, stmt := range strings.Split(m.UpSource, ";") {
			if strings.TrimSpace(stmt) == "" {
				continue
			}
			if _, err := db.Exec(stmt); err != nil {
				t.Fatalf("apply %s: %v", m.ID, err)
			}
		}
		applied++
	}
	if applied != 2 {
		t.Fatalf("applied %d of the 2 migrations the store needs", applied)
	}
	return db
}

func twoNodeGraph(id string) coreag.Graph {
	return coreag.Graph{
		SpecVersion: coreag.SpecVersion,
		ID:          id,
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

// eachStore runs fn against both shipped logs.
func eachStore(t *testing.T, fn func(t *testing.T, log coreag.EventLog, store coreag.RunSpecStore)) {
	t.Run("sql", func(t *testing.T) {
		log := coreag.NewSQLEventLog(openRunSpecDB(t))
		fn(t, log, log)
	})
	t.Run("memory", func(t *testing.T) {
		log := coreag.NewMemoryEventLog()
		store, ok := log.(coreag.RunSpecStore)
		if !ok {
			t.Fatal("NewMemoryEventLog does not implement RunSpecStore")
		}
		fn(t, log, store)
	})
}

func TestRunSpecStore_RoundTripInsertOnceAndMiss(t *testing.T) {
	t.Parallel()
	eachStore(t, func(t *testing.T, _ coreag.EventLog, store coreag.RunSpecStore) {
		if _, found, err := store.LoadRunSpec("never-ran"); err != nil || found {
			t.Fatalf("LoadRunSpec(unknown) = found %v err %v, want a clean miss", found, err)
		}
		first := twoNodeGraph("first_version")
		if err := store.RecordRunSpec("run-1", first); err != nil {
			t.Fatalf("RecordRunSpec: %v", err)
		}
		// A re-entry (Resume / redrive) must not replace the spec the run
		// started with.
		if err := store.RecordRunSpec("run-1", twoNodeGraph("second_version")); err != nil {
			t.Fatalf("RecordRunSpec (re-entry): %v", err)
		}
		got, found, err := store.LoadRunSpec("run-1")
		if err != nil || !found {
			t.Fatalf("LoadRunSpec = found %v err %v", found, err)
		}
		if got.ID != "first_version" || coreag.SpecDigest(got) != coreag.SpecDigest(first) {
			t.Errorf("stored spec = %s (%s), want the first write %s", got.ID, coreag.SpecDigest(got), coreag.SpecDigest(first))
		}
		if err := store.RecordRunSpec("", first); err == nil {
			t.Error("RecordRunSpec accepted an empty run id")
		}
	})
}

func TestRunSpecStore_OversizedSpecRefused(t *testing.T) {
	t.Parallel()
	eachStore(t, func(t *testing.T, _ coreag.EventLog, store coreag.RunSpecStore) {
		g := twoNodeGraph("huge")
		g.SystemPrompt = strings.Repeat("x", coreag.MaxRunSpecBytes)
		if err := store.RecordRunSpec("run-huge", g); !errors.Is(err, coreag.ErrRunSpecTooLarge) {
			t.Fatalf("RecordRunSpec(oversized) err = %v, want ErrRunSpecTooLarge", err)
		}
		if _, found, err := store.LoadRunSpec("run-huge"); err != nil || found {
			t.Errorf("oversized spec was stored anyway: found %v err %v", found, err)
		}
	})
}

// A row whose JSON no longer decodes to the digest it was written with
// is refused, never served as the exact spec.
func TestSQLRunSpecStore_CorruptRowIsAnError(t *testing.T) {
	t.Parallel()
	db := openRunSpecDB(t)
	log := coreag.NewSQLEventLog(db)
	if err := log.RecordRunSpec("run-c", twoNodeGraph("g")); err != nil {
		t.Fatalf("RecordRunSpec: %v", err)
	}
	if _, err := db.Exec(`UPDATE agent_graph_run_specs SET spec_digest = 'sha256:00' WHERE run_id = 'run-c'`); err != nil {
		t.Fatalf("corrupt: %v", err)
	}
	if _, found, err := log.LoadRunSpec("run-c"); err == nil || found {
		t.Errorf("corrupt row = found %v err %v, want an error", found, err)
	}
}

// The kernel stores env.Graph — the spec it executes — once per run,
// before run_start, and a resume does not rewrite it.
func TestKernel_RecordsResolvedSpecOncePerRun(t *testing.T) {
	t.Parallel()
	eachStore(t, func(t *testing.T, log coreag.EventLog, store coreag.RunSpecStore) {
		k := coreag.NewKernel(coreag.WithEventLog(log))
		g := twoNodeGraph("executed")
		// A run-time rewrite of the loaded graph — what the routing gate
		// and dials do before the kernel sees it.
		g.Description = "resolved at run time"
		env := &coreag.Env{RunID: "run-k", Graph: &g}
		if err := k.Run(context.Background(), env); err != nil {
			t.Fatalf("Run: %v", err)
		}
		got, found, err := store.LoadRunSpec("run-k")
		if err != nil || !found {
			t.Fatalf("LoadRunSpec after Run = found %v err %v", found, err)
		}
		if got.Description != "resolved at run time" {
			t.Errorf("stored description %q — not the spec the kernel executed", got.Description)
		}
		// run_start's digest is the stored spec's digest.
		var startDigest string
		_ = log.Replay("run-k", func(ev coreag.Event) error {
			if ev.Kind == coreag.EventRunStart && startDigest == "" {
				startDigest = digestFromPayload(t, ev.Payload)
			}
			return nil
		})
		if startDigest == "" || startDigest != coreag.SpecDigest(got) {
			t.Errorf("run_start spec_digest %q != stored spec digest %q", startDigest, coreag.SpecDigest(got))
		}
		// Re-entering the run with a different graph keeps the first.
		other := twoNodeGraph("other")
		env2 := &coreag.Env{RunID: "run-k", Graph: &other}
		_ = k.Run(context.Background(), env2)
		again, _, _ := store.LoadRunSpec("run-k")
		if again.ID != "executed" {
			t.Errorf("re-entry replaced the stored spec with %q", again.ID)
		}
	})
}

func digestFromPayload(t *testing.T, payload []byte) string {
	t.Helper()
	const key = `"spec_digest":"`
	s := string(payload)
	i := strings.Index(s, key)
	if i < 0 {
		return ""
	}
	rest := s[i+len(key):]
	j := strings.IndexByte(rest, '"')
	if j < 0 {
		return ""
	}
	return rest[:j]
}
