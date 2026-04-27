package agentgraph_test

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	graphview "github.com/sigil-tech/kaneaz-harness/core/rpc/views/agentgraph"
)

func newTestAPI(t *testing.T) (*graphview.Impl, *graphview.Manager, string) {
	t.Helper()
	dir := t.TempDir()
	mgr, err := graphview.NewManager(graphview.WithDataDir(dir))
	if err != nil {
		t.Fatalf("NewManager: %v", err)
	}
	return graphview.New(mgr), mgr, dir
}

// TestNilManager exercises the empty-state surface.
func TestNilManager(t *testing.T) {
	t.Parallel()
	a := graphview.New(nil)
	ctx := context.Background()
	if _, err := a.ListGraphs(ctx, ""); !errors.Is(err, graphview.ErrManagerUnavailable) {
		t.Errorf("ListGraphs err = %v", err)
	}
	if err := a.SaveGraph(ctx, graphview.GraphSpec{ID: "x"}); !errors.Is(err, graphview.ErrManagerUnavailable) {
		t.Errorf("SaveGraph err = %v", err)
	}
	if _, err := a.GetRunStatus(ctx, "r"); !errors.Is(err, graphview.ErrManagerUnavailable) {
		t.Errorf("GetRunStatus err = %v", err)
	}
}

// TestListBundled asserts the bundled toolloop_default graph is listed
// and loadable via the API.
func TestListBundled(t *testing.T) {
	t.Parallel()
	a, _, _ := newTestAPI(t)
	ctx := context.Background()
	rows, err := a.ListGraphs(ctx, "")
	if err != nil {
		t.Fatalf("ListGraphs: %v", err)
	}
	found := false
	for _, r := range rows {
		if r.ID == "toolloop_default" && r.Scope == "library" {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("toolloop_default not listed; rows = %+v", rows)
	}
	spec, err := a.LoadGraph(ctx, "toolloop_default")
	if err != nil {
		t.Fatalf("LoadGraph: %v", err)
	}
	if !strings.Contains(spec.YAML, "toolloop_default") {
		t.Errorf("YAML missing id; got %q", spec.YAML[:64])
	}
	if spec.Scope != "library" {
		t.Errorf("scope = %q want library", spec.Scope)
	}
}

// TestChatDefaultBundled asserts the chat_default library entry the
// agent-kernel-graph-chat-migration mission ships parses cleanly and
// is listed alongside toolloop_default. This is the new chassis chat
// path; the chassis runner picks it as the default graph for any
// session without an explicit override.
func TestChatDefaultBundled(t *testing.T) {
	t.Parallel()
	a, _, _ := newTestAPI(t)
	ctx := context.Background()
	rows, err := a.ListGraphs(ctx, "")
	if err != nil {
		t.Fatalf("ListGraphs: %v", err)
	}
	found := false
	for _, r := range rows {
		if r.ID == "chat_default" && r.Scope == "library" {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("chat_default not listed; rows = %+v", rows)
	}
	spec, err := a.LoadGraph(ctx, "chat_default")
	if err != nil {
		t.Fatalf("LoadGraph(chat_default): %v", err)
	}
	if !strings.Contains(spec.YAML, "chat_default") {
		t.Errorf("YAML missing id; got %q", spec.YAML[:64])
	}
	if !strings.Contains(spec.YAML, "max_iterations: 25") {
		t.Errorf("chat_default should default LoopNode max_iterations to 25 per mission brief")
	}
}

// TestSaveAndDeleteUserGraph walks the user-graph CRUD round-trip.
func TestSaveAndDeleteUserGraph(t *testing.T) {
	t.Parallel()
	a, _, dir := newTestAPI(t)
	ctx := context.Background()
	id := "my_user_graph"
	yaml := `spec_version: "1"
id: ` + id + `
entrypoints: [a]
nodes:
  - id: a
    kind: plan
    attrs:
      verbosity: terse
`
	if err := a.SaveGraph(ctx, graphview.GraphSpec{ID: id, YAML: yaml}); err != nil {
		t.Fatalf("SaveGraph: %v", err)
	}
	rows, err := a.ListGraphs(ctx, "user")
	if err != nil {
		t.Fatalf("ListGraphs: %v", err)
	}
	found := false
	for _, r := range rows {
		if r.ID == id && r.Scope == "user" {
			found = true
		}
	}
	if !found {
		t.Errorf("user graph not listed; got %+v", rows)
	}
	// File present on disk
	full := filepath.Join(dir, "agent_graph", "library", id+".yaml")
	if _, err := os.Stat(full); err != nil {
		t.Errorf("file not on disk: %v", err)
	}
	// LoadGraph prefers user version.
	spec, err := a.LoadGraph(ctx, id)
	if err != nil {
		t.Fatalf("LoadGraph: %v", err)
	}
	if spec.Scope != "user" {
		t.Errorf("scope = %q want user", spec.Scope)
	}
	// Delete
	if err := a.DeleteGraph(ctx, id); err != nil {
		t.Fatalf("DeleteGraph: %v", err)
	}
	if _, err := os.Stat(full); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("file still present after delete: %v", err)
	}
}

// TestSaveBundledRejected asserts the user can't shadow a library id.
func TestSaveBundledRejected(t *testing.T) {
	t.Parallel()
	a, _, _ := newTestAPI(t)
	ctx := context.Background()
	yaml := `spec_version: "1"
id: toolloop_default
entrypoints: [a]
nodes:
  - id: a
    kind: plan
    attrs:
      verbosity: terse
`
	err := a.SaveGraph(ctx, graphview.GraphSpec{ID: "toolloop_default", YAML: yaml})
	if err == nil || !strings.Contains(err.Error(), "bundled") {
		t.Errorf("expected bundled rejection; got %v", err)
	}
}

// TestValidate exercises both the green + red paths.
func TestValidate(t *testing.T) {
	t.Parallel()
	a, _, _ := newTestAPI(t)
	ctx := context.Background()
	good := `spec_version: "1"
id: g
entrypoints: [a]
nodes:
  - id: a
    kind: plan
    attrs:
      verbosity: terse
`
	res, err := a.Validate(ctx, good)
	if err != nil {
		t.Fatalf("Validate good: %v", err)
	}
	if !res.OK {
		t.Errorf("good graph not OK: %+v", res.Issues)
	}
	bad := `spec_version: "1"
id: g
entrypoints: [missing]
nodes:
  - id: a
    kind: plan
    attrs:
      verbosity: standard
`
	res2, err := a.Validate(ctx, bad)
	if err != nil {
		t.Fatalf("Validate bad: %v", err)
	}
	if res2.OK {
		t.Errorf("bad graph reported OK")
	}
	if len(res2.Issues) == 0 {
		t.Errorf("bad graph reported no issues")
	}
}

// TestStartRunCompletes runs a tiny graph end-to-end.
func TestStartRunCompletes(t *testing.T) {
	t.Parallel()
	a, _, _ := newTestAPI(t)
	ctx := context.Background()
	yaml := `spec_version: "1"
id: tiny
entrypoints: [t]
nodes:
  - id: t
    kind: transform
    attrs:
      name: concat
      params:
        sep: "-"
`
	if err := a.SaveGraph(ctx, graphview.GraphSpec{ID: "tiny", YAML: yaml}); err != nil {
		t.Fatalf("SaveGraph: %v", err)
	}
	resp, err := a.StartRun(ctx, graphview.StartRunRequest{GraphID: "tiny"})
	if err != nil {
		t.Fatalf("StartRun: %v", err)
	}
	if resp.RunID == "" {
		t.Fatalf("empty run id")
	}

	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		st, err := a.GetRunStatus(ctx, resp.RunID)
		if err != nil {
			t.Fatalf("GetRunStatus: %v", err)
		}
		if st.State == graphview.RunStateCompleted {
			break
		}
		if st.State == graphview.RunStateFailed {
			t.Fatalf("run failed: %s", st.Error)
		}
		time.Sleep(10 * time.Millisecond)
	}
	st, _ := a.GetRunStatus(ctx, resp.RunID)
	if st.State != graphview.RunStateCompleted {
		t.Fatalf("final state = %q (err=%q)", st.State, st.Error)
	}
	// Trace tail should contain run_start and run_complete.
	events, err := a.GetRunTrace(ctx, resp.RunID, 0)
	if err != nil {
		t.Fatalf("GetRunTrace: %v", err)
	}
	if len(events) == 0 {
		t.Fatalf("no events")
	}
	hasStart := false
	hasComplete := false
	for _, ev := range events {
		if ev.Kind == "run_start" {
			hasStart = true
		}
		if ev.Kind == "run_complete" {
			hasComplete = true
		}
	}
	if !hasStart || !hasComplete {
		t.Errorf("missing lifecycle events; got %d events", len(events))
	}
}

// TestPauseAndResume exercises the AskNode pause + resume cycle through
// the API surface.
func TestPauseAndResume(t *testing.T) {
	t.Parallel()
	a, _, _ := newTestAPI(t)
	ctx := context.Background()
	yaml := `spec_version: "1"
id: ask_only
entrypoints: [q]
nodes:
  - id: q
    kind: ask
    attrs:
      question: "What is your name?"
`
	if err := a.SaveGraph(ctx, graphview.GraphSpec{ID: "ask_only", YAML: yaml}); err != nil {
		t.Fatalf("SaveGraph: %v", err)
	}
	resp, err := a.StartRun(ctx, graphview.StartRunRequest{GraphID: "ask_only"})
	if err != nil {
		t.Fatalf("StartRun: %v", err)
	}
	deadline := time.Now().Add(2 * time.Second)
	var st graphview.RunStatus
	for time.Now().Before(deadline) {
		st, err = a.GetRunStatus(ctx, resp.RunID)
		if err != nil {
			t.Fatalf("GetRunStatus: %v", err)
		}
		if st.State == graphview.RunStatePaused {
			break
		}
		if st.State == graphview.RunStateFailed {
			t.Fatalf("run failed pre-pause: %s", st.Error)
		}
		time.Sleep(10 * time.Millisecond)
	}
	if st.State != graphview.RunStatePaused {
		t.Fatalf("did not pause; state=%q", st.State)
	}
	if st.PendingAsk == nil || st.PendingAsk.NodeID != "q" {
		t.Fatalf("pending ask = %+v", st.PendingAsk)
	}
	if err := a.Resume(ctx, resp.RunID, "Alice"); err != nil {
		t.Fatalf("Resume: %v", err)
	}
	deadline = time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		st, _ = a.GetRunStatus(ctx, resp.RunID)
		if st.State == graphview.RunStateCompleted || st.State == graphview.RunStateFailed {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if st.State != graphview.RunStateCompleted {
		t.Fatalf("post-resume state=%q err=%q", st.State, st.Error)
	}
}

// TestCancelRun signals an unstarted run to fail; the fact that we can
// cancel a completed run cleanly is also exercised.
func TestCancelRun(t *testing.T) {
	t.Parallel()
	a, _, _ := newTestAPI(t)
	ctx := context.Background()
	yaml := `spec_version: "1"
id: ask_cancel
entrypoints: [q]
nodes:
  - id: q
    kind: ask
    attrs:
      question: "park here"
`
	if err := a.SaveGraph(ctx, graphview.GraphSpec{ID: "ask_cancel", YAML: yaml}); err != nil {
		t.Fatalf("SaveGraph: %v", err)
	}
	resp, err := a.StartRun(ctx, graphview.StartRunRequest{GraphID: "ask_cancel"})
	if err != nil {
		t.Fatalf("StartRun: %v", err)
	}
	// Wait for pause then cancel.
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		st, _ := a.GetRunStatus(ctx, resp.RunID)
		if st.State == graphview.RunStatePaused {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if err := a.CancelRun(ctx, resp.RunID); err != nil {
		t.Fatalf("CancelRun: %v", err)
	}
	st, _ := a.GetRunStatus(ctx, resp.RunID)
	if st.State != graphview.RunStateFailed {
		t.Errorf("post-cancel state = %q want failed", st.State)
	}
}
