// session_runner.go — SessionRunnerAdapter wires core/hooks.Runner
// onto the session.SessionHookRunner seam.
//
// The adapter imports core/session to satisfy the interface, then is
// injected at the wiring layer (core/rpc).
package hooks

import (
	"context"

	"github.com/sigil-tech/kaneaz-harness/core/session"
)

// Compile-time assertion: SessionRunnerAdapter implements
// session.SessionHookRunner.
var _ session.SessionHookRunner = (*SessionRunnerAdapter)(nil)

// SessionRunnerAdapter wraps a *Runner and implements
// session.SessionHookRunner.
type SessionRunnerAdapter struct {
	Runner *Runner
}

// FireSessionStart fires the session_start event.
func (a *SessionRunnerAdapter) FireSessionStart(
	ctx context.Context,
	req session.SessionStartRequest,
) (session.SessionHookResult, error) {
	if a == nil || a.Runner == nil {
		return session.SessionHookResult{}, nil
	}
	outputs, err := a.Runner.Fire(ctx, EventSessionStart, SessionStartEvent{
		SessionID: req.SessionID,
		ProjectID: req.ProjectID,
		CWD:       req.CWD,
		Model:     req.Model,
	})
	if err != nil {
		return session.SessionHookResult{}, err
	}
	merged := MergeOutputs(outputs)
	return session.SessionHookResult{
		WatchPaths:        merged.WatchPaths,
		AdditionalContext: merged.AdditionalContext,
	}, nil
}

// FireSetup fires the setup event.
func (a *SessionRunnerAdapter) FireSetup(
	ctx context.Context,
	sessionID, projectID, cwd string,
) (session.SessionHookResult, error) {
	if a == nil || a.Runner == nil {
		return session.SessionHookResult{}, nil
	}
	outputs, err := a.Runner.Fire(ctx, EventSetup, SetupEvent{
		SessionID: sessionID,
		ProjectID: projectID,
		CWD:       cwd,
	})
	if err != nil {
		return session.SessionHookResult{}, err
	}
	merged := MergeOutputs(outputs)
	return session.SessionHookResult{
		WatchPaths:        merged.WatchPaths,
		AdditionalContext: merged.AdditionalContext,
	}, nil
}

// FireCwdChanged fires the cwd_changed event.
func (a *SessionRunnerAdapter) FireCwdChanged(
	ctx context.Context,
	sessionID, oldCWD, newCWD string,
) (session.SessionHookResult, error) {
	if a == nil || a.Runner == nil {
		return session.SessionHookResult{}, nil
	}
	outputs, err := a.Runner.Fire(ctx, EventCwdChanged, CwdChangedEvent{
		SessionID: sessionID,
		OldCWD:    oldCWD,
		NewCWD:    newCWD,
	})
	if err != nil {
		return session.SessionHookResult{}, err
	}
	merged := MergeOutputs(outputs)
	return session.SessionHookResult{
		WatchPaths:        merged.WatchPaths,
		AdditionalContext: merged.AdditionalContext,
	}, nil
}
