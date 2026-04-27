package prune

import (
	"sync"
	"time"
)

// Stats is the per-run metric snapshot. Emitted by the scheduler's
// onSweep callback (or returned by RunOnce). The kept / dropped /
// collapsed counts back the inspector's progress UI; the wall-clock
// duration backs perf-smoke acceptance (NFR-013).
type Stats struct {
	StartedAt time.Time     `json:"startedAt"`
	Duration  time.Duration `json:"duration"`
	Kept      int           `json:"kept"`
	Dropped   int           `json:"dropped"`
	Collapsed int           `json:"collapsed"`
	Pinned    int           `json:"pinned"`
}

// FromDecision distills a prune Decision into Stats. dur is the
// wall-clock the sweep took; the caller stamps it because the Decision
// itself doesn't carry timing.
func FromDecision(d Decision, started time.Time, dur time.Duration) Stats {
	collapsed := 0
	for range d.Collapsed {
		collapsed++
	}
	return Stats{
		StartedAt: started,
		Duration:  dur,
		Kept:      len(d.Kept),
		Dropped:   len(d.Dropped),
		Collapsed: collapsed,
		Pinned:    d.Pinned,
	}
}

// Recorder is a tiny in-memory ring of recent sweep stats. The
// inspector RPC reads from it to render the "last 7 days" sparkline.
// Capacity defaults to 30; wraps oldest-out.
type Recorder struct {
	mu  sync.Mutex
	cap int
	buf []Stats
}

// NewRecorder returns a recorder with the given capacity; <= 0 ⇒ 30.
func NewRecorder(capacity int) *Recorder {
	if capacity <= 0 {
		capacity = 30
	}
	return &Recorder{cap: capacity}
}

// Record appends a Stats entry; oldest is evicted when over capacity.
func (r *Recorder) Record(s Stats) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.buf = append(r.buf, s)
	if len(r.buf) > r.cap {
		r.buf = r.buf[len(r.buf)-r.cap:]
	}
}

// Snapshot returns a copy of the recorded stats, oldest first.
func (r *Recorder) Snapshot() []Stats {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make([]Stats, len(r.buf))
	copy(out, r.buf)
	return out
}

// Latest returns the most recent stats; zero value when empty.
func (r *Recorder) Latest() Stats {
	r.mu.Lock()
	defer r.mu.Unlock()
	if len(r.buf) == 0 {
		return Stats{}
	}
	return r.buf[len(r.buf)-1]
}
