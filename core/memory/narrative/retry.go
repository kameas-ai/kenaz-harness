package narrative

import "time"

// MaxAttempts is the maximum number of synthesis attempts per job
// before the job is moved to status=failed (FR-001-B).
const MaxAttempts = 5

// backoffSchedule is the exponential backoff delay indexed by attempt
// number (0-based). Attempt 0 is the first retry after first failure.
// Spec: 1m, 5m, 30m, 2h, 24h.
var backoffSchedule = []time.Duration{
	1 * time.Minute,
	5 * time.Minute,
	30 * time.Minute,
	2 * time.Hour,
	24 * time.Hour,
}

// NextRetryAt returns the next retry timestamp given the zero-based
// attempt number (i.e. how many attempts have already been made) and
// the current time. When attempt ≥ MaxAttempts the job is exhausted
// and NextRetryAt returns the zero time (callers interpret this as
// "move to failed").
func NextRetryAt(attempt int, now time.Time) time.Time {
	if attempt >= MaxAttempts {
		return time.Time{}
	}
	idx := attempt
	if idx >= len(backoffSchedule) {
		idx = len(backoffSchedule) - 1
	}
	return now.Add(backoffSchedule[idx])
}

// Exhausted reports whether the job has consumed all retry slots.
func Exhausted(attempt int) bool {
	return attempt >= MaxAttempts
}
