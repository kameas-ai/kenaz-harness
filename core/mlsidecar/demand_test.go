package mlsidecar

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/kameas-ai/kenaz-harness/core/advice"
)

// countingEngine is a minimal engine double that counts /health hits, so
// "nothing dials the port" and "one Ensure per tick" are asserted as call
// counts, not inferred.
type countingEngine struct {
	hits   atomic.Int64
	mu     sync.Mutex
	health HealthPayload
	// hold, when non-nil, parks every /health response until it is closed
	// (or the request ends) — lets a test pin "the engine has not answered
	// yet" without racing the probe's background Ensure.
	hold chan struct{}
	srv  *httptest.Server
}

func newCountingEngine(t *testing.T) *countingEngine {
	t.Helper()
	e := &countingEngine{}
	e.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/health" {
			e.mu.Lock()
			hold := e.hold
			e.mu.Unlock()
			if hold != nil {
				select {
				case <-hold:
				case <-r.Context().Done():
					return
				}
			}
			e.hits.Add(1)
			e.mu.Lock()
			h := e.health
			e.mu.Unlock()
			_ = json.NewEncoder(w).Encode(h)
			return
		}
		w.WriteHeader(http.StatusNotFound)
	}))
	t.Cleanup(e.srv.Close)
	return e
}

// holdHealth parks /health responses until the returned release is called.
func (e *countingEngine) holdHealth() (release func()) {
	ch := make(chan struct{})
	e.mu.Lock()
	e.hold = ch
	e.mu.Unlock()
	var once sync.Once
	return func() {
		once.Do(func() {
			e.mu.Lock()
			e.hold = nil
			e.mu.Unlock()
			close(ch)
		})
	}
}

func (e *countingEngine) setHealth(h HealthPayload) {
	e.mu.Lock()
	e.health = h
	e.mu.Unlock()
}

// fakeClock is a race-safe controllable clock.
type fakeClock struct {
	mu sync.Mutex
	t  time.Time
}

func (c *fakeClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.t
}
func (c *fakeClock) Advance(d time.Duration) {
	c.mu.Lock()
	c.t = c.t.Add(d)
	c.mu.Unlock()
}

// tickSleep is the probe's injected Sleep: it blocks until the test sends
// a tick (or the probe is closed), making the keepalive cadence a thing
// the test drives instead of waits for.
type tickSleep struct{ ch chan struct{} }

func newTickSleep() *tickSleep { return &tickSleep{ch: make(chan struct{})} }

func (s *tickSleep) Sleep(ctx context.Context, _ time.Duration) {
	select {
	case <-s.ch:
	case <-ctx.Done():
	}
}

// tick releases exactly one sleeping loop iteration (blocks until the loop
// is asleep, so it is also a barrier on the previous Ensure finishing).
func (s *tickSleep) tick(t *testing.T) {
	t.Helper()
	select {
	case s.ch <- struct{}{}:
	case <-time.After(5 * time.Second):
		t.Fatal("keepalive loop never reached its sleep (no loop running?)")
	}
}

func waitFor(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatalf("timed out waiting for %s", what)
}

// installedLayout returns a layout with a verified install at version
// 1.2.0 plus the health payload a verified, running engine would report
// for it (exe_path under current, the recorded launcher digest).
func installedLayout(t *testing.T) (Layout, HealthPayload) {
	t.Helper()
	l := NewLayout(t.TempDir())
	exe, sha := setupVerifiedVersion(t, l, "1.2.0", []byte("launcher"))
	return l, HealthPayload{
		Product:           "kameas-ml",
		SidecarVersion:    "1.2.0",
		ExePath:           exe,
		EngineSHA256:      sha,
		LifecycleProtocol: 1,
	}
}

// TestDemandProbe_NeverEnabled_DialsNothing is the zero-cost proof for a
// user who never clicked Enable: any number of advisor demands produce no
// /health request, no spawn, no state change.
func TestDemandProbe_NeverEnabled_DialsNothing(t *testing.T) {
	eng := newCountingEngine(t)
	l := NewLayout(t.TempDir()) // nothing installed
	spawner := &fakeSpawner{}
	m := NewManager(l, NewClient(eng.srv.URL, nil), spawner, "harness", "0.85.0")
	p := &DemandProbe{M: m}
	defer p.Close()
	for i := 0; i < 5; i++ {
		if p.Healthy() {
			t.Fatal("never-enabled probe reported healthy")
		}
	}
	p.Wait()
	if eng.hits.Load() != 0 || spawner.called {
		t.Fatalf("never-enabled demand dialed the port %d time(s), spawned=%v", eng.hits.Load(), spawner.called)
	}
}

// TestManager_CacheOnlyHealthy_NeverDials: the boot-time resolver is given
// the Manager itself, whose Healthy() is cache-only — app boot must never
// be a lazy-start trigger.
func TestManager_CacheOnlyHealthy_NeverDials(t *testing.T) {
	eng := newCountingEngine(t)
	l, h := installedLayout(t)
	eng.setHealth(h)
	spawner := &fakeSpawner{}
	m := NewManager(l, NewClient(eng.srv.URL, nil), spawner, "harness", "0.85.0")
	for i := 0; i < 3; i++ {
		if m.Healthy() {
			t.Fatal("cache-only Healthy() must be false before any Ensure")
		}
	}
	if _, _, rung, _, ok := advice.ResolveAdvisorModel(advice.AdvisorModelSetting{}, nil, m); ok || rung == advice.RungLocalLaya {
		t.Fatalf("boot resolve (rung=%q ok=%v) must not reach rung 2 before demand", rung, ok)
	}
	if eng.hits.Load() != 0 || spawner.called {
		t.Fatalf("boot-time resolve dialed the port %d time(s) / spawned=%v", eng.hits.Load(), spawner.called)
	}
}

// TestDemandProbe_FirstDemandStartsEngine_OneEnsurePerTick: the first
// demand falls back (per-call heuristic) but starts the loop; its first
// Ensure adopts the verified running engine; later demands see healthy and
// do NOT re-dial — Ensure runs once per tick, not once per demand.
func TestDemandProbe_FirstDemandStartsEngine_OneEnsurePerTick(t *testing.T) {
	eng := newCountingEngine(t)
	l, h := installedLayout(t)
	eng.setHealth(h)
	m := NewManager(l, NewClient(eng.srv.URL, nil), &fakeSpawner{}, "harness", "0.85.0")
	tk := newTickSleep()
	clock := &fakeClock{t: time.Unix(1_700_000_000, 0)}
	p := &DemandProbe{M: m, MinInterval: 30 * time.Second, IdleAfter: 5 * time.Minute, Now: clock.Now, Sleep: tk.Sleep}
	defer p.Close()

	// Park the engine's /health answer so the loop's first Ensure cannot
	// adopt before the first call reads the cache. Without this the
	// assertion below raced the background adopt (ledger 2026-10-07 CI
	// entry): a fast adopt made the first call truthfully report healthy.
	// Holding it also makes "does not block" a real check — the call
	// returns while the engine has not answered.
	release := eng.holdHealth()
	defer release()
	if p.Healthy() {
		t.Fatal("first demand must not block on / wait for the engine (per-call fallback)")
	}
	release()
	waitFor(t, "the first Ensure to adopt the engine", func() bool { return m.Healthy() })
	if !p.Healthy() {
		t.Fatal("second demand must see the engine healthy")
	}
	for i := 0; i < 20; i++ {
		p.Healthy()
	}
	if got := eng.hits.Load(); got != 1 {
		t.Fatalf("health hits = %d after 21 demands, want exactly 1 (Ensure is tick-driven, not demand-driven)", got)
	}
	tk.tick(t)
	waitFor(t, "the second Ensure after one tick", func() bool { return eng.hits.Load() == 2 })
	if _, err := os.Stat(l.LeaseFile("harness")); err != nil {
		t.Errorf("a healthy Ensure must heartbeat the harness lease: %v", err)
	}
}

// TestDemandProbe_LeaseCadence_HeartbeatsWithoutFurtherDemand is the A5
// guarantee: while the engine is in use (a demand within IdleAfter) the
// lease is refreshed on EVERY tick even if no advisor calls arrive in
// between — Kenaz's 90s mtime rule can never see a gap longer than one
// MinInterval. After IdleAfter with no demand the heartbeat stops (the
// engine is allowed to self-terminate), and the next demand restarts it.
func TestDemandProbe_LeaseCadence_HeartbeatsWithoutFurtherDemand(t *testing.T) {
	eng := newCountingEngine(t)
	l, h := installedLayout(t)
	eng.setHealth(h)
	m := NewManager(l, NewClient(eng.srv.URL, nil), &fakeSpawner{}, "harness", "0.85.0")
	tk := newTickSleep()
	clock := &fakeClock{t: time.Unix(1_700_000_000, 0)}
	p := &DemandProbe{M: m, MinInterval: 30 * time.Second, IdleAfter: 5 * time.Minute, Now: clock.Now, Sleep: tk.Sleep}
	defer p.Close()

	p.Healthy() // the one and only demand for the next few assertions
	waitFor(t, "healthy", func() bool { return m.Healthy() })
	lease := l.LeaseFile("harness")

	staleAge := time.Now().Add(-200 * time.Second) // far past Kenaz's 90s rule
	for i := 0; i < 3; i++ {
		if err := os.Chtimes(lease, staleAge, staleAge); err != nil {
			t.Fatal(err)
		}
		before := eng.hits.Load()
		clock.Advance(30 * time.Second) // well inside IdleAfter; NO new demand
		tk.tick(t)
		waitFor(t, "a tick-driven Ensure", func() bool { return eng.hits.Load() > before })
		waitFor(t, "the lease mtime to be refreshed by the tick", func() bool {
			info, err := os.Stat(lease)
			return err == nil && time.Since(info.ModTime()) < 60*time.Second
		})
	}

	// Idle: no demand for longer than IdleAfter -> the next tick ends the
	// loop WITHOUT another Ensure, leaving the lease to lapse.
	hitsBefore := eng.hits.Load()
	clock.Advance(10 * time.Minute)
	tk.tick(t)
	p.Wait()
	if eng.hits.Load() != hitsBefore {
		t.Fatalf("heartbeat continued past IdleAfter: hits %d -> %d", hitsBefore, eng.hits.Load())
	}
	// A new demand restarts it.
	p.Healthy()
	waitFor(t, "the loop to restart on demand", func() bool { return eng.hits.Load() > hitsBefore })
}

// TestDemandProbe_UninstalledStopsHeartbeat: uninstalling ends the loop.
func TestDemandProbe_UninstalledStopsHeartbeat(t *testing.T) {
	eng := newCountingEngine(t)
	l, h := installedLayout(t)
	eng.setHealth(h)
	m := NewManager(l, NewClient(eng.srv.URL, nil), &fakeSpawner{}, "harness", "0.85.0")
	tk := newTickSleep()
	p := &DemandProbe{M: m, Sleep: tk.Sleep}
	defer p.Close()
	p.Healthy()
	waitFor(t, "healthy", func() bool { return m.Healthy() })
	if err := os.Remove(l.InstallJSONPath()); err != nil {
		t.Fatal(err)
	}
	hits := eng.hits.Load()
	tk.tick(t)
	p.Wait()
	if eng.hits.Load() != hits {
		t.Fatal("the loop kept dialing after the install record vanished")
	}
}

// TestLadder_RealManagerHealthyResolvesRung2 is the end-to-end ladder
// proof with a REAL Manager (adopt-verified path: exe_path under
// `current`, on-disk launcher re-hashed against the install record)
// against an engine double — not a stub probe: rung 2 resolves; with the
// engine down the same ladder falls through.
func TestLadder_RealManagerHealthyResolvesRung2(t *testing.T) {
	eng := newCountingEngine(t)
	l, h := installedLayout(t)
	eng.setHealth(h)
	m := NewManager(l, NewClient(eng.srv.URL, nil), &fakeSpawner{}, "harness", "0.85.0")
	p := &DemandProbe{M: m, MinInterval: 2 * time.Millisecond}
	defer p.Close()

	resolve := func() (string, advice.ModelRung, bool) {
		_, model, rung, _, ok := advice.ResolveAdvisorModel(advice.AdvisorModelSetting{}, nil, p)
		return model, rung, ok
	}
	var model string
	var rung advice.ModelRung
	var ok bool
	waitFor(t, "rung 2 to resolve", func() bool {
		model, rung, ok = resolve()
		return ok
	})
	if rung != advice.RungLocalLaya {
		t.Fatalf("ladder = (%q, %q, %v), want rung 2 (local laya) via a real healthy Manager", model, rung, ok)
	}
	if model != "kenaz-ml-sidecar@1.2.0" {
		t.Errorf("resolved model = %q, want the manager's engine identity", model)
	}

	// Engine stops answering: the next Ensure marks it unhealthy, ladder falls through.
	eng.srv.Close()
	waitFor(t, "the ladder to fall through with the engine down", func() bool {
		_, r, o := resolve()
		return !(o && r == advice.RungLocalLaya)
	})
}
