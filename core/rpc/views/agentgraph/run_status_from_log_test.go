package agentgraph_test

// agentgraph-settings-linkage-01DOGF0D review H1: RunView (linked from
// every transcript turn by WP04) calls GetRunStatus / GetRunTrace, which
// used to read only m.runs — a registry chat turns never enter. Every
// chat turn's "Run details" link therefore opened a "not found" banner
// over a failing poll. These drive a run exactly the way the chat runner
// does (TrackExternalRun + the manager's shared kernel, never StartRun)
// on the SQL event log, then read it back through the API — in the same
// process and from a fresh manager on the same log (after a restart).

import (
	"context"
	"testing"

	coreag "github.com/kameas-ai/kenaz-harness/core/agentgraph"
	graphview "github.com/kameas-ai/kenaz-harness/core/rpc/views/agentgraph"
)

func TestGetRunStatusAndTrace_ChatRunResolvedFromEventLog(t *testing.T) {
	t.Parallel()
	log := openSQLEventLog(t)
	mgr, err := graphview.NewManager(graphview.WithEventLog(log))
	if err != nil {
		t.Fatalf("NewManager: %v", err)
	}
	api := graphview.New(mgr)
	ctx := context.Background()

	const runID = "chat-01J0000000000000000STATUS"
	g := materializeRPCGraph()
	env := &coreag.Env{RunID: runID, SessionID: "sess-1", Graph: &g}
	mgr.TrackExternalRun(runID, g)
	if err := mgr.Kernel().Run(ctx, env); err != nil {
		t.Fatalf("kernel run: %v", err)
	}

	for name, a := range map[string]graphview.API{
		"same process":  api,
		"after restart": graphview.New(mustManager(t, log)),
	} {
		st, err := a.GetRunStatus(ctx, runID)
		if err != nil {
			t.Fatalf("%s: GetRunStatus(chat run): %v", name, err)
		}
		if st.State != graphview.RunStateCompleted || st.RunID != runID || st.GraphID != g.ID || st.SessionID != "sess-1" {
			t.Errorf("%s: status = %+v, want completed run %q of graph %q in sess-1", name, st, runID, g.ID)
		}
		if st.NodesComplete == 0 || st.CompletedAt == "" {
			t.Errorf("%s: status carries no progress: %+v", name, st)
		}
		trace, err := a.GetRunTrace(ctx, runID, 0)
		if err != nil {
			t.Fatalf("%s: GetRunTrace(chat run): %v", name, err)
		}
		if len(trace) == 0 || trace[0].Kind != string(coreag.EventRunStart) {
			t.Errorf("%s: trace = %d events, want the recorded run starting with run_start", name, len(trace))
		}
	}

	// An unknown run is still an honest not-found.
	if _, err := api.GetRunStatus(ctx, "chat-nope"); err == nil {
		t.Error("GetRunStatus(unknown) should fail")
	}
	if _, err := api.GetRunTrace(ctx, "chat-nope", 0); err == nil {
		t.Error("GetRunTrace(unknown) should fail")
	}
}

// A log-only run with no terminal record that this process is not
// running (the app exited mid-turn) must read as finished-with-an-error,
// not "running" — RunView would otherwise poll it forever.
func TestGetRunStatus_UnfinishedLogOnlyRunIsInterrupted(t *testing.T) {
	t.Parallel()
	log := openSQLEventLog(t)
	ev := &fakeTurnEvents{runID: "chat-01J000000000000000CRASHED"}
	ev.add("", coreag.EventRunStart, map[string]any{"graph_id": "chat_default"})
	ev.fire("history_in", "history_read")
	if _, err := log.Append(ev.batch); err != nil {
		t.Fatalf("append: %v", err)
	}
	st, err := graphview.New(mustManager(t, log)).GetRunStatus(context.Background(), ev.runID)
	if err != nil {
		t.Fatalf("GetRunStatus: %v", err)
	}
	if st.State != graphview.RunStateFailed || st.Error == "" {
		t.Errorf("status = %+v, want failed with an interruption reason", st)
	}
}

func mustManager(t *testing.T, log coreag.EventLog) *graphview.Manager {
	t.Helper()
	m, err := graphview.NewManager(graphview.WithEventLog(log))
	if err != nil {
		t.Fatalf("NewManager: %v", err)
	}
	return m
}
