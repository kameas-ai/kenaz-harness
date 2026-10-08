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
// pusher) starts ONE background keepalive loop, whose every iteration is a
// Manager.Ensure — adopt-or-spawn the installed engine, heartbeat the
// lease, detect a dead process.
//
// # Lease cadence guarantee (Amendment A5: Kenaz judges our liveness by
// the lease file's mtime with a 90s staleness rule)
//
// While the engine is IN USE — at least one demand within IdleAfter —
// the loop runs Ensure every MinInterval (default 30s = HeartbeatInterval,
// three beats inside the 90s bound) REGARDLESS of whether further demands
// arrive: the heartbeat is the loop's clock, not the advisor's. A quiet
// minute between two advisor calls therefore never lets the lease lapse
// under a healthy, in-use engine. Once IdleAfter (default 5m) passes with
// no demand the loop exits, the lease is left to lapse, and the engine
// self-terminates after its own 120s zero-lease window — no idle RAM for
// a user who stopped using recommendations. The next demand restarts the
// loop.
//
//   - A user who never enabled recommendations: Installed() is false, no
//     loop starts — no port dial, no spawn, zero cost (and the
//     install.json read is itself throttled to MinInterval).
//   - First demand after enabling/app start: the call never waits; it
//     normally falls back (status is not yet healthy — the advisor's
//     per-call heuristic fallback), the loop's first Ensure starts the
//     engine, later calls see healthy. If an engine is ALREADY running,
//     the first Ensure can adopt it before this call reads the cache, and
//     the first call then truthfully answers healthy — see Healthy.
//
// The app-boot resolve (core/rpc's advice.laya_ladder.boot_resolve) must
// be handed the Manager itself (cache-only Healthy), NOT this type, so
// boot can never be a lazy-start trigger.
type DemandProbe struct {
	M *Manager
	// MinInterval is the keepalive/Ensure cadence (default 30s). Keep it
	// comfortably under StaleAfter/2.
	MinInterval time.Duration
	// IdleAfter is how long the loop keeps heartbeating after the LAST
	// demand (default 5m).
	IdleAfter time.Duration
	// Timeout bounds one Ensure (default 90s — covers Manager.StartupWait
	// plus adoption checks).
	Timeout time.Duration
	// Now is the clock (tests inject one).
	Now func() time.Time
	// Sleep waits d or until ctx ends; nil uses a real timer. Tests inject
	// a tick-controlled version so the cadence is asserted, not slept.
	Sleep func(ctx context.Context, d time.Duration)

	mu         sync.Mutex
	lastDemand time.Time
	recheckAt  time.Time // earliest next install.json read when nothing is installed
	running    bool
	closed     bool
	cancel     context.CancelFunc
	wg         sync.WaitGroup
}

// Healthy implements advice.SidecarProbe.
//
// It kicks the demand BEFORE reading the cache, deliberately: the answer
// is the Manager's current cached status, and a background adopt that
// lands in between makes it true only because the engine really is
// healthy. Reading first would make the first call deterministically
// "not yet" but buys nothing — the contract is never-block plus per-call
// fallback, not "the first call fails" — and would discard a true answer.
// (Reviewed 2026-10-07 for the TestDemandProbe_FirstDemandStartsEngine
// flake: the race was in the test's assertion, not here.)
func (d *DemandProbe) Healthy() bool {
	if d == nil || d.M == nil {
		return false
	}
	d.demand()
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

func (d *DemandProbe) interval() time.Duration {
	if d.MinInterval > 0 {
		return d.MinInterval
	}
	return HeartbeatInterval
}

func (d *DemandProbe) idleAfter() time.Duration {
	if d.IdleAfter > 0 {
		return d.IdleAfter
	}
	return 5 * time.Minute
}

func (d *DemandProbe) sleep(ctx context.Context, dur time.Duration) {
	if d.Sleep != nil {
		d.Sleep(ctx, dur)
		return
	}
	t := time.NewTimer(dur)
	defer t.Stop()
	select {
	case <-t.C:
	case <-ctx.Done():
	}
}

// demand records an advisor demand and starts the keepalive loop if it is
// not already running and an engine is installed.
func (d *DemandProbe) demand() {
	now := d.now()
	d.mu.Lock()
	if d.closed {
		d.mu.Unlock()
		return
	}
	d.lastDemand = now
	if d.running || now.Before(d.recheckAt) {
		d.mu.Unlock()
		return
	}
	d.mu.Unlock()

	// Never-enabled users: no install record, nothing to start. The file
	// read is throttled so a stream of demands does not re-read it.
	if _, ok := d.M.Installed(); !ok {
		d.mu.Lock()
		d.recheckAt = now.Add(d.interval())
		d.mu.Unlock()
		return
	}

	d.mu.Lock()
	if d.closed || d.running {
		d.mu.Unlock()
		return
	}
	d.running = true
	ctx, cancel := context.WithCancel(context.Background())
	d.cancel = cancel
	d.wg.Add(1)
	d.mu.Unlock()
	go d.loop(ctx, cancel)
}

func (d *DemandProbe) loop(ctx context.Context, cancel context.CancelFunc) {
	defer d.wg.Done()
	defer cancel()
	timeout := d.Timeout
	if timeout <= 0 {
		timeout = 90 * time.Second
	}
	for {
		// An install/update in flight reconciles itself on success.
		if d.M.Status().State != StateInstalling {
			ectx, ecancel := context.WithTimeout(ctx, timeout)
			d.M.Ensure(ectx)
			ecancel()
		}
		if ctx.Err() != nil {
			d.finish()
			return
		}
		d.sleep(ctx, d.interval())
		if ctx.Err() != nil {
			d.finish()
			return
		}
		// Stop heartbeating once the engine has been idle for IdleAfter
		// (or was uninstalled). The idle check and the running=false flip
		// share one critical section so a demand racing the exit either
		// extends lastDemand before the check or finds running=false and
		// restarts the loop — never a lost wakeup.
		_, installed := d.M.Installed()
		d.mu.Lock()
		if !installed || d.now().Sub(d.lastDemand) > d.idleAfter() {
			d.running = false
			d.cancel = nil
			d.mu.Unlock()
			return
		}
		d.mu.Unlock()
	}
}

func (d *DemandProbe) finish() {
	d.mu.Lock()
	d.running = false
	d.cancel = nil
	d.mu.Unlock()
}

// Wait blocks until the keepalive loop (if any) has exited — only ever
// returns once the probe is idle-expired, uninstalled or Closed. Tests
// use Close; Wait exists for idle-expiry assertions.
func (d *DemandProbe) Wait() { d.wg.Wait() }

// Close stops the probe: later demands schedule nothing, an in-flight
// Ensure is cancelled (its spawn-wait ends at once), and Close returns
// only after the loop goroutine has exited — so app shutdown never waits
// out a 30s startup poll and no goroutine outlives its owner. Idempotent.
func (d *DemandProbe) Close() {
	if d == nil {
		return
	}
	d.mu.Lock()
	d.closed = true
	cancel := d.cancel
	d.mu.Unlock()
	if cancel != nil {
		cancel()
	}
	d.wg.Wait()
}
