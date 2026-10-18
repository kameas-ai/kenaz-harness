package activity

import (
	"errors"
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
//     construction when there has been none) — Health reports Blocked so
//     the Local activity panel can say so. The Pruner keeps pruning to
//     the last good value (the member's own choice; stopping would
//     over-retain for as long as the file stays broken) and deletes
//     nothing only when there is none.
//   - ErrSettingMissing (the settings file does not exist) is a failed
//     read once a value has been read from an existing file in this
//     process (the file was deleted after a good read: keep that value),
//     and a good read of the default (0) otherwise (a genuine first run).
//
// Safe for concurrent use.
type Retention struct {
	read func() (int, error)
	now  func() time.Time

	mu           sync.Mutex
	haveGood     bool
	haveStored   bool // a read from an existing settings file succeeded
	good         int
	goodAt       time.Time
	start        time.Time
	lastFailed   bool
	failedPasses int
}

// ErrSettingMissing is what a Retention read func returns when the
// settings file does not exist (see Retention).
var ErrSettingMissing = errors.New("activity: retention setting file missing")

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
	if errors.Is(err, ErrSettingMissing) && !r.haveStored {
		err, days = nil, 0
	} else if err == nil {
		r.haveStored = true
	}
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
	// threshold (visible guard; pruning continues to the last good value
	// when there is one).
	Blocked bool
	// ConsecutiveFailedPasses is the current streak of prune passes whose
	// read failed.
	ConsecutiveFailedPasses int
	// SinceLastGood is the time since the last good read (since
	// construction when there has been none).
	SinceLastGood time.Duration
	// HaveGood: a read has succeeded in this process.
	HaveGood bool
	// LastGoodDays is the last good read's raw value (0 = default; only
	// meaningful with HaveGood).
	LastGoodDays int
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
		LastGoodDays:            r.good,
	}
	h.Blocked = r.lastFailed && (r.failedPasses >= BlockedPasses || h.SinceLastGood > BlockedAfter)
	return h
}
