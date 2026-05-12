package narrative

import (
	"context"
	"encoding/json"
	"errors"
	"sync"
	"time"
)

// JobStatus is the state machine for a narrative synthesis job.
type JobStatus string

const (
	JobStatusPending   JobStatus = "pending"
	JobStatusRetrying  JobStatus = "retrying"
	JobStatusSucceeded JobStatus = "succeeded"
	JobStatusFailed    JobStatus = "failed"
)

// Job is one pending narrative-synthesis task. It is keyed by
// (SessionID, TurnID) — exactly one non-terminal job per turn.
type Job struct {
	ID         string
	SessionID  string
	TurnID     string
	ProjectID  string
	Attempt    int
	RetryAt    time.Time
	LastError  string
	Payload    JobPayload
	Status     JobStatus
	CreatedAt  time.Time
	UpdatedAt  time.Time
}

// JobPayload carries the content needed by SyntheticBuilder.
type JobPayload struct {
	LastUserMsg      string          `json:"last_user_msg"`
	LastAssistantMsg string          `json:"last_assistant_msg"`
	ToolNarratives   []ToolNarrative `json:"tool_narratives,omitempty"`
}

// JobQueue manages pending narrative-synthesis jobs. The production
// implementation backs jobs through a SQL table; the in-memory
// implementation (used here) is sufficient for testing and for Phase 1
// deployments where persistence of inflight jobs is not critical.
//
// The queue guarantees:
//   - Enqueue is idempotent for (session_id, turn_id) — a second Enqueue
//     for the same key while a non-terminal job exists is a no-op.
//   - Pop returns up to n jobs whose RetryAt is ≤ now.
//   - Update changes the job's status and retry fields atomically.
type JobQueue interface {
	Enqueue(ctx context.Context, j Job) error
	Pop(ctx context.Context, now time.Time, limit int) ([]Job, error)
	Update(ctx context.Context, id string, status JobStatus, nextRetryAt time.Time, lastError string) error
	CountFailed(ctx context.Context) (int, error)
	ListFailed(ctx context.Context) ([]Job, error)
	ResetForRetry(ctx context.Context, id string) error
}

// ---- In-memory implementation ----

// MemJobQueue is an in-memory JobQueue. It is safe for concurrent use.
// Used in tests and as the default when no SQL store is wired.
type MemJobQueue struct {
	mu   sync.Mutex
	jobs map[string]*Job   // keyed by Job.ID
	byTurn map[string]string // (sess+turn) → Job.ID for dedup
}

// NewMemJobQueue constructs an empty in-memory queue.
func NewMemJobQueue() *MemJobQueue {
	return &MemJobQueue{
		jobs:   make(map[string]*Job),
		byTurn: make(map[string]string),
	}
}

func turnKey(sessionID, turnID string) string {
	return sessionID + "\x00" + turnID
}

// Enqueue adds a job if no non-terminal job exists for the same
// (session_id, turn_id). Duplicate enqueue is a no-op.
func (q *MemJobQueue) Enqueue(_ context.Context, j Job) error {
	q.mu.Lock()
	defer q.mu.Unlock()
	tk := turnKey(j.SessionID, j.TurnID)
	if existing, ok := q.byTurn[tk]; ok {
		if e := q.jobs[existing]; e != nil && e.Status != JobStatusSucceeded && e.Status != JobStatusFailed {
			// Non-terminal job exists — no-op.
			return nil
		}
	}
	cp := j
	if cp.CreatedAt.IsZero() {
		cp.CreatedAt = time.Now().UTC()
	}
	cp.UpdatedAt = cp.CreatedAt
	q.jobs[cp.ID] = &cp
	q.byTurn[tk] = cp.ID
	return nil
}

// Pop returns up to limit jobs whose RetryAt is ≤ now and status is
// pending or retrying. Jobs are NOT removed from the queue — the caller
// must call Update to transition them.
func (q *MemJobQueue) Pop(_ context.Context, now time.Time, limit int) ([]Job, error) {
	q.mu.Lock()
	defer q.mu.Unlock()
	var out []Job
	for _, j := range q.jobs {
		if j.Status != JobStatusPending && j.Status != JobStatusRetrying {
			continue
		}
		if j.RetryAt.After(now) {
			continue
		}
		out = append(out, *j)
		if len(out) >= limit {
			break
		}
	}
	return out, nil
}

// Update changes a job's status and retry fields.
func (q *MemJobQueue) Update(_ context.Context, id string, status JobStatus, nextRetryAt time.Time, lastError string) error {
	q.mu.Lock()
	defer q.mu.Unlock()
	j, ok := q.jobs[id]
	if !ok {
		return errors.New("narrative: job not found: " + id)
	}
	j.Status = status
	j.RetryAt = nextRetryAt
	j.LastError = lastError
	j.Attempt++
	j.UpdatedAt = time.Now().UTC()
	return nil
}

// CountFailed returns the number of jobs with status=failed.
func (q *MemJobQueue) CountFailed(_ context.Context) (int, error) {
	q.mu.Lock()
	defer q.mu.Unlock()
	n := 0
	for _, j := range q.jobs {
		if j.Status == JobStatusFailed {
			n++
		}
	}
	return n, nil
}

// ListFailed returns all jobs with status=failed.
func (q *MemJobQueue) ListFailed(_ context.Context) ([]Job, error) {
	q.mu.Lock()
	defer q.mu.Unlock()
	var out []Job
	for _, j := range q.jobs {
		if j.Status == JobStatusFailed {
			out = append(out, *j)
		}
	}
	return out, nil
}

// ResetForRetry sets the job back to pending with attempt=0 and
// retry_at=now so the next Promoter scan picks it up immediately.
func (q *MemJobQueue) ResetForRetry(_ context.Context, id string) error {
	q.mu.Lock()
	defer q.mu.Unlock()
	j, ok := q.jobs[id]
	if !ok {
		return errors.New("narrative: job not found: " + id)
	}
	j.Status = JobStatusPending
	j.Attempt = 0
	j.RetryAt = time.Now().UTC()
	j.UpdatedAt = time.Now().UTC()
	return nil
}

// payloadJSON round-trips a JobPayload to/from JSON for the SQL store.
func payloadJSON(p JobPayload) (string, error) {
	b, err := json.Marshal(p)
	return string(b), err
}

func payloadFromJSON(s string) (JobPayload, error) {
	var p JobPayload
	if s == "" || s == "{}" {
		return p, nil
	}
	err := json.Unmarshal([]byte(s), &p)
	return p, err
}
