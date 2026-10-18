package activity

import (
	"context"
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
//   - The member's choice is durable next to the data it governs
//     (activity.db's harness_retention_choice, WithChoiceStore): Chose
//     records it, and a good read of an EXPLICIT non-zero value records
//     it. A read of 0 / absent never records it.
//   - The settings file present with 0 / the key absent (any save
//     recreates a deleted file that way, and a hand edit can drop the
//     key) is not a failure: the recorded choice applies, else the
//     in-process value from an explicit read or Chose, else the default.
//   - ErrSettingMissing (the settings file does not exist) is a failed
//     read once a choice exists — the recorded one (also in a new
//     process) or this process's — and that choice is the value kept;
//     with none it is a good read of the default (a genuine first run).
//   - A recorded choice that cannot be read (busy, timeout, IO) while the
//     file is missing or says 0 is a failed read; only an absent row
//     means "no choice".
//
// Safe for concurrent use.
type Retention struct {
	read func() (int, error)
	now  func() time.Time

	mu           sync.Mutex
	haveGood     bool
	haveStored   bool // good holds the member's choice (explicit read, recorded choice or Chose)
	choiceStore  func() *Store
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

// WithChoiceStore sets where the member's choice is recorded (the local
// activity store; nil or a func returning nil = not recorded). Call
// before the Retention is shared.
func (r *Retention) WithChoiceStore(f func() *Store) *Retention {
	r.choiceStore = f
	return r
}

func (r *Retention) store() *Store {
	if r.choiceStore == nil {
		return nil
	}
	return r.choiceStore()
}

// recordedChoice is the recorded choice. ok = false with a nil error
// only when there is none (no row, or no store: with activity.db gone the
// data is gone too). A read error (busy, timeout, IO) is returned, never
// folded into "no choice": that would read as the default and prune.
func (r *Retention) recordedChoice() (int, bool, error) {
	st := r.store()
	if st == nil {
		return 0, false, nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), choiceTimeout)
	defer cancel()
	return st.RetentionChoice(ctx)
}

// record records days (effective) as the choice, unless already recorded.
func (r *Retention) record(days int) error {
	st := r.store()
	if st == nil {
		return nil
	}
	if cur, ok, err := r.recordedChoice(); err == nil && ok && cur == days {
		return nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), choiceTimeout)
	defer cancel()
	return st.SetRetentionChoice(ctx, days)
}

const choiceTimeout = 5 * time.Second

// Chose records the member's explicit choice (raw: 0 = default) after the
// settings save succeeded, and makes it this process's value.
func (r *Retention) Chose(raw int) error {
	eff := EffectiveRetentionDays(raw)
	err := r.record(eff)
	r.mu.Lock()
	defer r.mu.Unlock()
	r.haveGood, r.haveStored, r.good, r.goodAt = true, true, eff, r.now()
	r.lastFailed, r.failedPasses = false, 0
	return err
}

func (r *Retention) readCounted(pass bool) (int, bool, bool) {
	days, err := r.read()
	missing := errors.Is(err, ErrSettingMissing)
	explicit := err == nil && days != 0
	unset := err == nil && days == 0
	var choice int
	var haveChoice bool
	var choiceErr error
	switch {
	case explicit:
		// Best effort: the value still applies when it cannot be recorded.
		_ = r.record(EffectiveRetentionDays(days))
	case unset || missing:
		choice, haveChoice, choiceErr = r.recordedChoice()
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	good := func(v int, stored bool) (int, bool, bool) {
		r.haveGood, r.good, r.goodAt = true, v, r.now()
		r.haveStored = r.haveStored || stored
		r.lastFailed, r.failedPasses = false, 0
		return v, true, true
	}
	switch {
	case explicit:
		return good(days, true)
	case choiceErr != nil:
		// The recorded choice cannot be read: a failed read (the last
		// good value, else nothing), never the default.
	case unset && haveChoice:
		return good(choice, true)
	case unset && r.haveStored:
		return good(r.good, true)
	case unset:
		return good(0, false)
	case missing && haveChoice:
		if !r.haveGood {
			r.goodAt = r.start
		}
		r.haveGood, r.haveStored, r.good = true, true, choice
	case missing && !r.haveStored:
		return good(0, false)
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
