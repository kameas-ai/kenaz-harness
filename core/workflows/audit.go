// audit.go — workflow-family audit emission helpers
// (mission workflows-01KQ8TDG, WP11).
//
// These helpers are thin wrappers around audit.Emit so the workflows
// RPC impl + engine layer can fire events without re-implementing the
// payload-marshalling boilerplate. The privacy invariant lives here:
// every payload field is either an id, a kind, or a classification
// metric — step inputs / outputs / prompt bytes never appear.
package workflows

import (
	"context"
	"errors"
	"strings"
	"time"

	"github.com/kameas-ai/kenaz-harness/core/context/audit"
)

// EmitExecuted records a completed Run (success or failure) on the
// audit log. duration is the wall-clock time from Run start to end.
//
// Caller wires audit.Emitter through the workflows RPC config; nil is
// a no-op so test harnesses can run without a wired emitter.
func EmitExecuted(ctx context.Context, em audit.Emitter, r *Run) {
	if em == nil || r == nil {
		return
	}
	dur := r.EndedAt.Sub(r.StartedAt)
	if dur < 0 {
		dur = 0
	}
	audit.MustEmit(ctx, em, audit.KindWorkflowExecuted,
		audit.WorkflowExecutedPayload{
			WorkflowID: r.WorkflowID,
			RunID:      r.ID,
			Status:     r.Status,
			DurationMs: dur.Milliseconds(),
			StepCount:  len(r.Steps),
		}, time.Now().UTC())
}

// EmitStepFailures walks r.Steps and emits one workflow.step_failed
// event per failed step. The error message itself is NEVER recorded;
// classifyStepError maps the raw error onto a typed class label so the
// audit log stays free of user-input bytes (privacy invariant).
func EmitStepFailures(ctx context.Context, em audit.Emitter, r *Run) {
	if em == nil || r == nil {
		return
	}
	for _, s := range r.Steps {
		if s.Status != "failed" {
			continue
		}
		audit.MustEmit(ctx, em, audit.KindWorkflowStepFailed,
			audit.WorkflowStepFailedPayload{
				WorkflowID: r.WorkflowID,
				RunID:      r.ID,
				StepID:     s.Name,
				StepKind:   string(s.Kind),
				ErrorClass: classifyStepError(s.Err),
			}, time.Now().UTC())
	}
}

// emitNetworkFetch records one KindWorkflowNetworkFetch event
// (core/context/audit/audit.go:108) for a web_fetch/web_scrape step
// that completed a network request — called from both runners.go's
// webFetchRunner.Run and webScrapeRunner.Run right after a successful
// fetch (audit-that-tells-the-truth-01PMZA10 UNIT-5: zero emit sites
// existed anywhere in the tree before this). em/rc nil-safe: rc is nil
// only in test harnesses that call a runner directly without an
// Engine-managed RunContext, and em is nil whenever Deps.NetworkAudit
// was not wired.
//
// Privacy invariant (payload doc comment, audit.go): hostname, HTTP
// status, and byte count ONLY — never the full URL (which may carry
// signed auth tokens) or response body.
func emitNetworkFetch(ctx context.Context, em audit.Emitter, rc *RunContext, st Step, stepKind, hostname string, status, bytes int) {
	if em == nil || rc == nil {
		return
	}
	audit.MustEmit(ctx, em, audit.KindWorkflowNetworkFetch,
		audit.WorkflowNetworkFetchPayload{
			WorkflowID: rc.Workflow.ID,
			RunID:      rc.RunID,
			StepID:     st.Name,
			StepKind:   stepKind,
			Hostname:   hostname,
			Status:     status,
			Bytes:      bytes,
		}, time.Now().UTC())
}

// EmitSaved records a successful Save on the audit log.
func EmitSaved(ctx context.Context, em audit.Emitter, w Workflow) {
	if em == nil {
		return
	}
	audit.MustEmit(ctx, em, audit.KindWorkflowSaved,
		audit.WorkflowSavedPayload{
			WorkflowID: w.ID,
			Version:    w.Version,
			Hash:       w.Hash(),
		}, time.Now().UTC())
}

// EmitDeleted records a successful Delete on the audit log.
func EmitDeleted(ctx context.Context, em audit.Emitter, workflowID string) {
	if em == nil {
		return
	}
	audit.MustEmit(ctx, em, audit.KindWorkflowDeleted,
		audit.WorkflowDeletedPayload{
			WorkflowID: workflowID,
		}, time.Now().UTC())
}

// classifyStepError reduces a raw step error message to a small set of
// typed classes so the audit payload stays free of user-input bytes.
// The classes are stable strings the audit consumer can pivot on;
// unknown errors collapse to "runner_error".
func classifyStepError(msg string) string {
	if msg == "" {
		return "unknown"
	}
	low := strings.ToLower(msg)
	switch {
	case strings.Contains(low, "cancel"):
		return "context_canceled"
	case strings.Contains(low, "timeout") || strings.Contains(low, "deadline"):
		return "deadline_exceeded"
	case errors.Is(errors.New(msg), ErrUnknownStepKind) ||
		strings.Contains(low, "unknown step kind"):
		return "unknown_step_kind"
	case strings.Contains(low, "policy") || strings.Contains(low, "denied"):
		return "policy_denied"
	case strings.Contains(low, "validate") || strings.Contains(low, "required"):
		return "step_validation"
	default:
		return "runner_error"
	}
}

// CollectStepKinds returns the comma-joined sorted list of unique step
// kinds in w. Used by the cedar gate helpers (GateWorkflowRun /
// GateWorkflowSave) so the policy bundle's `like` rules can match on
// "*shell*" without forcing the policy author to enumerate kinds.
func CollectStepKinds(w Workflow) string {
	if len(w.Steps) == 0 {
		return ""
	}
	seen := make(map[string]struct{}, len(w.Steps))
	var kinds []string
	for _, s := range w.Steps {
		k := string(s.Kind)
		if _, ok := seen[k]; ok {
			continue
		}
		seen[k] = struct{}{}
		kinds = append(kinds, k)
	}
	// Sort in-place for deterministic policy matching.
	for i := 1; i < len(kinds); i++ {
		for j := i; j > 0 && kinds[j-1] > kinds[j]; j-- {
			kinds[j-1], kinds[j] = kinds[j], kinds[j-1]
		}
	}
	return strings.Join(kinds, ",")
}
