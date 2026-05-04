package workflows

import (
	"context"
	"strings"
	"testing"
)

// =============================================================================
// Fakes for notify tests
// =============================================================================

// fakeNotifier records calls to Notify.
type fakeNotifier struct {
	calls []notifyCall
	err   error
}

type notifyCall struct {
	title, body string
}

func (f *fakeNotifier) Notify(_ context.Context, title, body string) error {
	f.calls = append(f.calls, notifyCall{title: title, body: body})
	return f.err
}

// fakeAudit records EmitNotifySent calls.
type fakeAudit struct {
	events []auditEvent
}

type auditEvent struct {
	target, title string
}

func (f *fakeAudit) EmitNotifySent(_ context.Context, target, title string) error {
	f.events = append(f.events, auditEvent{target: target, title: title})
	return nil
}

// =============================================================================
// Notify runner tests
// =============================================================================

func TestNotify_OSSurfaceCallsNotifierOnce(t *testing.T) {
	notif := &fakeNotifier{}
	audit := &fakeAudit{}

	r := notifyRunner{notifier: notif, audit: audit}
	st := Step{
		Name:        "ping",
		Kind:        StepKindNotify,
		NotifyTitle: "Hello",
		NotifyBody:  "World body",
		Surface:     []string{"os"},
	}
	rc := &RunContext{Inputs: map[string]TypedValue{}, StepOutputs: map[string]TypedValue{}}
	_, err := r.Run(context.Background(), st, rc)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if len(notif.calls) != 1 {
		t.Fatalf("notifier called %d times, want 1", len(notif.calls))
	}
	if notif.calls[0].title != "Hello" {
		t.Errorf("title: got %q want %q", notif.calls[0].title, "Hello")
	}
}

func TestNotify_OsAndSlack_BothDispatched_TwoAuditEvents(t *testing.T) {
	notif := &fakeNotifier{}
	audit := &fakeAudit{}

	// Provide a fake MCP that succeeds for any server.
	mcp := &fakeMCP{out: "ok"}

	r := notifyRunner{notifier: notif, mcp: mcp, audit: audit}
	st := Step{
		Name:        "multi",
		Kind:        StepKindNotify,
		NotifyTitle: "Alert",
		NotifyBody:  "Secret body text",
		Surface:     []string{"os", "slack"},
	}
	rc := &RunContext{Inputs: map[string]TypedValue{}, StepOutputs: map[string]TypedValue{}}
	_, err := r.Run(context.Background(), st, rc)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	// Both surfaces dispatched.
	if len(notif.calls) != 1 {
		t.Errorf("os notifier calls: got %d want 1", len(notif.calls))
	}
	// Two audit events emitted (one per surface).
	if len(audit.events) != 2 {
		t.Errorf("audit events: got %d want 2", len(audit.events))
	}
	targets := map[string]bool{}
	for _, ev := range audit.events {
		targets[ev.target] = true
	}
	if !targets["os"] || !targets["slack"] {
		t.Errorf("audit targets: got %v, want both os and slack", targets)
	}
}

func TestNotify_UnconfiguredSlack_OsSucceeds_RunNotFailed(t *testing.T) {
	notif := &fakeNotifier{}
	audit := &fakeAudit{}
	// No MCP wired → slack will get ErrNotifyTargetUnconfigured.

	r := notifyRunner{notifier: notif, mcp: nil, audit: audit}
	st := Step{
		Name:        "partial",
		Kind:        StepKindNotify,
		NotifyTitle: "Hi",
		NotifyBody:  "body",
		Surface:     []string{"os", "slack"},
	}
	rc := &RunContext{Inputs: map[string]TypedValue{}, StepOutputs: map[string]TypedValue{}}
	out, err := r.Run(context.Background(), st, rc)
	// Run must NOT fail — os succeeded.
	if err != nil {
		t.Fatalf("Run should not fail when os succeeds: %v", err)
	}
	// Output JSON should contain the unconfigured status for slack.
	if !strings.Contains(out.Text, "unconfigured") {
		t.Errorf("output should mention unconfigured: %q", out.Text)
	}
	// os notifier was still called.
	if len(notif.calls) != 1 {
		t.Errorf("os notifier: got %d calls, want 1", len(notif.calls))
	}
	// Only one audit event (for os) — slack didn't succeed.
	if len(audit.events) != 1 {
		t.Errorf("audit events: got %d, want 1 (os only)", len(audit.events))
	}
}

func TestNotify_AuditDoesNotContainBody(t *testing.T) {
	notif := &fakeNotifier{}
	audit := &fakeAudit{}

	r := notifyRunner{notifier: notif, audit: audit}
	st := Step{
		Name:        "priv",
		Kind:        StepKindNotify,
		NotifyTitle: "Short title",
		NotifyBody:  "This is highly confidential body content that must never appear in audit",
		Surface:     []string{"os"},
	}
	rc := &RunContext{Inputs: map[string]TypedValue{}, StepOutputs: map[string]TypedValue{}}
	_, err := r.Run(context.Background(), st, rc)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if len(audit.events) == 0 {
		t.Fatalf("expected at least one audit event")
	}
	for _, ev := range audit.events {
		// Audit title must be present (truncated).
		if ev.title == "" {
			t.Errorf("audit title is empty")
		}
		// Body must NEVER appear in any audit field.
		if strings.Contains(ev.title, "confidential") {
			t.Errorf("audit title contains body content: %q", ev.title)
		}
	}
}

func TestNotify_TitleTruncatedInAudit(t *testing.T) {
	notif := &fakeNotifier{}
	audit := &fakeAudit{}

	longTitle := strings.Repeat("A", notifyTitleAuditMaxLen+20)
	r := notifyRunner{notifier: notif, audit: audit}
	st := Step{
		Name:        "trunc",
		Kind:        StepKindNotify,
		NotifyTitle: longTitle,
		NotifyBody:  "body",
		Surface:     []string{"os"},
	}
	rc := &RunContext{Inputs: map[string]TypedValue{}, StepOutputs: map[string]TypedValue{}}
	_, err := r.Run(context.Background(), st, rc)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if len(audit.events) == 0 {
		t.Fatalf("no audit events emitted")
	}
	ev := audit.events[0]
	if len(ev.title) > notifyTitleAuditMaxLen {
		t.Errorf("audit title too long: got %d chars, want ≤%d", len(ev.title), notifyTitleAuditMaxLen)
	}
}

// =============================================================================
// End-to-end via Engine
// =============================================================================

func TestNotify_EndToEnd_OSOnly(t *testing.T) {
	notif := &fakeNotifier{}
	wf := Workflow{
		ID: "n", Name: "n", Version: 1,
		Steps: []Step{
			{
				Name:        "send",
				Kind:        StepKindNotify,
				NotifyTitle: "Test",
				NotifyBody:  "body",
				Surface:     []string{"os"},
			},
		},
	}
	e := NewEngineWithDeps(Deps{Notifier: notif})
	run, err := e.Run(context.Background(), wf, nil, RunOptions{})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if run.Status != "completed" {
		t.Fatalf("status: %s err=%s", run.Status, run.Err)
	}
	if len(notif.calls) != 1 {
		t.Errorf("notifier calls: got %d want 1", len(notif.calls))
	}
}
