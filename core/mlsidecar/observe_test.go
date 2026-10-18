package mlsidecar

import (
	"context"
	"os"
	"testing"
	"time"
)

// TestObserve_NeverSpawnsNeverLeases: a settings-panel read is not advisor
// demand. Observe reports what is on the port but must not start an
// engine, and must not heartbeat a lease (that would pin the engine alive
// for as long as a panel is open).
func TestObserve_NeverSpawnsNeverLeases(t *testing.T) {
	eng := newCountingEngine(t)
	l, h := installedLayout(t)
	eng.setHealth(h)
	spawner := &fakeSpawner{}
	m := NewManager(l, NewClient(eng.srv.URL, nil), spawner, "harness", "0.85.0")

	st, running := m.Observe(context.Background())
	if !running || st.State != StateHealthy {
		t.Fatalf("Observe = %+v running=%v, want a verified healthy engine", st, running)
	}
	if spawner.called {
		t.Fatal("Observe spawned an engine")
	}
	if _, err := os.Stat(l.LeaseFile("harness")); !os.IsNotExist(err) {
		t.Fatalf("Observe wrote a lease (%v) — a read must not pin the engine alive", err)
	}
}

func TestObserve_EngineGone_DemotesStaleHealthyWithoutCallingItAFault(t *testing.T) {
	eng := newCountingEngine(t)
	l, h := installedLayout(t)
	eng.setHealth(h)
	m := NewManager(l, NewClient(eng.srv.URL, nil), &fakeSpawner{}, "harness", "0.85.0")
	if st := m.Reconcile(context.Background()); st.State != StateHealthy {
		t.Fatalf("setup: %+v", st)
	}
	eng.srv.Close() // the engine self-terminated when idle

	st, running := m.Observe(context.Background())
	if running {
		t.Fatal("running must be false once the port stops answering")
	}
	if st.State == StateHealthy || st.Reason != ReasonNone {
		t.Fatalf("status = %+v: a stale healthy claim must be demoted, with NO failure reason (idle is not a fault)", st)
	}
	if m.Healthy() {
		t.Fatal("the advisor ladder must not resolve rung 2 against a stopped engine")
	}
}

func TestObserve_LegacyEngineWithNothingInstalled_SurfacesLegacyUnverified(t *testing.T) {
	eng := newCountingEngine(t)
	eng.setHealth(HealthPayload{Product: "kameas-ml", SidecarVersion: "0.9.0"}) // pre-lease
	m := NewManager(NewLayout(t.TempDir()), NewClient(eng.srv.URL, nil), nil, "harness", "0.85.0")
	st, running := m.Observe(context.Background())
	if !running || st.State != StateLegacyUnverified || st.Reason != ReasonLegacyEngine {
		t.Fatalf("Observe = %+v running=%v", st, running)
	}
}

func TestObserve_InstallInFlight_ReturnsInstallingUntouched(t *testing.T) {
	m := NewManager(NewLayout(t.TempDir()), NewClient(unreachableBaseURL, nil), nil, "harness", "0.85.0")
	m.setStatus(Status{State: StateInstalling, Detail: "verifying 1.2.0", UpdatedAt: time.Now()})
	st, _ := m.Observe(context.Background())
	if st.State != StateInstalling || st.Detail != "verifying 1.2.0" {
		t.Fatalf("Observe clobbered the in-flight install status: %+v", st)
	}
}
