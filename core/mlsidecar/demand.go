package mlsidecar

import (
	"context"
	"sync"
	"time"
)

// DemandProbe is the advisor-facing probe (advice.SidecarProbe,
// structurally): "lazy start on first advisor demand" made concrete
// (laya-advisors-01LAYA001 WP13; spec §2c, design §3.5/§6.2).
//
// Healthy() answers from the Manager's cached Status and returns
// immediately — an advisor call carries an 800ms budget and must never
// wait on a spawn. What makes it the lazy-start trigger is the side
// effect: a demand (an actual Healthy() call from an advisor or the label
// pusher) schedules ONE throttled background Manager.Ensure, which
// adopts-or-spawns the installed engine, heartbeats the lease and
// detects a dead process. So:
//
//   - A user who never enabled recommendations: Installed() is false, the
//     background step is a no-op — no port dial, no spawn, zero cost.
//   - First demand after enabling/app start: the call itself falls back
//     (status is not yet healthy — the advisor's per-call heuristic
//     fallback), the background Ensure starts the engine, later calls see
//     healthy.
//   - Steady state: at most one Ensure per MinInterval (the 30s lease
//     heartbeat cadence of design §3.5), demand-driven — with no advisor
//     demand there is no timer, the lease lapses and the engine
//     self-terminates after its 120s zero-lease window (no idle RAM).
//
// The app-boot resolve (core/rpc's advice.laya_ladder.boot_resolve) must
// be handed the Manager itself (cache-only Healthy), NOT this type, so
// boot can never be a lazy-start trigger.
type DemandProbe struct {
	M *Manager
	// MinInterval throttles background Ensure calls (default 30s).
	MinInterval time.Duration
	// Timeout bounds one background Ensure (default 90s — covers
	// Manager.StartupWait plus adoption checks).
	Timeout time.Duration
	// Now is the clock (tests inject one).
	Now func() time.Time

	mu       sync.Mutex
	last     time.Time
	inflight bool
	wg       sync.WaitGroup
}

// Healthy implements advice.SidecarProbe.
func (d *DemandProbe) Healthy() bool {
	if d == nil || d.M == nil {
		return false
	}
	d.kick()
	return d.M.Healthy()
}

// Identity implements advice.SidecarProbe.
func (d *DemandProbe) Identity() string {
	if d == nil || d.M == nil {
		return "kenaz-ml-sidecar"
	}
	return d.M.Identity()
}

func (d *DemandProbe) now() time.Time {
	if d.Now != nil {
		return d.Now()
	}
	return time.Now()
}

func (d *DemandProbe) kick() {
	interval := d.MinInterval
	if interval <= 0 {
		interval = HeartbeatInterval
	}
	d.mu.Lock()
	now := d.now()
	if d.inflight || (!d.last.IsZero() && now.Sub(d.last) < interval) {
		d.mu.Unlock()
		return
	}
	d.last = now
	d.mu.Unlock()

	// Never-enabled users: no install record, nothing to start.
	if _, ok := d.M.Installed(); !ok {
		return
	}
	// An install/update is in flight — it reconciles itself on success.
	if d.M.Status().State == StateInstalling {
		return
	}

	d.mu.Lock()
	if d.inflight {
		d.mu.Unlock()
		return
	}
	d.inflight = true
	d.wg.Add(1)
	d.mu.Unlock()

	timeout := d.Timeout
	if timeout <= 0 {
		timeout = 90 * time.Second
	}
	go func() {
		defer d.wg.Done()
		ctx, cancel := context.WithTimeout(context.Background(), timeout)
		defer cancel()
		d.M.Ensure(ctx)
		d.mu.Lock()
		d.inflight = false
		d.mu.Unlock()
	}()
}

// Wait blocks until any in-flight background Ensure finishes. Used by
// tests and by shutdown so no goroutine outlives its owner.
func (d *DemandProbe) Wait() { d.wg.Wait() }
