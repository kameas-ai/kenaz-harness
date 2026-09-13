package rpc

// wf_notify_audit_bridge_test.go — automation-actually-runs-01PMZ404
// UNIT-8 (AC-009). corewf.Deps.Audit was never assigned in production,
// so notifyRunner's EmitNotifySent call (core/workflows/
// runners_notify.go:149) was always a silent no-op regardless of
// whether the notification itself succeeded — the one workflow step
// kind that reaches outside the process had no audit trail.
//
// Per AC-009's own wording ("a workflow with one notify step and a
// FAKE OS notifier"), the OS/MCP notification transport itself is
// faked — probed separately (TestProbeSendNotificationNoContext-shaped
// investigation, not committed) and confirmed to abort the test binary
// outright when driven through the real Wails runtime with no app
// context, which is exactly why the AC specifies a fake there. What
// this test drives for REAL is everything downstream of "the
// notification surface reported success": the real wfNotifyAuditBridge
// bound to a real rpc/views/audit.API ring (audit.NewAPI(), not a
// hand-rolled capture emitter — CLAUDE.md blind spot #2), through the
// real corewf.Engine.

import (
	"context"
	"strings"
	"testing"

	auditview "github.com/kameas-ai/kenaz-harness/core/rpc/views/audit"
	corewf "github.com/kameas-ai/kenaz-harness/core/workflows"
)

// fakeOSNotifier is the "fake OS notifier" AC-009 asks for — the real
// wfNotifierAdapter cannot run outside a live Wails app context (it
// calls os.Exit via the Wails runtime helper when the context is
// invalid, which would abort the test binary, not merely fail an
// assertion).
type fakeOSNotifier struct{ err error }

func (f fakeOSNotifier) Notify(_ context.Context, _, _ string) error { return f.err }

const wfNotifyProbeYAML = `
id: zz-wfnotify-probe
name: "wf notify probe"
version: 1
steps:
  - name: alert
    kind: notify
    notify_title: "this-is-a-very-long-title-that-exceeds-the-sixty-character-audit-truncation-limit-by-a-wide-margin"
    notify_body: "SECRET-BODY-MUST-NEVER-APPEAR-IN-AUDIT"
    surface: ["os"]
`

// TestWfNotifyAuditBridge_EmitsExactlyOneRecord drives a real
// corewf.Engine with the real wfNotifyAuditBridge over a real
// audit.API ring. Asserts:
//   - exactly one workflow.notify_sent entry
//   - it names the target ("os")
//   - it carries the TRUNCATED title (≤60 chars)
//   - the notification body never appears anywhere in the entry
//
// Mutation (run and recorded): set Audit: nil in the Deps below —
// zero entries. Set Notifier to return an error — zero entries (the
// unconfigured/error branch never calls emitSent). Both confirm the
// assertion is actually exercising the audit path, not a tautology.
func TestWfNotifyAuditBridge_EmitsExactlyOneRecord(t *testing.T) {
	ctx := context.Background()
	auditAPI := auditview.NewAPI()

	wf, err := corewf.LoadYAML([]byte(wfNotifyProbeYAML))
	if err != nil {
		t.Fatalf("LoadYAML: %v", err)
	}

	engine := corewf.NewEngineWithDeps(corewf.Deps{
		Notifier: fakeOSNotifier{},
		Audit:    &wfNotifyAuditBridge{impl: auditAPI},
	})

	run, err := engine.Run(ctx, wf, nil, corewf.RunOptions{})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if run.Status != "completed" {
		t.Fatalf("run status = %q, want completed (err=%q)", run.Status, run.Err)
	}

	entries, err := auditAPI.ListEntries(ctx, auditview.Filter{Categories: []string{"WORKFLOW"}, Limit: 50})
	if err != nil {
		t.Fatalf("ListEntries: %v", err)
	}
	var found []auditview.Entry
	for _, e := range entries {
		if e.Subject == "workflow.notify_sent" {
			found = append(found, e)
		}
	}
	if len(found) != 1 {
		t.Fatalf("got %d workflow.notify_sent entries, want exactly 1: %#v", len(found), entries)
	}
	entry := found[0]
	if !strings.Contains(entry.Trailing, "target=os") {
		t.Errorf("entry.Trailing = %q, want it to name the target (os)", entry.Trailing)
	}
	if strings.Contains(entry.Trailing, "SECRET-BODY-MUST-NEVER-APPEAR-IN-AUDIT") {
		t.Fatalf("entry.Trailing = %q, LEAKS THE NOTIFICATION BODY — privacy invariant violated", entry.Trailing)
	}
	// The title in the YAML is 101 chars; notifyTitleAuditMaxLen truncates to 60.
	if strings.Contains(entry.Trailing, "by-a-wide-margin") {
		t.Errorf("entry.Trailing = %q, contains text past the 60-char truncation point — title was not truncated", entry.Trailing)
	}
	if !strings.Contains(entry.Trailing, "this-is-a-very-long-title") {
		t.Errorf("entry.Trailing = %q, want the (truncated) title prefix present", entry.Trailing)
	}
}

// TestWfNotifyAuditBridge_NilAudit_NoRecord is the negative control:
// with Deps.Audit nil (the pre-UNIT-8 production state), no audit
// record is produced even though the notification itself succeeds.
func TestWfNotifyAuditBridge_NilAudit_NoRecord(t *testing.T) {
	ctx := context.Background()
	wf, err := corewf.LoadYAML([]byte(wfNotifyProbeYAML))
	if err != nil {
		t.Fatalf("LoadYAML: %v", err)
	}
	engine := corewf.NewEngineWithDeps(corewf.Deps{Notifier: fakeOSNotifier{}})
	run, err := engine.Run(ctx, wf, nil, corewf.RunOptions{})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if run.Status != "completed" {
		t.Fatalf("run status = %q, want completed", run.Status)
	}
	// Nothing to assert against a ring — Deps.Audit is nil, so there is
	// no sink at all. This test's value is that Run still succeeds
	// (notify's own audit call must never fail the step) — the shape
	// notifyRunner.emitSent guards with `if r.audit == nil { return }`.
}
