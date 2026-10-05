package chat

// agentgraph-settings-linkage-01DOGF0D WP02 (pin P-1): chat run ids are
// unique across runner instances — i.e. across restarts — on ONE
// persistent SQL event log.
//
// The defect this pins is invisible on NewMemoryEventLog: a fresh memory
// log per process means "chat-1" twice never meets itself. It only bites
// when the log outlives the process, which is exactly the production
// wiring (api.go buildAgentGraphEventLog -> NewSQLEventLog on the
// sessions DB). So this test drives real sqlite with the real 0309
// migration DDL, and two separately-constructed ChatRunners standing in
// for "yesterday's process" and "today's process".

import (
	"context"
	"database/sql"
	"path/filepath"
	"strings"
	"testing"

	coreag "github.com/kameas-ai/kenaz-harness/core/agentgraph"
	"github.com/kameas-ai/kenaz-harness/core/session"

	_ "modernc.org/sqlite"
)

// openEventLogDB opens a file-backed sqlite DB carrying the production
// agent_graph_events schema, taken from the registered sessions
// migration rather than a hand copy so a schema change cannot drift
// away from this test.
func openEventLogDB(t *testing.T) *sql.DB {
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
	if ddl == "" {
		t.Fatal("sessions/0309-agent-graph-events not registered")
	}
	for _, stmt := range strings.Split(ddl, ";") {
		if strings.TrimSpace(stmt) == "" {
			continue
		}
		if _, err := db.Exec(stmt); err != nil {
			t.Fatalf("apply 0309: %v", err)
		}
	}
	return db
}

// runOneTurnOn builds a fresh ChatRunner (a "process") whose kernel
// writes to log, runs one text-only turn, waits for it to close, and
// returns the run id.
func runOneTurnOn(t *testing.T, log coreag.EventLog, graph coreag.Graph, reply string) string {
	t.Helper()
	llm := &stubLLM{}
	llm.push(stubLLMResponse{
		stream: []coreag.StreamEvent{{Kind: coreag.StreamEventText, Text: reply}},
		resp:   coreag.LLMResponse{Content: reply, FinishReason: "stop"},
	})
	broker := &recordingBroker{}
	runner, err := New(Config{
		Kernel:        coreag.NewKernel(coreag.WithEventLog(log)),
		Registry:      stubRegistry{},
		Broker:        broker,
		HistoryWriter: &recordingHistoryWriter{},
		History:       staticHistoryReader{msgs: []coreag.Message{{Role: "user", Content: "hi"}}},
		GraphLoader:   func() (coreag.Graph, error) { return graph, nil },
		MaxTurns:      func() int { return 25 },
		EnvDefaults: func(env *coreag.Env) {
			env.LLM = llm
			env.Tools = newStubTools()
		},
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	runID, err := runner.StartStream(context.Background(), "profile-1", "session-1", "", "hi")
	if err != nil {
		t.Fatalf("StartStream: %v", err)
	}
	closed := waitForClosed(t, broker)
	if closed.Reason == "backend-error" {
		t.Fatalf("turn failed: %q", closed.Message)
	}
	return runID
}

func countRunStarts(t *testing.T, log coreag.EventLog, runID string) int {
	t.Helper()
	n := 0
	if err := log.Replay(runID, func(ev coreag.Event) error {
		if ev.Kind == coreag.EventRunStart {
			n++
		}
		return nil
	}); err != nil {
		t.Fatalf("Replay(%q): %v", runID, err)
	}
	return n
}

func TestChatRunID_UniqueAcrossRestartsOnSQLEventLog(t *testing.T) {
	t.Parallel()
	db := openEventLogDB(t)
	log := coreag.NewSQLEventLog(db)
	graph := loadProductionChatGraph(t)

	// Same DB, two runner instances: the second is "after a restart".
	// Before WP02 both minted "chat-1".
	first := runOneTurnOn(t, log, graph, "yesterday")
	second := runOneTurnOn(t, log, graph, "today")

	if first == second {
		t.Fatalf("run ids collide across runner instances: both %q", first)
	}
	for _, id := range []string{first, second} {
		if !strings.HasPrefix(id, "chat-") || len(id) != len("chat-")+26 {
			t.Errorf("run id %q, want chat-<26-char ULID>", id)
		}
		if got := countRunStarts(t, log, id); got != 1 {
			t.Errorf("run %q has %d run_start events in the shared log, want exactly 1", id, got)
		}
	}

	// Materializing turn N returns only turn N: a projection of either
	// run succeeds (it would be refused with ErrRunIDReused if the log
	// carried both turns under one id).
	for _, id := range []string{first, second} {
		if _, err := coreag.MaterializeRun(graph, id, log); err != nil {
			t.Errorf("MaterializeRun(%q): %v", id, err)
		}
	}
}
