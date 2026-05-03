package credstore

// prune.go — WP07: SweepAuditLog retention sweep.
//
// Design:
//   - SweepAuditLog(ctx, retainDuration) deletes audit events older than
//     retainDuration. When retainDuration is zero the sweep is a no-op
//     (default forever retention, per spec Q26.1 resolution).
//   - The actual deletion is delegated to an optional AuditSweeper
//     injected via WithAuditSweeper. nil sweeper is a no-op (tests and
//     builds that don't yet wire a SQL store). This keeps credstore free
//     of any database dependency (C-004).
//   - A background goroutine started in New() calls SweepAuditLog once at
//     startup (if last sweep > 24h ago) and then every 24h. The goroutine
//     stops when the store is closed.
//   - Schedule: the goroutine respects s.done so Close() tears it down
//     cleanly. It tracks the last-sweep wall time in sweepLastRan
//     (in-memory only; a restart re-runs the check).

import (
	"context"
	"sync/atomic"
	"time"
	"unsafe"
)

// sweepInterval is how often the background sweep goroutine checks whether
// a sweep is due.
const sweepInterval = 6 * time.Hour

// sweepMinAge is the minimum time since the last sweep before the
// goroutine will fire again. This avoids re-sweeping on every startup when
// the process restarts frequently.
const sweepMinAge = 24 * time.Hour

// AuditSweeper is the optional deletion backend for SweepAuditLog.
// Implementations operate on the backing audit-events store (e.g. SQLite
// via the event-log mission). nil = no-op (in-memory audit paths and test
// builds).
//
// The sweep MUST be paginated: the implementation should issue DELETE
// statements with a LIMIT (e.g. 5000 rows) in a loop until the affected
// count reaches zero. This prevents a large sweep from holding the SQLite
// writer lock for an extended period (per plan §12 risk register).
type AuditSweeper interface {
	// SweepCredentialAccessed deletes audit rows of kind
	// "credential.accessed" with created_at older than cutoff.
	// Returns the total number of rows deleted. Must be idempotent.
	SweepCredentialAccessed(ctx context.Context, cutoff time.Time) (int, error)
}

// WithAuditSweeper injects an AuditSweeper into the store. The sweeper is
// called by SweepAuditLog and by the daily background goroutine. nil is
// accepted silently (no-op); the store remains usable.
func WithAuditSweeper(sw AuditSweeper) StoreOption {
	return func(s *store) {
		s.auditSweeper = sw
	}
}

// WithRetentionDays sets the audit-log retention window in days. Zero
// (default) means forever. The background goroutine reads this on every
// sweep tick so a settings change takes effect on the next scheduled run.
// Wire a func() int that returns Settings.CredentialAuditRetentionDays.
func WithRetentionDays(fn func() int) StoreOption {
	return func(s *store) {
		s.retentionDaysFn = fn
	}
}

// SweepAuditLog deletes credential.accessed audit rows older than
// retainDuration. When retainDuration is zero the call is a no-op and
// returns (0, nil). Returns (deleted, nil) on success.
//
// This method is also called by the background sweep goroutine on the
// schedule above.
func (s *store) SweepAuditLog(ctx context.Context, retainDuration time.Duration) (int, error) {
	if retainDuration <= 0 {
		return 0, nil // zero = forever, per spec Q26.1
	}
	if s.auditSweeper == nil {
		return 0, nil // no sweeper wired — no-op
	}
	cutoff := time.Now().Add(-retainDuration)
	n, err := s.auditSweeper.SweepCredentialAccessed(ctx, cutoff)
	if err == nil {
		// record last-sweep time (pointer swap — no lock needed)
		now := time.Now()
		atomic.StorePointer((*unsafe.Pointer)(unsafe.Pointer(&s.sweepLastRan)), unsafe.Pointer(&now))
	}
	return n, err
}

// sweepLoop is the background goroutine started in New() that runs a daily
// sweep. It reads the retention days from retentionDaysFn on every tick so
// settings changes take effect without restarting the store.
func (s *store) sweepLoop() {
	ticker := time.NewTicker(sweepInterval)
	defer ticker.Stop()
	for {
		select {
		case <-s.done:
			return
		case <-ticker.C:
			s.maybeSweep()
		}
	}
}

func (s *store) maybeSweep() {
	// Check last sweep time.
	ptr := atomic.LoadPointer((*unsafe.Pointer)(unsafe.Pointer(&s.sweepLastRan)))
	if ptr != nil {
		lastRan := *(*time.Time)(ptr)
		if time.Since(lastRan) < sweepMinAge {
			return // swept recently enough
		}
	}
	if s.retentionDaysFn == nil {
		return // no retention dial wired
	}
	days := s.retentionDaysFn()
	if days <= 0 {
		return // forever retention
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	_, _ = s.SweepAuditLog(ctx, time.Duration(days)*24*time.Hour)
}
