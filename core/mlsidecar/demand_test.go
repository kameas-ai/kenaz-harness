package mlsidecar

import (
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
// "nothing dials the port" and "at most one Ensure per interval" are
// asserted as call counts, not inferred.
type countingEngine struct {
	hits   atomic.Int64
	mu     sync.Mutex
	health HealthPayload
	srv    *httptest.Server
}

func newCountingEngine(t *testing.T) *countingEngine {
	t.Helper()
	e := &countingEngine{}
	e.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/health" {
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
		ContractVersions:  map[string]int{"api": SupportedContractMajor},
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

// TestDemandProbe_FirstDemandStartsEngine_LaterCallsHealthy_Throttled:
// the first demand falls back (the advisor's per-call heuristic), the
// background Ensure adopts the verified running engine, later demands see
// healthy, and Ensure runs at most once per MinInterval.
func TestDemandProbe_FirstDemandStartsEngine_LaterCallsHealthy_Throttled(t *testing.T) {
	eng := newCountingEngine(t)
	l, h := installedLayout(t)
	eng.setHealth(h)
	m := NewManager(l, NewClient(eng.srv.URL, nil), &fakeSpawner{}, "harness", "0.85.0")
	clock := &fakeClock{t: time.Unix(1_700_000_000, 0)}
	p := &DemandProbe{M: m, MinInterval: 30 * time.Second, Now: clock.Now}

	if p.Healthy() {
		t.Fatal("first demand must not block on / wait for the engine (per-call fallback)")
	}
	p.Wait()
	if got := m.Status().State; got != StateHealthy {
		t.Fatalf("after the background Ensure: state = %q (%s)", got, m.Status().Detail)
	}
	if !p.Healthy() {
		t.Fatal("second demand must see the engine healthy")
	}
	p.Wait()
	after := eng.hits.Load()
	if after != 1 {
		t.Fatalf("health hits = %d, want exactly 1 (throttled)", after)
	}
	for i := 0; i < 10; i++ {
		p.Healthy()
	}
	p.Wait()
	if eng.hits.Load() != 1 {
		t.Fatalf("demands inside MinInterval re-dialed: hits = %d", eng.hits.Load())
	}
	clock.Advance(31 * time.Second)
	p.Healthy()
	p.Wait()
	if eng.hits.Load() != 2 {
		t.Fatalf("after MinInterval one more Ensure was expected: hits = %d", eng.hits.Load())
	}
	if _, err := os.Stat(l.LeaseFile("harness")); err != nil {
		t.Errorf("a healthy Ensure must heartbeat the harness lease: %v", err)
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
	p := &DemandProbe{M: m, MinInterval: time.Nanosecond}

	resolve := func() (string, advice.ModelRung, bool) {
		_, model, rung, _, ok := advice.ResolveAdvisorModel(advice.AdvisorModelSetting{}, nil, p)
		return model, rung, ok
	}
	// Demand until the background Ensure lands (each Healthy() may kick one).
	deadline := time.Now().Add(5 * time.Second)
	var model string
	var rung advice.ModelRung
	var ok bool
	for time.Now().Before(deadline) {
		model, rung, ok = resolve()
		p.Wait()
		if ok {
			break
		}
		time.Sleep(time.Millisecond)
	}
	if !ok || rung != advice.RungLocalLaya {
		t.Fatalf("ladder = (%q, %q, %v), want rung 2 (local laya) via a real healthy Manager", model, rung, ok)
	}
	if model != "kenaz-ml-sidecar@1.2.0" {
		t.Errorf("resolved model = %q, want the manager's engine identity", model)
	}

	// Engine stops answering: next Ensure marks unhealthy, ladder falls through.
	eng.srv.Close()
	for i := 0; i < 50; i++ {
		p.Healthy()
		p.Wait()
		time.Sleep(time.Millisecond)
		if !m.Healthy() {
			break
		}
	}
	if _, rung2, ok2 := resolve(); ok2 && rung2 == advice.RungLocalLaya {
		t.Fatal("ladder still resolved rung 2 with the engine down")
	}
}
