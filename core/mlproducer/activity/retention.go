package activity

import (
	"sync"
	"time"
)

// Retention reads the local retention setting for anything that prunes
// by it (the Pruner, the engine's spawn env) and fails safe: pruning is
// irreversible, so a failed read never widens or shortens the window.
//
//   - A read that fails falls back to the last SUCCESSFULLY read value in
//     this process.
//   - With no successful read yet, there is no value (ok = false): the
//     caller deletes nothing.
//   - When reads keep failing — BlockedPasses consecutive failed prune
//     passes, or more than BlockedAfter since the last good read (since
//     construction when there has been none) — Health reports Blocked and
//     the Pruner stops deleting even with a remembered value, so the
//     Local activity panel's "nothing is being deleted" is true.
//
// Safe for concurrent use.
type Retention struct {
	read func() (int, error)
	now  func() time.Time

	mu           sync.Mutex
	haveGood     bool
	good         int
	goodAt       time.Time
	start        time.Time
	lastFailed   bool
	failedPasses int
}

// BlockedPasses / BlockedAfter are the thresholds at which unreadable
// retention becomes visible (Health().Blocked).
const (
	BlockedPasses = 3
	BlockedAfter  = 24 * time.Hour
)

// NewRetention wraps read (the setting: 0 = default). nil read always
// reads (0, nil). nil now = time.Now.
func NewRetention(read func() (int, error), now func() time.Time) *Retention {
	if read == nil {
		read = func() (int, error) { return 0, nil }
	}
	if now == nil {
		now = time.Now
	}
	return &Retention{read: read, now: now, start: now()}
}

// Read reads the setting now. raw is the stored value (0 = default);
// fresh is true when this read succeeded; ok is false only when it failed
// and nothing was ever read successfully. A successful read resets the
// failure streak.
func (r *Retention) Read() (raw int, fresh, ok bool) {
	return r.readCounted(false)
}

// readPass is Read for one prune pass: a failure extends the streak.
func (r *Retention) readPass() (raw int, fresh, ok bool) {
	return r.readCounted(true)
}

func (r *Retention) readCounted(pass bool) (int, bool, bool) {
	days, err := r.read()
	r.mu.Lock()
	defer r.mu.Unlock()
	if err == nil {
		r.haveGood, r.good, r.goodAt = true, days, r.now()
		r.lastFailed, r.failedPasses = false, 0
		return days, true, true
	}
	r.lastFailed = true
	if pass {
		r.failedPasses++
	}
	return r.good, false, r.haveGood
}

// RetentionHealth is Retention's failure state, as counts only (never
// the setting's content or the read error).
type RetentionHealth struct {
	// Blocked: the most recent read failed and the streak has reached a
	// threshold; nothing is pruned until a read succeeds.
	Blocked bool
	// ConsecutiveFailedPasses is the current streak of prune passes whose
	// read failed.
	ConsecutiveFailedPasses int
	// SinceLastGood is the time since the last good read (since
	// construction when there has been none).
	SinceLastGood time.Duration
	// HaveGood: a read has succeeded in this process.
	HaveGood bool
}

// Health is the current failure state.
func (r *Retention) Health() RetentionHealth {
	r.mu.Lock()
	defer r.mu.Unlock()
	ref := r.start
	if r.haveGood {
		ref = r.goodAt
	}
	h := RetentionHealth{
		ConsecutiveFailedPasses: r.failedPasses,
		SinceLastGood:           r.now().Sub(ref),
		HaveGood:                r.haveGood,
	}
	h.Blocked = r.lastFailed && (r.failedPasses >= BlockedPasses || h.SinceLastGood > BlockedAfter)
	return h
}
