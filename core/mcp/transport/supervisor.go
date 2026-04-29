package transport

import "time"

// RestartWindow is the rolling window over which restart attempts
// are counted. Three failures inside the window → state=failed
// per FR-007.
const RestartWindow = 5 * time.Minute

// BackoffSchedule encodes the FR-007 exponential backoff: 1 s, 2 s,
// 4 s — three attempts maximum inside any 5-minute window. Shared
// across stdio, HTTP, and SSE transports so reconnect cadence is
// consistent regardless of how the bytes layer is implemented.
var BackoffSchedule = []time.Duration{
	1 * time.Second,
	2 * time.Second,
	4 * time.Second,
}

// PruneHistory drops timestamps older than the window relative to
// now. Returns a new slice; does not mutate the input.
func PruneHistory(history []time.Time, now time.Time, window time.Duration) []time.Time {
	if len(history) == 0 {
		return history
	}
	cutoff := now.Add(-window)
	out := history[:0:0]
	for _, t := range history {
		if t.After(cutoff) {
			out = append(out, t)
		}
	}
	return out
}
