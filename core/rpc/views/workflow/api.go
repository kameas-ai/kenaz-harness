// Package workflow defines the WorkflowAPI view-scoped accessor for
// scheduler/job orchestration consumed by the harness workflow surface.
package workflow

import "context"

// Job is reference-only job metadata.
type Job struct {
	ID        string `json:"id"`
	Name      string `json:"name"`
	State     string `json:"state"`
	StartedAt string `json:"startedAt,omitempty"`
}

// WorkflowAPI is the view-scoped accessor for jobs/schedules.
type WorkflowAPI interface {
	ListJobs(ctx context.Context) ([]Job, error)
	StartStream(ctx context.Context) (subscriptionID string, err error)
	StopStream(ctx context.Context, subscriptionID string) error
}
