package rpc

// agentgraph-settings-linkage-01DOGF0D review M3 (+ H1 at the composition
// boundary): the turn -> run mapping and the run's status must survive
// the REAL wiring, not only the chat package's hand-built Config.
//
// The chat-package pin (turn_runs_upgrade_test.go) hands the runner a
// session.Manager itself, so deleting `TurnRuns: turnRuns` from
// newLLMStack's chat.Config left every test green — the B0/B4 class.
// This drives a real text turn through newLLMStack's real chat runner
// (the real core/llm/anthropic adapter against an httptest SSE fixture,
// the B4 pattern), over a real Core whose storage is real sqlite, then
// reads the link back through the SAME sessions API production serves
// Sessions_TurnRuns from (newSessionsAPI), and the run's status through
// the real graph manager's API (GetRunStatus — RunView's poll).
//
// Falsifiable: delete `TurnRuns: turnRuns,` in api.go -> no mapping;
// revert Manager.statusFromLog -> "run not found".

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/kameas-ai/kenaz-harness/core"
	corellm "github.com/kameas-ai/kenaz-harness/core/llm"
	"github.com/kameas-ai/kenaz-harness/core/llm/anthropic"
	graphview "github.com/kameas-ai/kenaz-harness/core/rpc/views/agentgraph"
	chat "github.com/kameas-ai/kenaz-harness/core/rpc/views/agentgraph/chat"
	"github.com/kameas-ai/kenaz-harness/core/session"
	corebash "github.com/kameas-ai/kenaz-harness/core/tools/bash"
)

func TestTurnRunsWiring_RealTurnRecordedAndRunStatusReadable(t *testing.T) {
	sandboxUserConfigDir(t)
	dataDir := t.TempDir()
	c, err := core.New(core.Options{DataDir: dataDir})
	if err != nil {
		t.Fatalf("core.New: %v", err)
	}
	cedarEngine := buildCedarEngineOrNil(dataDir, nil)
	memStore := openMemoryStore(c)
	if memStore == nil {
		t.Fatal("openMemoryStore returned nil over a real DataDir")
	}
	bashStore := corebash.NewStore()
	graphMgr, _, _, _ := newGraphManagerWithDeps(c, nil, nil, memStore, nil, bashStore, nil, cedarEngine, nil, nil)
	if graphMgr == nil {
		t.Fatal("newGraphManagerWithDeps returned a nil manager")
	}

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		for _, f := range []string{
			`{"type":"message_start","message":{"id":"msg_1","role":"assistant","model":"zz-tr-model","usage":{"input_tokens":1,"output_tokens":1}}}`,
			`{"type":"content_block_start","index":0,"content_block":{"type":"text","text":""}}`,
			`{"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"done"}}`,
			`{"type":"content_block_stop","index":0}`,
			`{"type":"message_delta","delta":{"stop_reason":"end_turn"},"usage":{"input_tokens":1,"output_tokens":1}}`,
			`{"type":"message_stop"}`,
		} {
			fmt.Fprintf(w, "data: %s\n\n", f)
		}
		if fl, ok := w.(http.Flusher); ok {
			fl.Flush()
		}
	}))
	t.Cleanup(srv.Close)

	broker := NewStreamBroker(NewMultiEmitter())
	stack := newLLMStack(c, broker, newPersonalStore(c), nil, nil, func() bool { return false },
		nil, nil, nil, bashStore, nil, graphMgr, nil, nil, nil, nil,
		nil, nil, nil, nil, confirmAuditEmitter{}, nil, cedarEngine, nil, nil, nil, nil)
	if stack.compactionScheduler != nil {
		t.Cleanup(stack.compactionScheduler.Stop)
	}
	if stack.chatRunner == nil || stack.reg == nil {
		t.Fatal("newLLMStack produced no chat runner / registry")
	}
	stack.reg.RegisterAdapter(anthropic.New(anthropic.WithEndpoint(srv.URL)))
	t.Setenv("ZZ_TURNRUNS_KEY", "unused-test-key")
	prof := corellm.ProviderProfile{
		ID:    "zz-turnruns-probe",
		Kind:  anthropic.Kind,
		Model: "default",
		Cred:  corellm.CredentialReference{Kind: "env", Locator: "ZZ_TURNRUNS_KEY"},
	}
	if err := stack.reg.LoadProfiles([]corellm.ProviderProfile{prof}); err != nil {
		t.Fatalf("LoadProfiles: %v", err)
	}

	ctx := context.Background()
	rec, err := c.SessionManager().Create(ctx, "zz-turnruns")
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	userRow, err := c.SessionManager().AppendMessage(ctx, rec.ID, session.Message{Role: session.RoleUser, Content: "hello"})
	if err != nil {
		t.Fatalf("append user row: %v", err)
	}
	runID, err := stack.chatRunner.StartStream(ctx, prof.ID, rec.ID, "",
		chat.UserTurn{MessageID: userRow.ID, Text: "hello", Announce: true})
	if err != nil {
		t.Fatalf("StartStream: %v", err)
	}

	// Read back through the production Sessions_TurnRuns path.
	sessAPI := newSessionsAPI(c, nil, nil, nil, nil)
	runs, err := sessAPI.TurnRuns(ctx, rec.ID)
	if err != nil {
		t.Fatalf("Sessions().TurnRuns: %v", err)
	}
	if len(runs) != 1 || runs[0].RunID != runID || runs[0].TurnSpanID == "" || runs[0].SpecDigest == "" {
		t.Fatalf("REGRESSION (M3): turn -> run mapping not recorded through the real wiring "+
			"(is `TurnRuns: turnRuns` still in newLLMStack's chat.Config?): got %+v, want run %q", runs, runID)
	}

	// RunView's poll for that same run, through the real graph manager.
	gapi := graphview.New(graphMgr)
	deadline := time.Now().Add(8 * time.Second)
	var st graphview.RunStatus
	for time.Now().Before(deadline) {
		st, err = gapi.GetRunStatus(ctx, runID)
		if err != nil {
			t.Fatalf("REGRESSION (H1): GetRunStatus(chat run %q): %v", runID, err)
		}
		if st.State == graphview.RunStateCompleted {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	if st.State != graphview.RunStateCompleted || st.SessionID != rec.ID || st.LLMCalls == 0 {
		t.Fatalf("chat run status = %+v, want completed in session %q with its LLM call counted", st, rec.ID)
	}
	trace, err := gapi.GetRunTrace(ctx, runID, 0)
	if err != nil || len(trace) == 0 {
		t.Fatalf("GetRunTrace(chat run) = %d events, err %v", len(trace), err)
	}
}
