// Package tasks defines the RPC view for the background-task registry
// (background-task-monitor-01KZNP3C WP05).
//
// These four bindings are exposed to the frontend via Wails so the
// Tasks panel and BackgroundTaskChip can list, inspect, tail, and abort tasks.
package tasks

import (
	"context"
	"fmt"
	"time"

	coretasks "github.com/sigil-tech/kaneaz-harness/core/tasks"
)

// TaskRow is the wire-safe serialisation of a background task for the UI.
type TaskRow struct {
	ID             string  `json:"id"`
	Kind           string  `json:"kind"`
	OwnerSessionID string  `json:"ownerSessionId"`
	Cmd            string  `json:"cmd"`
	Description    string  `json:"description"`
	Status         string  `json:"status"`
	ExitCode       int     `json:"exitCode"`
	StartedAt      string  `json:"startedAt"` // RFC3339
	EndedAt        *string `json:"endedAt,omitempty"`
	AgeMs          int64   `json:"ageMs"`
}

// LineRow is one output line returned by Tasks_Tail.
type LineRow struct {
	Stream string `json:"stream"` // "stdout" | "stderr"
	Text   string `json:"text"`
	Offset int64  `json:"offset"`
	At     string `json:"at"` // RFC3339
}

// RegistryIface is the narrow interface the RPC view needs.
type RegistryIface interface {
	Get(id string) (coretasks.Task, bool)
	List() []coretasks.Task
	Tail(id string, fromOffset int64) ([]coretasks.Line, int64, bool)
	Abort(ctx context.Context, id string) error
}

// API provides the four Wails-bound task operations.
type API struct {
	reg RegistryIface
}

// NewAPI constructs an API. reg must be non-nil.
func NewAPI(reg RegistryIface) *API {
	return &API{reg: reg}
}

// Tasks_List returns all known tasks sorted newest-first.
func (a *API) Tasks_List(_ context.Context) ([]TaskRow, error) {
	if a.reg == nil {
		return nil, nil
	}
	tasks := a.reg.List()
	rows := make([]TaskRow, len(tasks))
	for i, t := range tasks {
		rows[i] = toRow(t)
	}
	// Sort newest-first (by StartedAt descending).
	for i := 0; i < len(rows)-1; i++ {
		for j := i + 1; j < len(rows); j++ {
			if rows[j].StartedAt > rows[i].StartedAt {
				rows[i], rows[j] = rows[j], rows[i]
			}
		}
	}
	return rows, nil
}

// Tasks_Get returns a single task by ID.
func (a *API) Tasks_Get(_ context.Context, id string) (TaskRow, error) {
	if a.reg == nil {
		return TaskRow{}, fmt.Errorf("registry not available")
	}
	t, ok := a.reg.Get(id)
	if !ok {
		return TaskRow{}, fmt.Errorf("task %q not found", id)
	}
	return toRow(t), nil
}

// Tasks_Tail returns lines captured since fromOffset.
func (a *API) Tasks_Tail(_ context.Context, id string, fromOffset int64) ([]LineRow, error) {
	if a.reg == nil {
		return nil, fmt.Errorf("registry not available")
	}
	lines, _, ok := a.reg.Tail(id, fromOffset)
	if !ok {
		return nil, fmt.Errorf("task %q not found", id)
	}
	rows := make([]LineRow, len(lines))
	for i, ln := range lines {
		rows[i] = LineRow{
			Stream: ln.Stream,
			Text:   ln.Text,
			Offset: ln.Offset,
			At:     ln.At.Format(time.RFC3339Nano),
		}
	}
	return rows, nil
}

// Tasks_Abort sends SIGTERM to the task's process and marks it cancelled.
func (a *API) Tasks_Abort(ctx context.Context, id string) error {
	if a.reg == nil {
		return fmt.Errorf("registry not available")
	}
	return a.reg.Abort(ctx, id)
}

func toRow(t coretasks.Task) TaskRow {
	row := TaskRow{
		ID:             t.ID,
		Kind:           t.Kind,
		OwnerSessionID: t.OwnerSessionID,
		Cmd:            t.Cmd,
		Description:    t.Description,
		Status:         t.Status,
		ExitCode:       t.ExitCode,
		StartedAt:      t.StartedAt.Format(time.RFC3339),
		AgeMs:          time.Since(t.StartedAt).Milliseconds(),
	}
	if t.EndedAt != nil {
		s := t.EndedAt.Format(time.RFC3339)
		row.EndedAt = &s
	}
	return row
}
