package workflows

// runners_notify.go — `notify` step runner (WP06).
//
// Dispatches a notification to one or more surfaces: os, slack, email, push.
// Unconfigured or erroring surfaces are logged as warnings; the run only
// fails when ALL surfaces fail with a hard error. A partially-successful
// dispatch is still a success — the result records per-target outcomes.
//
// Privacy invariant: audit events carry only the target name and a truncated
// title (≤60 chars). The full body is NEVER included in any audit payload.

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
)

const notifyTitleAuditMaxLen = 60

// notifyRunner implements the `notify` step kind.
type notifyRunner struct {
	notifier Notifier    // OS surface; nil → ErrNotifyTargetUnconfigured for "os"
	mcp      MCPCaller   // used for slack / email / push via MCP recipes
	audit    AuditEmitter // nil → no audit events emitted
}

func (notifyRunner) Validate(st Step) error {
	if st.NotifyTitle == "" {
		return fmt.Errorf("notify step %q: notify_title required", st.Name)
	}
	if len(st.Surface) == 0 {
		return fmt.Errorf("notify step %q: surface must list at least one target", st.Name)
	}
	for _, s := range st.Surface {
		switch s {
		case "os", "slack", "email", "push":
		default:
			return fmt.Errorf("notify step %q: unknown surface %q (valid: os, slack, email, push)", st.Name, s)
		}
	}
	return nil
}

func (r notifyRunner) Run(ctx context.Context, st Step, _ *RunContext) (TypedValue, error) {
	type targetResult struct {
		Target string `json:"target"`
		Status string `json:"status"` // sent | unconfigured | error
		Err    string `json:"error,omitempty"`
	}

	results := make([]targetResult, 0, len(st.Surface))
	var hardErrs []string

	for _, surface := range st.Surface {
		var res targetResult
		res.Target = surface

		var dispatchErr error
		switch surface {
		case "os":
			dispatchErr = r.dispatchOS(ctx, st.NotifyTitle, st.NotifyBody)
		case "slack":
			dispatchErr = r.dispatchMCP(ctx, "slack", st.NotifyTitle, st.NotifyBody)
		case "email":
			dispatchErr = r.dispatchMCP(ctx, "gmail", st.NotifyTitle, st.NotifyBody)
		case "push":
			dispatchErr = r.dispatchMCP(ctx, "push", st.NotifyTitle, st.NotifyBody)
		}

		switch {
		case dispatchErr == nil:
			res.Status = "sent"
			// Emit audit event — truncated title only, NEVER body.
			r.emitSent(ctx, surface, st.NotifyTitle)
		case isUnconfigured(dispatchErr):
			res.Status = "unconfigured"
			res.Err = dispatchErr.Error()
			// Unconfigured is a soft failure — log but don't fail the run.
		default:
			res.Status = "error"
			res.Err = dispatchErr.Error()
			hardErrs = append(hardErrs, fmt.Sprintf("%s: %v", surface, dispatchErr))
		}
		results = append(results, res)
	}

	payload := map[string]any{
		"targets":       results,
		"surface_count": len(st.Surface),
	}
	encoded, _ := json.Marshal(payload)
	tv := TypedValue{Type: ValueTypeJSON, JSON: payload, Text: string(encoded)}

	// The run fails only when every surface produced a hard error.
	sentCount := 0
	for _, r := range results {
		if r.Status == "sent" {
			sentCount++
		}
	}
	if sentCount == 0 && len(hardErrs) > 0 {
		return tv, fmt.Errorf("notify step %q: all surfaces failed: %s", st.Name, strings.Join(hardErrs, "; "))
	}
	return tv, nil
}

// dispatchOS sends a notification via the injected Notifier.
func (r notifyRunner) dispatchOS(ctx context.Context, title, body string) error {
	if r.notifier == nil {
		return ErrNotifyTargetUnconfigured
	}
	return r.notifier.Notify(ctx, title, body)
}

// dispatchMCP sends a notification via the named MCP recipe server.
// If MCP is not wired or the server is not found, returns
// ErrNotifyTargetUnconfigured.
func (r notifyRunner) dispatchMCP(ctx context.Context, server, title, body string) error {
	if r.mcp == nil {
		return ErrNotifyTargetUnconfigured
	}
	_, err := r.mcp.Call(ctx, server, "send_notification", map[string]any{
		"title": title,
		"body":  body,
	})
	if err != nil {
		// Treat "not configured" / "server not found" errors as soft unconfigured.
		low := strings.ToLower(err.Error())
		if strings.Contains(low, "not found") || strings.Contains(low, "not configured") ||
			strings.Contains(low, "unavailable") || strings.Contains(low, "no server") {
			return ErrNotifyTargetUnconfigured
		}
		return err
	}
	return nil
}

// emitSent fires a notify.sent audit event. Body is intentionally absent.
func (r notifyRunner) emitSent(ctx context.Context, target, title string) {
	if r.audit == nil {
		return
	}
	truncated := title
	if len(truncated) > notifyTitleAuditMaxLen {
		truncated = truncated[:notifyTitleAuditMaxLen]
	}
	_ = r.audit.EmitNotifySent(ctx, target, truncated)
}

// isUnconfigured checks whether err signals a missing/unconfigured target
// rather than a transient dispatch error.
func isUnconfigured(err error) bool {
	if err == nil {
		return false
	}
	if err == ErrNotifyTargetUnconfigured {
		return true
	}
	low := strings.ToLower(err.Error())
	return strings.Contains(low, "not configured") || strings.Contains(low, "unconfigured")
}
