package chat

// agentgraph-settings-linkage-01DOGF0D WP03 — pin P-2 / AC-PI-1: the
// turn -> run mapping on a database a PREVIOUS release produced.
//
// Boots core/storage/sqlite/testdata/upgrade/v0.85.2 (the newest
// snapshot at the time of writing, i.e. a pre-mission install with
// populated sessions) under HEAD, so migration sessions/0342 applies to
// populated tables — not to an empty directory where it cannot fail
// interestingly (CLAUDE.md blind spot #3). Then it drives the REAL
// call site: a ChatRunner turn whose TurnRuns seam is the real
// *session.Manager over that database and whose kernel writes the real
// SQL event log on the same file, exactly as core/rpc/api.go wires it.
//
// Asserts:
//   - the snapshot's pre-existing sessions have NO mapping (old turns
//     render the disabled-with-reason affordance, never a link);
//   - a legacy pre-WP02 "chat-1" run already in the event log is left
//     untouched, and the new turn does not reuse its id;
//   - the new turn maps: run id, turn span, graph id and spec digest
//     round-trip through SQL, and run_start carries the same session,
//     span and digest.
//
// Falsifiable: drop the TurnRuns call in StartStream (or the 0342
// registration) and the "new turn maps" assertions fail.

import (
	"context"
	"database/sql"
	"encoding/json"
	"net/url"
	"os"
	"path/filepath"
	"testing"

	coreag "github.com/kameas-ai/kenaz-harness/core/agentgraph"
	"github.com/kameas-ai/kenaz-harness/core/session"
	"github.com/kameas-ai/kenaz-harness/core/storage"
	storagesqlite "github.com/kameas-ai/kenaz-harness/core/storage/sqlite"
	"github.com/kameas-ai/kenaz-harness/core/storage/sqlite/upgradesnap"

	_ "modernc.org/sqlite"
)

const v0852DumpRelPath = "../../../../storage/sqlite/testdata/upgrade/v0.85.2/dump.sql"

func TestTurnRuns_RecordedOnUpgradedDatabase_OldTurnsUnmapped(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()

	dumpText, err := os.ReadFile(v0852DumpRelPath)
	if err != nil {
		t.Fatalf("read v0.85.2 dump.sql: %v", err)
	}
	rawPath := filepath.Join(dir, "data.db")
	raw, err := sql.Open("sqlite", "file:"+url.PathEscape(rawPath)+"?_pragma=foreign_keys(1)&_pragma=busy_timeout(5000)")
	if err != nil {
		t.Fatalf("open raw sqlite: %v", err)
	}
	raw.SetMaxOpenConns(1)
	if err := upgradesnap.Materialize(ctx, raw, string(dumpText)); err != nil {
		t.Fatalf("materialise v0.85.2 snapshot: %v", err)
	}
	// A pre-WP02 chat run already in the persistent log, as every
	// install that has chatted carries.
	if _, err := raw.ExecContext(ctx, `INSERT INTO agent_graph_events
		(run_id, session_id, node_id, seq, kind, payload_json, ts_ns)
		VALUES ('chat-1', '', '', 1, 'run_start', '{"graph_id":"chat_default"}', 1)`); err != nil {
		t.Fatalf("seed legacy chat-1 run: %v", err)
	}
	if err := raw.Close(); err != nil {
		t.Fatalf("close raw: %v", err)
	}

	db, err := storagesqlite.Open(storage.Config{DataDir: dir, EncryptionStatus: storage.EncryptionStatusDisabledWithDiskEncryption})
	if err != nil {
		t.Fatalf("Open on the v0.85.2 snapshot: %v", err)
	}
	defer func() { _ = db.Close(context.Background()) }()
	if pending, err := db.Migrations().Pending(); err != nil || len(pending) != 0 {
		t.Fatalf("Pending after Open = %v (err %v), want none — sessions/0342 must have applied", pending, err)
	}

	mgr := session.NewManager(session.NewSQLStore(session.NewStorageDB(db)))

	// Old turns: the snapshot's sessions predate the mapping.
	for _, old := range []string{"seed-session-1", "seed-session-2"} {
		runs, err := mgr.ListTurnRuns(ctx, old)
		if err != nil {
			t.Fatalf("ListTurnRuns(%s): %v", old, err)
		}
		if len(runs) != 0 {
			t.Errorf("pre-mission session %s has %d turn->run mappings, want 0", old, len(runs))
		}
	}

	// New turn, on an existing (upgraded) session.
	const sessionID = "seed-session-1"
	// Same structural-interface dance as api.go buildAgentGraphEventLog:
	// the production event log is on this very database handle.
	h, ok := db.(interface{ SQL() *sql.DB })
	if !ok {
		t.Fatal("storage handle does not expose SQL()")
	}
	log := coreag.NewSQLEventLog(h.SQL())
	graph := loadProductionChatGraph(t)
	llm := &stubLLM{}
	llm.push(stubLLMResponse{
		stream: []coreag.StreamEvent{{Kind: coreag.StreamEventText, Text: "ok"}},
		resp:   coreag.LLMResponse{Content: "ok", FinishReason: "stop"},
	})
	broker := &recordingBroker{}
	runner, err := New(Config{
		Kernel:        coreag.NewKernel(coreag.WithEventLog(log)),
		Registry:      stubRegistry{},
		Broker:        broker,
		HistoryWriter: &recordingHistoryWriter{}, // span id "msg-1"
		History:       staticHistoryReader{msgs: []coreag.Message{{Role: "user", Content: "hi"}}},
		GraphLoader:   func() (coreag.Graph, error) { return graph, nil },
		MaxTurns:      func() int { return 25 },
		TurnRuns:      mgr,
		EnvDefaults: func(env *coreag.Env) {
			env.LLM = llm
			env.Tools = newStubTools()
		},
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	runID, err := runner.StartStream(ctx, "profile-1", sessionID, "", "hi")
	if err != nil {
		t.Fatalf("StartStream: %v", err)
	}
	// The mapping is written before StartStream returns: the live turn
	// is linkable immediately.
	runs, err := mgr.ListTurnRuns(ctx, sessionID)
	if err != nil {
		t.Fatalf("ListTurnRuns: %v", err)
	}
	if closed := waitForClosed(t, broker); closed.Reason == "backend-error" {
		t.Fatalf("turn failed: %q", closed.Message)
	}

	if runID == "chat-1" {
		t.Fatal("new turn reused the legacy chat-1 run id")
	}
	if got := countRunStarts(t, log, "chat-1"); got != 1 {
		t.Errorf("legacy chat-1 now has %d run_start events, want 1 (untouched)", got)
	}
	if len(runs) != 1 {
		t.Fatalf("session %s has %d mappings after one new turn, want 1: %+v", sessionID, len(runs), runs)
	}
	tr := runs[0]
	wantDigest := coreag.SpecDigest(graph)
	if tr.RunID != runID || tr.TurnSpanID != "msg-1" || tr.GraphID != graph.ID || tr.SpecDigest != wantDigest {
		t.Errorf("mapping = %+v, want run %q span msg-1 graph %q digest %q", tr, runID, graph.ID, wantDigest)
	}

	var start struct {
		SessionID  string `json:"session_id"`
		TurnSpanID string `json:"turn_span_id"`
		SpecDigest string `json:"spec_digest"`
	}
	if err := log.Replay(runID, func(ev coreag.Event) error {
		if ev.Kind == coreag.EventRunStart {
			return json.Unmarshal(ev.Payload, &start)
		}
		return nil
	}); err != nil {
		t.Fatalf("Replay: %v", err)
	}
	if start.SessionID != sessionID || start.TurnSpanID != "msg-1" || start.SpecDigest != wantDigest {
		t.Errorf("run_start = %+v, want session %q span msg-1 digest %q", start, sessionID, wantDigest)
	}

	// The mapping dies with its session (FK cascade, as session_messages).
	if err := mgr.Delete(ctx, sessionID); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	if runs, _ := mgr.ListTurnRuns(ctx, sessionID); len(runs) != 0 {
		t.Errorf("mappings survived session delete: %+v", runs)
	}
}
