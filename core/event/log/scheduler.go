package log

import (
	"context"
	"log/slog"
	"sync"
	"time"

	"github.com/kameas-ai/kenaz-harness/core/storage"
)

// LocalRetentionSweepInterval is how often LocalRetentionScheduler
// runs. spec §5.6 item 6 / E-002: "runs nightly"
// (AuditSettingsPanel.vue:79) has no existing interval constant to
// drive it from — that constant belongs to the UNRELATED fleet ACK
// sweeper (core/fleet.retentionSweepInterval, 1*time.Hour) — so this
// mission picks one. Daily makes the shipped copy true without a
// second frontend edit.
const LocalRetentionSweepInterval = 24 * time.Hour

// LocalRetentionScheduler runs RetentionSweep on a fixed interval
// against whatever policy is CURRENTLY persisted in retention_config,
// re-read fresh on every pass (so a settings change takes effect on
// the next tick, not only after a restart — AC-013 exercises the
// restart case because that is the strongest possible test, not
// because a restart is required for the dial to reach the deleter).
//
// Exists — and is Start()ed — on EVERY install with a real DataDir,
// unconditionally (spec D-7: "local retention is not fleet-gated").
// This is the opposite construction condition from
// core/fleet.AuditRetentionSweeper, which core/rpc/api.go only
// constructs when `dataDir != "" && flCl != nil && !flCl.IsNop()` — a
// non-enrolled device never gets that one at all. AC-015 exercises
// exactly this: with a nop fleet client, the LOCAL sweep still runs.
type LocalRetentionScheduler struct {
	backend  SweepableBackend
	db       storage.DB
	dataDir  string
	interval time.Duration

	mu     sync.Mutex
	cancel context.CancelFunc
	done   chan struct{}
}

// NewLocalRetentionScheduler constructs a scheduler. backend and db
// must be non-nil (backend for the sweep itself; db to re-read the
// policy each pass — see core/event/log/retention_config.go).
func NewLocalRetentionScheduler(backend SweepableBackend, db storage.DB, dataDir string) *LocalRetentionScheduler {
	return &LocalRetentionScheduler{
		backend:  backend,
		db:       db,
		dataDir:  dataDir,
		interval: LocalRetentionSweepInterval,
	}
}

// Start launches the background sweep loop. Safe to call at most once;
// a second call before Stop is a programming error (matches the
// Start/Stop contract core/fleet.AuditRetentionSweeper and every other
// background loop in core/rpc/api.go's Shutdown already use).
func (s *LocalRetentionScheduler) Start(ctx context.Context) {
	loopCtx, cancel := context.WithCancel(ctx)
	s.mu.Lock()
	s.cancel = cancel
	s.done = make(chan struct{})
	done := s.done
	s.mu.Unlock()
	go func() {
		defer close(done)
		s.loop(loopCtx)
	}()
}

// Stop signals the loop to exit and waits for it. Nil-safe on a
// scheduler that was never Start()ed.
func (s *LocalRetentionScheduler) Stop() {
	s.mu.Lock()
	cancel := s.cancel
	done := s.done
	s.mu.Unlock()
	if cancel != nil {
		cancel()
	}
	if done != nil {
		<-done
	}
}

func (s *LocalRetentionScheduler) loop(ctx context.Context) {
	ticker := time.NewTicker(s.interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			if _, err := s.SweepOnce(ctx); err != nil {
				slog.Warn("event/log: local retention sweep error", "err", err)
			}
		}
	}
}

// SweepOnce reads the current policy and runs one retention pass.
// Exposed (not just the loop) so tests and AC-013's restart scenario
// can drive a single pass deterministically without waiting a full
// interval.
func (s *LocalRetentionScheduler) SweepOnce(ctx context.Context) (RetentionResult, error) {
	policy, err := ReadRetentionPolicy(ctx, s.db)
	if err != nil {
		return RetentionResult{}, err
	}
	window := time.Duration(policy.WindowDays) * 24 * time.Hour
	if policy.Kind != RetentionKeepForever && policy.WindowDays <= 0 {
		// Defensive: a delete/archive strategy with no window persisted
		// (should not happen via SetAuditSettings, which always sets
		// one) falls back to a generous default rather than a
		// zero-duration window that would delete everything on the
		// next tick.
		window = DefaultRetentionWindowDays * 24 * time.Hour
	}
	return RetentionSweep(ctx, s.backend, s.dataDir, policy.Kind, window)
}
