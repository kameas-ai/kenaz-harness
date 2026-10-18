package mlsidecar

import (
	"context"
	"errors"
	"time"
)

// Observe is the read-only sibling of Reconcile, for the Settings status
// line (laya-advisors-01LAYA001 WP13): it asks "what is actually on the
// port right now?" and records the answer, but NEVER spawns, leases or
// downloads anything — opening a settings panel is not advisor demand, so
// it must not be a lazy-start trigger.
//
//   - /health answers: the answer goes through the same adoption
//     verdict Reconcile uses (healthy / legacy_unverified / unverified /
//     contract_unsupported / port conflict), cached, running=true. (A
//     Kenaz-owned legacy engine on the port surfaces here even when this
//     client installed nothing — that is the "update Kenaz to share the
//     ML engine" case.)
//   - /health answers with something unusable (ErrUnusableResponse: a
//     non-2xx, or a body that is not a health payload): the port is
//     OCCUPIED — port_conflict, running=true, never "stopped when idle".
//   - /health does not answer: running=false. The cached status is left
//     alone EXCEPT that a cached `healthy` is demoted — a healthy claim
//     about a process that is no longer there would make the advisor
//     ladder resolve rung 2 against nothing (the engine self-terminates
//     after its 120s zero-lease window, so this is the normal idle case,
//     not a fault: Reason stays empty and the Settings panel says "starts
//     when needed", never "unhealthy").
//
// While an install/update is in flight the cached installing status is
// returned untouched.
func (m *Manager) Observe(ctx context.Context) (Status, bool) {
	cur := m.Status()
	if cur.State == StateInstalling || m.Client == nil {
		return cur, false
	}
	if m.BasePort > 0 {
		// Lane mode: the same per-candidate identity scan Reconcile runs
		// (lanes.go), read-only. A foreign listener on the base port (e.g.
		// sigild) is not "our engine is legacy" while a lane is free — it
		// is only reported when every candidate is foreign.
		return m.observeLanes(ctx, cur)
	}
	health, err := m.Client.Health(ctx)
	if err == nil {
		// Serialize with spawn/install via the op lock so a concurrent
		// Reconcile's richer status is not clobbered mid-flight.
		if !m.mu.TryLock() {
			return m.Status(), true
		}
		defer m.mu.Unlock()
		return m.setStatus(m.evaluate(health, false)), true
	}
	if errors.Is(err, ErrUnusableResponse) {
		// Occupied, not gone: something answered on the port without a
		// usable /health payload (same verdict Reconcile reaches). Never
		// demoted to "stopped itself when idle" — the port is taken.
		if !m.mu.TryLock() {
			return m.Status(), true
		}
		defer m.mu.Unlock()
		return m.setStatus(occupiedStatus(err)), true
	}
	return m.demoteIfHealthy(cur), false
}

// demoteIfHealthy is Observe's "nothing is answering" outcome: a cached
// healthy claim is demoted (the engine stopped itself when idle), any
// other cached status is left alone.
func (m *Manager) demoteIfHealthy(cur Status) Status {
	if cur.State == StateHealthy {
		return m.setStatus(Status{
			State:         StateInstalledUnhealthy,
			EngineVersion: cur.EngineVersion,
			Detail:        "engine is not running (it stops itself when idle and starts again when needed)",
			UpdatedAt:     time.Now(),
		})
	}
	return cur
}
