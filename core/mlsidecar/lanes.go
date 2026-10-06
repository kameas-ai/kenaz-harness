package mlsidecar

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/kameas-ai/kenaz-harness/core/logging"
)

// laneVerdict is one lane candidate's classification by the identity
// check (owner ruling A5.2: the existing adoption verdict is the
// per-candidate arbiter).
type laneVerdict string

const (
	// laneFree: nothing answered /health (transport failure) — a spawn
	// target.
	laneFree laneVerdict = "free"
	// laneForeign: something answered but is not our engine — an
	// unreadable /health (ErrUnusableResponse), a pre-lease/legacy
	// /health (sigild's bare {"status":"ok"} lands here), or an exe_path
	// outside this root's `current` (AdoptPortConflict). Skipped, never
	// spawned over, never killed.
	laneForeign laneVerdict = "foreign"
	// laneOurs: an engine of THIS root, adopted (engine.port recorded):
	// either the verified `current` engine (AdoptAccept, healthy), or an
	// UPDATE-PENDING one — a lease-aware engine whose exe_path is under
	// this root's versions/ but not `current` (the old version still
	// serving after an update flipped `current`; kenaz parity (c)). The
	// latter is adopted so no second engine is spawned onto another lane
	// of the same lease dir, but it is never reported healthy (it was not
	// verified against `current`) and never leased by this client, so it
	// still stops on its token shutdown / idle exit and is respawned from
	// `current` on the next Ensure.
	laneOurs laneVerdict = "ours"
	// laneTerminal: a lease-aware engine this client must not route
	// around — one claiming this root's install whose bytes fail
	// verification (AdoptRefuseUnverified), or a contract-major this build
	// cannot speak (AdoptContractUnsupported). Spawning a second engine
	// onto another lane of the same root would put two engines on one
	// lease directory, so the scan stops and reports it.
	laneTerminal laneVerdict = "terminal"
)

type laneProbe struct {
	port    int
	verdict laneVerdict
	status  Status // the verdict's status (zero for laneFree)
	healthy bool   // laneOurs AND verified against `current` (AdoptAccept)
}

// label is the probe's entry in a scanned-list detail ("7785 legacy_unverified").
func (lp laneProbe) label() string {
	if lp.verdict == laneFree {
		return fmt.Sprintf("%d free", lp.port)
	}
	what := string(lp.status.State)
	if lp.status.Reason != ReasonNone {
		what += "/" + string(lp.status.Reason)
	}
	return fmt.Sprintf("%d %s", lp.port, what)
}

// probeLane runs the identity check against one candidate port. It never
// renews a lease and never writes engine.port — callers decide.
func (m *Manager) probeLane(ctx context.Context, port int) laneProbe {
	health, err := m.Client.at(port).Health(ctx)
	if err != nil {
		if errors.Is(err, ErrUnusableResponse) {
			return laneProbe{port: port, verdict: laneForeign, status: occupiedStatus(err)}
		}
		return laneProbe{port: port, verdict: laneFree}
	}
	decision, derr := EvaluateAdoption(m.Layout, health, m.tv)
	if derr != nil {
		return laneProbe{port: port, verdict: laneTerminal,
			status: Status{State: StateInstalledUnhealthy, Reason: ReasonCrash, Detail: derr.Error(), UpdatedAt: time.Now()}}
	}
	st := statusForDecision(decision, health)
	switch decision.Action {
	case AdoptAccept:
		st.Detail += fmt.Sprintf(" (port %d)", port)
		return laneProbe{port: port, verdict: laneOurs, status: st, healthy: true}
	case AdoptPortConflict:
		if pathIsWithin(health.ExePath, m.Layout.VersionsDir()) {
			return laneProbe{port: port, verdict: laneOurs, status: Status{
				State: StateInstalledUnhealthy, Reason: ReasonUpdatePending, EngineVersion: health.SidecarVersion,
				ContractVersion: health.LifecycleProtocol,
				Detail:          fmt.Sprintf("an engine from this install root that is not `current` is serving on port %d (update pending): adopted, not used; it is respawned from `current` once it stops", port),
				UpdatedAt:       time.Now(),
			}}
		}
		return laneProbe{port: port, verdict: laneForeign, status: st}
	case AdoptLegacyUnverified:
		return laneProbe{port: port, verdict: laneForeign, status: st}
	default:
		return laneProbe{port: port, verdict: laneTerminal, status: st}
	}
}

// laneScan is the outcome of probing the record and then the lane.
type laneScan struct {
	probes []laneProbe // every candidate probed, in lane order
	found  *laneProbe  // ours or terminal: stop here
	free   int         // first free candidate, 0 if none
}

// scanLanes probes engine.port first (if recorded and in-lane), then the
// whole lane in order (kenaz parity (d)): no listener -> remember the
// first free candidate and KEEP LOOKING for ours (a free base must never
// get a second engine while ours runs on a fallback lane); ours ->
// stop, adopt; foreign -> next; terminal (refuse-unverified /
// contract-unsupported) -> stop: a refusal is a safety stop, never
// stepped around. A stale record (dead or foreign) is ignored, not
// removed — the next adopt/spawn overwrites it, and an all-foreign scan
// leaves the file untouched. Garbage or out-of-lane records are logged
// and treated as absent. Pure probing: no spawn, no lease, no write.
func (m *Manager) scanLanes(ctx context.Context) laneScan {
	var sc laneScan
	rec, ok, rerr := RecordedEnginePort(m.Layout, m.BasePort)
	if rerr != nil {
		logging.L().Warn("mlsidecar.engine_port.ignored", "err", rerr.Error())
	}
	if ok {
		lp := m.probeLane(ctx, rec)
		if lp.verdict == laneOurs || lp.verdict == laneTerminal {
			sc.probes = []laneProbe{lp}
			sc.found = &sc.probes[0]
			return sc
		}
		// Stale (dead or foreign): ignored, the lane scan reruns.
	}
	for _, port := range CandidatePorts(m.BasePort) {
		lp := m.probeLane(ctx, port)
		sc.probes = append(sc.probes, lp)
		switch lp.verdict {
		case laneOurs, laneTerminal:
			sc.found = &sc.probes[len(sc.probes)-1]
			return sc
		case laneFree:
			if sc.free == 0 {
				sc.free = port
			}
		}
	}
	return sc
}

// exhaustedStatus is the "every lane candidate holds a foreign listener"
// status (kenaz parity (d)): installed_unhealthy / port_conflict, with
// every scanned candidate and its verdict named in Detail (e.g. "7785
// legacy_unverified/legacy_engine" for sigild's bare {"status":"ok"}).
// engine.port is never written for it.
func exhaustedStatus(probes []laneProbe) Status {
	labels := make([]string, len(probes))
	for i, lp := range probes {
		labels[i] = lp.label()
	}
	return Status{State: StateInstalledUnhealthy, Reason: ReasonPortConflict, UpdatedAt: time.Now(),
		Detail: fmt.Sprintf("all %d engine ports are held by foreign listeners (scanned: %s)", len(probes), strings.Join(labels, ", "))}
}

// adoptLane records an adopted engine's port (only when it differs) and
// renews this client's lease for a healthy one.
func (m *Manager) adoptLane(lp laneProbe) {
	if rec, ok, _ := RecordedEnginePort(m.Layout, m.BasePort); !ok || rec != lp.port {
		if err := WriteEnginePort(m.Layout, lp.port); err != nil {
			logging.L().Warn("mlsidecar.engine_port.write_failed", "port", lp.port, "err", err.Error())
		}
	}
	if lp.healthy {
		m.renewLease()
	}
}

// reconcileLanes is reconcileLocked in lane mode: adopt our engine
// wherever it is on the lane (recording its port), else spawn on the
// first free candidate, else — every candidate foreign — report the
// exhausted status. Every Ensure re-runs it, so when the engine stops
// answering on the recorded port the lane is rescanned BEFORE anything
// is spawned (kenaz parity (e)): an engine the other client respawned on
// a different lane port is found and adopted instead of double-spawned.
func (m *Manager) reconcileLanes(ctx context.Context) Status {
	sc := m.scanLanes(ctx)
	if sc.found != nil {
		if sc.found.verdict == laneOurs {
			m.adoptLane(*sc.found)
		}
		return sc.found.status
	}
	if sc.free == 0 {
		return exhaustedStatus(sc.probes)
	}
	return m.spawnLocked(ctx, sc.free)
}

// observeLanes is Observe in lane mode: the same scan, read-only (no
// spawn, no lease, no engine.port write or removal). running reports
// whether anything relevant answered.
func (m *Manager) observeLanes(ctx context.Context, cur Status) (Status, bool) {
	sc := m.scanLanes(ctx)
	var st Status
	switch {
	case sc.found != nil:
		st = sc.found.status
	case sc.free == 0:
		st = exhaustedStatus(sc.probes)
	default:
		// Nothing of ours is running and a lane is free: the idle case.
		return m.demoteIfHealthy(cur), false
	}
	if !m.mu.TryLock() {
		return m.Status(), true
	}
	defer m.mu.Unlock()
	return m.setStatus(st), true
}
