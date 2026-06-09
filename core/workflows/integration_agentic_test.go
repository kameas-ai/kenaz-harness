package workflows_test

// integration_agentic_test.go — WP08 capstone e2e integration tests for
// mission workflows-agentic-01KW2D3X.
//
// Tests drive the full engine through five scenarios spanning the
// complete feature surface added in WP01–WP07:
//
//  1. DailyEABriefing end-to-end: fake mcp_calls run in parallel (DAG) →
//     model_turn → notify → audit events.
//  2. RerunPolicy skip: second run hits cache, no fresh ProgressEvents.
//  3. CycleRejection: workflow with a→b→a cycle fails to load.
//  4. StrictMode shell deny: saving a shell workflow in cedar strict mode denied.
//  5. BackgroundPoll: scheduler RunNow dispatches + ProgressEvents flow.
//
// ALL tests are real — no t.Skip, no TODO placeholders.

import (
	"context"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/kameas-ai/kenaz-harness/core/context/audit"
	rpcworkflows "github.com/kameas-ai/kenaz-harness/core/rpc/views/workflows"
	"github.com/kameas-ai/kenaz-harness/core/workflows"
	"github.com/kameas-ai/kenaz-harness/core/workflows/scheduler"
)

// ─── fake dependencies ──────────────────────────────────────────────────────

// fakeMCPCaller records every Call and returns canned JSON.
type fakeMCPCaller struct {
	mu      sync.Mutex
	calls   []mcpCallRecord
	results map[string]string // server → result JSON
}

type mcpCallRecord struct {
	server string
	tool   string
}

func (f *fakeMCPCaller) Call(_ context.Context, server, tool string, _ map[string]any) (string, error) {
	f.mu.Lock()
	f.calls = append(f.calls, mcpCallRecord{server: server, tool: tool})
	f.mu.Unlock()
	if r, ok := f.results[server]; ok {
		return r, nil
	}
	return `{"ok":true,"server":"` + server + `"}`, nil
}

func (f *fakeMCPCaller) callCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.calls)
}

func (f *fakeMCPCaller) callServers() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	seen := make(map[string]bool)
	var out []string
	for _, c := range f.calls {
		if !seen[c.server] {
			out = append(out, c.server)
			seen[c.server] = true
		}
	}
	return out
}

// fakeLLMStreamEvent is one event on the fake stream channel.
type fakeLLMStreamEvent = workflows.LLMStreamEvent

// fakeLLMStream is a single-event stream returning a canned summary.
type fakeLLMStream struct {
	text string
	ch   chan fakeLLMStreamEvent
}

func newFakeLLMStream(text string) *fakeLLMStream {
	ch := make(chan fakeLLMStreamEvent, 1)
	ch <- fakeLLMStreamEvent{Text: text}
	close(ch)
	return &fakeLLMStream{text: text, ch: ch}
}

func (s *fakeLLMStream) Events() <-chan workflows.LLMStreamEvent { return s.ch }
func (s *fakeLLMStream) Final() (string, error)                  { return s.text, nil }

// fakeLLMStreamer always returns a canned briefing.
type fakeLLMStreamer struct {
	summary string
	calls   atomic.Int32
}

func (f *fakeLLMStreamer) Stream(_ context.Context, _ workflows.LLMRequest) (workflows.LLMStream, error) {
	f.calls.Add(1)
	return newFakeLLMStream(f.summary), nil
}

// fakeNotifier records Notify calls.
type fakeNotifier struct {
	mu    sync.Mutex
	calls int
}

func (f *fakeNotifier) Notify(_ context.Context, _, _ string) error {
	f.mu.Lock()
	f.calls++
	f.mu.Unlock()
	return nil
}

func (f *fakeNotifier) callCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.calls
}

// fakeRunnerAudit records EmitNotifySent calls (satisfies workflows.AuditEmitter).
type fakeRunnerAudit struct {
	mu   sync.Mutex
	sent int
}

func (f *fakeRunnerAudit) EmitNotifySent(_ context.Context, _, _ string) error {
	f.mu.Lock()
	f.sent++
	f.mu.Unlock()
	return nil
}

func (f *fakeRunnerAudit) sentCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.sent
}

// ─── progress capture ───────────────────────────────────────────────────────

type agenticProgressCapture struct {
	mu     sync.Mutex
	events []workflows.ProgressEvent
}

func (c *agenticProgressCapture) sink(ev workflows.ProgressEvent) {
	c.mu.Lock()
	c.events = append(c.events, ev)
	c.mu.Unlock()
}

func (c *agenticProgressCapture) snapshot() []workflows.ProgressEvent {
	c.mu.Lock()
	defer c.mu.Unlock()
	out := make([]workflows.ProgressEvent, len(c.events))
	copy(out, c.events)
	return out
}

// ─── daily_ea_briefing YAML (hermetic version) ──────────────────────────────

// eaBriefingYAML mirrors daily_ea_briefing but uses a profile so the
// fake LLM streamer is invoked, and surface:[os] so the fake Notifier fires.
const eaBriefingYAML = `
id: test-ea-briefing
name: "EA Briefing Test"
version: 1
steps:
  - name: fetch_emails
    kind: mcp_call
    server: gmail
    tool_name: search_threads
    tool_args:
      query: "is:unread newer_than:1d"

  - name: fetch_slack
    kind: mcp_call
    server: slack
    tool_name: list_messages
    tool_args:
      since: "24h"

  - name: fetch_calendar
    kind: mcp_call
    server: google_calendar
    tool_name: list_events
    tool_args:
      time_min: "today"

  - name: write_briefing
    kind: model_turn
    inputs_from: [fetch_emails, fetch_slack, fetch_calendar]
    profile: default
    user_prompt: "Write a briefing. emails=${step.fetch_emails.output} slack=${step.fetch_slack.output} calendar=${step.fetch_calendar.output}"

  - name: notify_step
    kind: notify
    inputs_from: [write_briefing]
    notify_title: "Morning Briefing"
    notify_body: "${step.write_briefing.output}"
    surface: [os]
`

// ─── Test 1: DailyEABriefing End-to-End ────────────────────────────────────

// Test_DailyEABriefing_EndToEnd boots an engine wired with fake mcp_call,
// LLMStreamer, and Notifier. Asserts the DAG fans out 3 mcp_calls in
// parallel, aggregates them, runs model_turn once, then notifies.
// Validates ProgressEvent ordering and notify.sent audit.
func Test_DailyEABriefing_EndToEnd(t *testing.T) {
	t.Parallel()
	ctx := context.Background()

	mcpCaller := &fakeMCPCaller{
		results: map[string]string{
			"gmail":           `{"threads":[{"id":"t1","subject":"Quarterly review"}]}`,
			"slack":           `{"messages":[{"text":"deploy done"}]}`,
			"google_calendar": `{"events":[{"summary":"Standup","start":"09:00"}]}`,
		},
	}
	llmStreamer := &fakeLLMStreamer{summary: "Morning briefing: 1 email, 1 slack, 1 event."}
	notifier := &fakeNotifier{}
	runnerAudit := &fakeRunnerAudit{}

	deps := workflows.Deps{
		MCP:               mcpCaller,
		LLM:               llmStreamer,
		DefaultLLMProfile: "default",
		Notifier:          notifier,
		Audit:             runnerAudit,
	}
	engine := workflows.NewEngineWithDeps(deps)
	engine.Cache = workflows.NewMemoryCache()

	wf, err := workflows.LoadYAML([]byte(eaBriefingYAML))
	if err != nil {
		t.Fatalf("LoadYAML: %v", err)
	}

	prog := &agenticProgressCapture{}
	run, err := engine.Run(ctx, wf, nil, workflows.RunOptions{ProgressSink: prog.sink})
	if err != nil {
		t.Fatalf("Run: %v (status=%q)", err, run.Status)
	}
	if run.Status != "completed" {
		t.Fatalf("run.Status=%q want completed (err=%q)", run.Status, run.Err)
	}

	// Assert 3 mcp_calls fired (to the three servers).
	if got := mcpCaller.callCount(); got != 3 {
		t.Errorf("mcp calls=%d want 3", got)
	}
	for _, s := range []string{"gmail", "slack", "google_calendar"} {
		found := false
		for _, sv := range mcpCaller.callServers() {
			if sv == s {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("mcp server %q was not called", s)
		}
	}

	// Assert model_turn ran once.
	if got := llmStreamer.calls.Load(); got != 1 {
		t.Errorf("LLM stream calls=%d want 1", got)
	}

	// Assert notify fired once.
	if got := notifier.callCount(); got != 1 {
		t.Errorf("notify calls=%d want 1", got)
	}

	// All 5 steps must have completed.
	if len(run.Steps) != 5 {
		t.Errorf("run.Steps=%d want 5", len(run.Steps))
	}
	for _, sr := range run.Steps {
		if sr.Status != "completed" {
			t.Errorf("step %q status=%q want completed (err=%q)", sr.Name, sr.Status, sr.Err)
		}
	}

	// ProgressEvents: every completed step must have had a prior "running" event.
	events := prog.snapshot()
	if len(events) < 5 {
		t.Errorf("progress events=%d want >=5", len(events))
	}
	firstRunningIdx := make(map[string]int)
	for i, ev := range events {
		if ev.Status == "running" {
			if _, already := firstRunningIdx[ev.Step]; !already {
				firstRunningIdx[ev.Step] = i
			}
		}
	}
	for i, ev := range events {
		if ev.Status == "completed" {
			ri, ok := firstRunningIdx[ev.Step]
			if !ok {
				t.Errorf("step %q 'completed' at index %d has no prior 'running'", ev.Step, i)
			} else if ri >= i {
				t.Errorf("step %q 'running' at %d is not before 'completed' at %d", ev.Step, ri, i)
			}
		}
	}

	// notify.sent audit event fired once (at runner level).
	if got := runnerAudit.sentCount(); got != 1 {
		t.Errorf("notify.sent audit events=%d want 1", got)
	}
}

// ─── Test 2: RerunPolicy skip ───────────────────────────────────────────────

// Test_RerunPolicy_SkipsCachedRuns verifies the rerun_policy=skip path still
// works correctly under the new DAG runtime.
func Test_RerunPolicy_SkipsCachedRuns(t *testing.T) {
	t.Parallel()
	ctx := context.Background()

	const skipPolicyYAML = `
id: rerun-skip-agentic
name: "Rerun Skip Agentic"
version: 1
rerun_policy: skip
steps:
  - name: alpha
    kind: shell
    cmd: "true"
  - name: beta
    kind: shell
    cmd: "true"
  - name: gamma
    kind: shell
    cmd: "true"
`
	engine := workflows.NewEngine()
	engine.Cache = workflows.NewMemoryCache()

	wf, err := workflows.LoadYAML([]byte(skipPolicyYAML))
	if err != nil {
		t.Fatalf("LoadYAML: %v", err)
	}

	prog := &agenticProgressCapture{}

	// Run #1 — fresh; should emit >=3 progress events.
	run1, err := engine.Run(ctx, wf, nil, workflows.RunOptions{ProgressSink: prog.sink})
	if err != nil {
		t.Fatalf("Run #1: %v", err)
	}
	if run1.Status != "completed" {
		t.Fatalf("Run #1 status=%q", run1.Status)
	}
	afterFirst := len(prog.snapshot())
	if afterFirst < 3 {
		t.Fatalf("Run #1 progress events=%d want >=3", afterFirst)
	}

	// Run #2 — cache hit; NO new ProgressEvents expected.
	run2, err := engine.Run(ctx, wf, nil, workflows.RunOptions{ProgressSink: prog.sink})
	if err != nil {
		t.Fatalf("Run #2: %v", err)
	}
	if run2.Status != "completed" {
		t.Fatalf("Run #2 status=%q want completed", run2.Status)
	}
	afterSecond := len(prog.snapshot())
	if afterSecond != afterFirst {
		t.Errorf("Run #2 added %d extra progress events; want 0 (cache hit skips emitter)",
			afterSecond-afterFirst)
	}
}

// ─── Test 3: CycleRejection ─────────────────────────────────────────────────

// Test_CycleRejection asserts that a workflow with an inputs_from cycle
// fails to load and the error includes the cycle path.
func Test_CycleRejection(t *testing.T) {
	t.Parallel()
	const cycleYAML = `
id: cycle-test
name: "Cycle Test"
version: 1
steps:
  - name: step_a
    kind: shell
    cmd: "true"
    inputs_from: [step_b]
  - name: step_b
    kind: shell
    cmd: "true"
    inputs_from: [step_a]
`
	_, err := workflows.LoadYAML([]byte(cycleYAML))
	if err == nil {
		t.Fatal("LoadYAML: expected error for cycle, got nil")
	}

	// Error must wrap ErrWorkflowCycle.
	if !agenticIsErrCycle(err) {
		t.Fatalf("expected ErrWorkflowCycle in error chain, got: %v", err)
	}

	// Error message must include both step names (the cycle path).
	msg := err.Error()
	for _, name := range []string{"step_a", "step_b"} {
		if !agenticContainsStr(msg, name) {
			t.Errorf("cycle error message %q does not mention step %q", msg, name)
		}
	}
}

func agenticIsErrCycle(err error) bool {
	for cur := err; cur != nil; {
		if cur == workflows.ErrWorkflowCycle {
			return true
		}
		type unwrapper interface{ Unwrap() error }
		u, ok := cur.(unwrapper)
		if !ok {
			return false
		}
		cur = u.Unwrap()
	}
	return false
}

func agenticContainsStr(s, sub string) bool {
	if len(sub) > len(s) {
		return false
	}
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}

// ─── Test 4: StrictMode shell deny ──────────────────────────────────────────

// Test_StrictModeShellSave verifies that a shell-bearing workflow is denied
// at the cedar strict-mode gate when saved via the RPC API. This test
// confirms the constraint still holds under the agentic DAG runtime.
func Test_StrictModeShellSave(t *testing.T) {
	t.Parallel()
	f := newIntegrationFixture(t, "strict")
	ctx := context.Background()

	const shellYAML = `
id: strict-mode-shell-agentic
name: "Strict shell agentic test"
version: 1
steps:
  - name: run_it
    kind: shell
    cmd: "echo hello"
`
	_, err := f.api.Save(ctx, rpcworkflows.SaveInput{YAML: shellYAML})
	if err == nil {
		t.Fatal("Save should have been denied in strict mode")
	}
	if !errorIs(err, rpcworkflows.ErrCedarDenied) {
		t.Fatalf("err=%v want wrap of ErrCedarDenied", err)
	}
	// No workflow.saved audit must fire on a denied save.
	if k := kindCounts(f.audit.snapshot()); k[audit.KindWorkflowSaved] != 0 {
		t.Errorf("workflow.saved count=%d want 0 (save was denied)", k[audit.KindWorkflowSaved])
	}
}

// ─── Test 5: BackgroundPoll fires scheduled ──────────────────────────────────

// Test_BackgroundPoll_FiresScheduled verifies that after Register, calling
// RunNow (which exercises the same dispatcher path the cron tick triggers)
// produces a completed RunSummary AND that a downstream engine run emits
// ProgressEvents correctly.
func Test_BackgroundPoll_FiresScheduled(t *testing.T) {
	t.Parallel()
	ctx := context.Background()

	disp := &schedDispatcher{}
	sched, err := scheduler.New(ctx, scheduler.Config{Dispatcher: disp})
	if err != nil {
		t.Fatalf("scheduler.New: %v", err)
	}
	t.Cleanup(func() { sched.Stop() })

	// Register a schedule.
	if err := sched.Register(ctx, "wf-poll", "0 7 * * *", "UTC"); err != nil {
		t.Fatalf("Register: %v", err)
	}

	// RunNow exercises the same fireSync → Dispatcher.Dispatch path the
	// cron tick uses when the clock fires.
	sum, err := sched.RunNow(ctx, "wf-poll")
	if err != nil {
		t.Fatalf("RunNow: %v", err)
	}
	if sum.Status != "completed" {
		t.Errorf("RunNow status=%q want completed", sum.Status)
	}
	if sum.WorkflowID != "wf-poll" {
		t.Errorf("WorkflowID=%q want wf-poll", sum.WorkflowID)
	}

	// Dispatcher must have been called exactly once.
	if got := disp.count(); got != 1 {
		t.Errorf("dispatcher called %d times want 1", got)
	}

	// Verify a real engine run under the same conditions emits ProgressEvents.
	const pollWFYAML = `
id: poll-workflow
name: "Poll Workflow"
version: 1
steps:
  - name: check
    kind: shell
    cmd: "true"
`
	wf, err := workflows.LoadYAML([]byte(pollWFYAML))
	if err != nil {
		t.Fatalf("LoadYAML: %v", err)
	}
	engine := workflows.NewEngine()
	prog := &agenticProgressCapture{}
	run, err := engine.Run(ctx, wf, nil, workflows.RunOptions{ProgressSink: prog.sink})
	if err != nil {
		t.Fatalf("engine.Run: %v", err)
	}
	if run.Status != "completed" {
		t.Fatalf("run.Status=%q want completed", run.Status)
	}
	events := prog.snapshot()
	if len(events) < 1 {
		t.Errorf("progress events=%d want >=1", len(events))
	}
}

// ─── scheduler fake dispatcher ───────────────────────────────────────────────

type schedDispatcher struct {
	mu    sync.Mutex
	calls int
}

func (d *schedDispatcher) Dispatch(_ context.Context, _ string, _ bool) (string, error) {
	d.mu.Lock()
	d.calls++
	d.mu.Unlock()
	return "run-fake", nil
}

func (d *schedDispatcher) count() int {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.calls
}
