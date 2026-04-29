package stdio

import (
	"context"
	"time"
)

// runSupervisor is the long-lived restart driver for one server
// instance. It listens on the per-generation crashCh; when a crash
// is signalled (reader EOF, writer broken pipe, or two consecutive
// failed pings), it consults restartHistory and either:
//
//   - prunes entries older than restartWindow, sleeps the next
//     backoff step, respawns the process, and re-runs the
//     initialize handshake (transitioning state through
//     restarting → running on success); or
//
//   - if the pruned history already holds restartWindow attempts,
//     marks state=failed and exits. Failed is sticky until a user-
//     driven toggle off/on tears the instance down (Close) and the
//     pool spawns a fresh ServerInstance with empty history.
//
// The supervisor exits when (a) Close fires (doneCh closed) or
// (b) state transitions to failed. Either way it decrements its
// supervisorWG entry on return.
//
// First-init failure does NOT reach here — Spawn returns the error
// directly and never starts the supervisor (per spec.md §9 edge
// case 2).
func (s *ServerInstance) runSupervisor() {
	defer s.supervisorWG.Done()

	for {
		// Snapshot the current generation's crashCh under the
		// lifecycle lock. Each successful respawn allocates a fresh
		// crashCh, so reading it once per loop iteration ensures we
		// always wait on the live one.
		s.lifecycleMu.Lock()
		crashCh := s.crashCh
		s.lifecycleMu.Unlock()

		select {
		case <-s.doneCh:
			return
		case <-crashCh:
			// Fall through to restart logic.
		}

		// Close path may have set the closing flag between the
		// crash and our wakeup; if so, exit cleanly without
		// attempting a restart.
		if s.closing.Load() {
			return
		}

		if !s.attemptRestart() {
			// State has been transitioned to failed by
			// attemptRestart; exit so the goroutine count returns
			// to baseline. The instance stays in failed until Close
			// + a fresh Spawn elsewhere clears the history.
			return
		}
	}
}

// attemptRestart drives the restart cycle until the instance is
// either back in the running state or marked failed. It loops
// internally on respawn failures (each one consumes a backoff
// slot) so the caller's main supervisor loop only re-enters when
// a fresh, post-init crash arrives.
//
// Returns true if the instance is back in running and the
// supervisor should keep listening; false if the instance is
// failed (or Close fired) and the supervisor should exit.
func (s *ServerInstance) attemptRestart() bool {
	// One-time tear-down of the generation that just crashed.
	s.lifecycleMu.Lock()
	s.state = StateRestarting
	prevCmd := s.cmd
	prevStdin := s.stdin
	s.lifecycleMu.Unlock()

	// Cancel any pending callers immediately — they'll see
	// "stdio: call cancelled" and the user-facing error path takes
	// over while we restart.
	s.router.CancelAll()

	// Reap the previous process. SIGKILL guarantees we don't leak
	// a zombie even if the crash signal came from the writer side
	// (process may still be alive briefly).
	if prevStdin != nil {
		_ = prevStdin.Close()
	}
	if prevCmd != nil && prevCmd.Process != nil {
		_ = prevCmd.Process.Kill()
		_ = prevCmd.Wait()
	}
	s.readerWG.Wait()

	for {
		if s.closing.Load() {
			return false
		}

		s.lifecycleMu.Lock()
		now := s.opts.Now()
		s.restartHistory = pruneHistory(s.restartHistory, now, restartWindow)
		attemptIndex := len(s.restartHistory)
		spec := s.spec
		s.lifecycleMu.Unlock()

		if attemptIndex >= len(backoffSchedule) {
			s.lifecycleMu.Lock()
			s.state = StateFailed
			attempts := len(s.restartHistory)
			s.lifecycleMu.Unlock()
			s.logger.Error("mcp.recipe.failed",
				"recipe", s.id,
				"attempts", attempts,
				"window_ms", restartWindow.Milliseconds(),
			)
			return false
		}

		backoff := backoffSchedule[attemptIndex]
		s.logger.Warn("mcp.recipe.restarting",
			"recipe", s.id,
			"attempt", attemptIndex+1,
			"backoff_ms", backoff.Milliseconds(),
		)

		if !s.sleepOrDone(backoff) {
			return false
		}
		if s.closing.Load() {
			return false
		}

		// Respawn with a generous handshake budget; ctx.Background
		// is fine — the supervisor lifetime is bounded by doneCh
		// (Close) and the spawn itself has its own initTimeout.
		ctx, cancel := context.WithTimeout(context.Background(), respawnCtxBudget(spec))
		err := s.doSpawn(ctx, spec)
		cancel()

		// Either way, the attempt counted: append now to history.
		s.lifecycleMu.Lock()
		s.restartHistory = append(s.restartHistory, s.opts.Now())
		s.lastRestartAt = s.restartHistory[len(s.restartHistory)-1]
		if err != nil {
			s.lastError = err.Error()
		}
		s.lifecycleMu.Unlock()

		if err != nil {
			s.logger.Warn("mcp.recipe.restart_failed",
				"recipe", s.id,
				"attempt", attemptIndex+1,
				"err", err.Error(),
			)
			// Loop: next iteration either tries again (if budget
			// remains) or transitions to failed.
			continue
		}

		s.lifecycleMu.Lock()
		s.state = StateRunning
		s.lifecycleMu.Unlock()
		s.logger.Info("mcp.recipe.restarted",
			"recipe", s.id,
			"attempt", attemptIndex+1,
		)
		return true
	}
}

// sleepOrDone blocks for d using the injected Sleep but bails out
// early if Close fires. Returns true if the full duration elapsed,
// false if doneCh closed first.
//
// We dispatch the injected Sleep on a goroutine so the test stub
// (which may return instantly) and the real time.Sleep both
// integrate uniformly with the doneCh select.
func (s *ServerInstance) sleepOrDone(d time.Duration) bool {
	if d <= 0 {
		return true
	}
	done := make(chan struct{})
	go func() {
		s.opts.Sleep(d)
		close(done)
	}()
	select {
	case <-done:
		return true
	case <-s.doneCh:
		// Sleep goroutine still pending — leak it briefly. With the
		// real time.Sleep that's at most one backoff step
		// (max 4 s); with the injected stub it's instant.
		return false
	}
}

// respawnCtxBudget returns the wall-clock budget for one respawn
// attempt: first-byte timeout + init timeout + 1 s slack. Defaults
// to the package defaults when the spec leaves them zero.
func respawnCtxBudget(spec SpawnSpec) time.Duration {
	first := spec.FirstByteTimeout
	if first <= 0 {
		first = defaultFirstByteTimeout
	}
	init := spec.InitTimeout
	if init <= 0 {
		init = defaultInitTimeout
	}
	return first + init + time.Second
}
