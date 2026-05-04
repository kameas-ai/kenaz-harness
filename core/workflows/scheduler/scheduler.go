// Package scheduler provides a cron-backed dispatch layer for
// user-defined workflows (mission workflows-agentic-01KW2D3X, WP02).
//
// # Design
//
//   - Scheduler is the narrow interface tests and the chassis wire against.
//     Register / Unregister manage the set of active schedules; RunNow
//     dispatches an off-schedule run immediately; History returns recent
//     run summaries for a workflow; Tick is the injectable clock used by
//     tests to drive time forward without sleeping.
//
//   - CronScheduler is the concrete implementation backed by
//     github.com/robfig/cron/v3. Schedules survive chassis restarts via
//     Storage, which persists them in the workflow_schedules DB table.
//
//   - Timezone quirk: robfig/cron/v3's WithLocation option sets a single
//     process-wide TZ. Per-schedule timezone is applied by wrapping each
//     cron spec with the IANA location name prefix
//     (e.g. "CRON_TZ=America/New_York 0 7 * * *").
package scheduler

import (
	"context"
	"time"
)

// RunSummary is one record in the History slice.
type RunSummary struct {
	RunID      string    `json:"runId"`
	WorkflowID string    `json:"workflowId"`
	Status     string    `json:"status"` // completed | failed | running
	StartedAt  time.Time `json:"startedAt"`
	EndedAt    time.Time `json:"endedAt,omitempty"`
	Err        string    `json:"error,omitempty"`
	Scheduled  bool      `json:"scheduled"` // false when triggered by RunNow
}

// ScheduleEntry describes one registered schedule.
type ScheduleEntry struct {
	WorkflowID string `json:"workflowId"`
	Cron       string `json:"cron"`
	Timezone   string `json:"timezone"`
	Enabled    bool   `json:"enabled"`
}

// Dispatcher is the interface the Scheduler uses to actually fire a
// workflow run. Decoupled from the concrete engine so tests can inject
// a stub without booting the full LLM stack.
type Dispatcher interface {
	// Dispatch fires an async workflow run and returns the run ID.
	// The scheduler records the summary after the run completes.
	Dispatch(ctx context.Context, workflowID string, scheduled bool) (string, error)
}

// Scheduler is the cron-scheduler surface.
//
// All methods are safe for concurrent use.
type Scheduler interface {
	// Register adds or replaces the cron schedule for workflowID.
	// cron is a standard 5-field cron expression (minute hour dom month dow).
	// tz is an IANA timezone name (e.g. "America/New_York"); empty string
	// falls back to UTC.
	Register(ctx context.Context, workflowID, cron, tz string) error

	// Unregister removes the cron schedule for workflowID. Returns nil
	// if no schedule was registered.
	Unregister(ctx context.Context, workflowID string) error

	// RunNow dispatches an immediate off-schedule run. Does not require
	// an active cron entry for the workflow.
	RunNow(ctx context.Context, workflowID string) (RunSummary, error)

	// History returns up to limit recent RunSummary records for
	// workflowID in reverse-chronological order.
	History(ctx context.Context, workflowID string, limit int) ([]RunSummary, error)

	// List returns all currently registered schedule entries.
	List(ctx context.Context) ([]ScheduleEntry, error)

	// Start starts the underlying cron engine. Safe to call multiple
	// times; subsequent calls are no-ops.
	Start()

	// Stop halts cron dispatch. In-flight runs are not interrupted.
	Stop()

	// Tick drives the test clock to now. No-op on the real
	// implementation (which uses the system clock internally through
	// robfig/cron).
	Tick(now time.Time)
}
