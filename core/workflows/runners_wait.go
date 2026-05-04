package workflows

// runners_wait.go — `wait_until` step runner (WP06).
//
// Pauses the workflow run until one of:
//   - An absolute wall-clock time (RFC 3339) has passed.
//   - A relative duration from now has elapsed.
//   - A polled workflow expression becomes truthy.
//
// Context cancellation aborts the wait and returns context.Canceled.
// Past absolute times and zero-duration waits return immediately.
//
// Wakeup state is written to / cleared from the RunContext so a
// chassis restart that crosses the wakeup time can resume inline via
// the scheduler (see WP02 cron scheduler integration notes).

import (
	"context"
	"encoding/json"
	"fmt"
	"time"
)

// waitPollInterval is the tick rate for condition polling.
const waitPollInterval = 250 * time.Millisecond

type waitUntilRunner struct{}

func (waitUntilRunner) Validate(st Step) error {
	set := 0
	if st.Until != "" {
		set++
	}
	if st.WaitDuration != "" {
		set++
	}
	if st.Condition != "" {
		set++
	}
	if set == 0 {
		return fmt.Errorf("wait_until step %q: one of until, duration, or condition required", st.Name)
	}
	if set > 1 {
		return fmt.Errorf("wait_until step %q: only one of until, duration, or condition may be set", st.Name)
	}
	if st.Until != "" {
		if _, err := time.Parse(time.RFC3339, st.Until); err != nil {
			return fmt.Errorf("wait_until step %q: until must be RFC 3339: %w", st.Name, err)
		}
	}
	if st.WaitDuration != "" {
		if _, err := time.ParseDuration(st.WaitDuration); err != nil {
			return fmt.Errorf("wait_until step %q: invalid duration: %w", st.Name, err)
		}
	}
	return nil
}

func (r waitUntilRunner) Run(ctx context.Context, st Step, rc *RunContext) (TypedValue, error) {
	switch {
	case st.Until != "":
		return r.runUntil(ctx, st, rc)
	case st.WaitDuration != "":
		return r.runDuration(ctx, st, rc)
	default:
		return r.runCondition(ctx, st, rc)
	}
}

func (waitUntilRunner) runUntil(ctx context.Context, st Step, rc *RunContext) (TypedValue, error) {
	target, _ := time.Parse(time.RFC3339, st.Until)
	remaining := time.Until(target)
	if remaining <= 0 {
		// Already past — return immediately (expired wakeup path).
		return wakeResult("expired", st.Until), nil
	}
	// Persist wakeup state so a restart can resume.
	rc.SetWakeup(WakeupState{StepName: st.Name, WakeAt: target})
	timer := time.NewTimer(remaining)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		// Wakeup state intentionally kept — chassis restart can pick up.
		return wakeResult("interrupted", st.Until), ctx.Err()
	case <-timer.C:
		rc.ClearWakeup()
		return wakeResult("woken", st.Until), nil
	}
}

func (waitUntilRunner) runDuration(ctx context.Context, st Step, rc *RunContext) (TypedValue, error) {
	d, _ := time.ParseDuration(st.WaitDuration)
	if d <= 0 {
		return wakeResult("expired", st.WaitDuration), nil
	}
	wakeAt := time.Now().UTC().Add(d)
	rc.SetWakeup(WakeupState{StepName: st.Name, WakeAt: wakeAt})
	timer := time.NewTimer(d)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return wakeResult("interrupted", st.WaitDuration), ctx.Err()
	case <-timer.C:
		rc.ClearWakeup()
		return wakeResult("woken", st.WaitDuration), nil
	}
}

func (waitUntilRunner) runCondition(ctx context.Context, st Step, rc *RunContext) (TypedValue, error) {
	ticker := time.NewTicker(waitPollInterval)
	defer ticker.Stop()
	for {
		// Evaluate the condition expression against current run context.
		expanded, err := expandRefs(st.Condition, rc)
		if err != nil {
			// Unresolved refs are not ready yet — keep polling.
			expanded = ""
		}
		if evalPredicate(expanded) {
			return wakeResult("condition_met", st.Condition), nil
		}
		select {
		case <-ctx.Done():
			return wakeResult("interrupted", st.Condition), ctx.Err()
		case <-ticker.C:
		}
	}
}

func wakeResult(status, detail string) TypedValue {
	payload := map[string]any{"status": status, "detail": detail}
	b, _ := json.Marshal(payload)
	return TypedValue{Type: ValueTypeJSON, JSON: payload, Text: string(b)}
}

// WakeupState records the wakeup target so a chassis restart can resume
// the run. Persisted in RunContext; exported for external scheduler integration.
type WakeupState struct {
	StepName string
	WakeAt   time.Time
}

// SetWakeup records a pending wakeup in the run context.
func (rc *RunContext) SetWakeup(w WakeupState) {
	rc.mu.Lock()
	rc.wakeup = &w
	rc.mu.Unlock()
}

// ClearWakeup removes a previously set wakeup state.
func (rc *RunContext) ClearWakeup() {
	rc.mu.Lock()
	rc.wakeup = nil
	rc.mu.Unlock()
}

// Wakeup returns the current wakeup state (nil if none pending).
func (rc *RunContext) Wakeup() *WakeupState {
	rc.mu.RLock()
	defer rc.mu.RUnlock()
	return rc.wakeup
}
