package stdio

import (
	"context"
)

// healthPinger ticks at recipe.PingPeriodMs (default 30 s) and
// issues a `ping` JSON-RPC request to the server. The MCP spec
// promises a no-op response within the response deadline; two
// consecutive misses (timeout, transport error, or any other
// non-success outcome) signal a crash so the supervisor restarts
// the server (FR-008, A8).
//
// The goroutine exits when Close fires. Per-tick failures reset
// once a successful ping comes back, so a transient hiccup does
// not snowball into a restart.
func (s *ServerInstance) healthPinger() {
	defer s.supervisorWG.Done()

	period := s.pingPeriod()
	if period <= 0 {
		// Ping disabled (negative spec value). Nothing to do.
		return
	}
	ticker := s.opts.NewTicker(period)
	defer ticker.Stop()

	consecutiveFailures := 0
	for {
		select {
		case <-s.doneCh:
			return
		case <-ticker.C():
			ok := s.sendPing()
			// During a restart, the supervisor has cancelled the
			// router and may have torn down the framer. A failed
			// ping in that window is not the user's problem and
			// must not feed back into the failure counter.
			if !ok && s.isRestarting() {
				continue
			}
			if ok {
				consecutiveFailures = 0
				continue
			}
			consecutiveFailures++
			s.logger.Debug("mcp.recipe.ping_failed",
				"recipe", s.id,
				"consecutive", consecutiveFailures,
			)
			if consecutiveFailures >= 2 {
				// Trip a restart by signalling crash. The supervisor
				// owns history accounting; we just close the
				// channel.
				s.signalCrash("two consecutive ping failures")
				// Reset so we don't double-trip on the next tick
				// while the supervisor is still tearing the
				// previous generation down.
				consecutiveFailures = 0
			}
		}
	}
}

// sendPing issues one ping with the configured deadline and
// returns true on a successful response.
func (s *ServerInstance) sendPing() bool {
	ctx, cancel := context.WithTimeout(context.Background(), s.pingTimeout())
	defer cancel()
	_, err := s.callRaw(ctx, MethodPing, nil)
	return err == nil
}

// isRestarting reports whether the supervisor is currently between
// crash detection and a fresh handshake. Used by healthPinger to
// suppress spurious failure accumulation.
func (s *ServerInstance) isRestarting() bool {
	s.lifecycleMu.Lock()
	defer s.lifecycleMu.Unlock()
	return s.state == StateRestarting
}
