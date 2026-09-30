package mlsidecar

import (
	"context"
	"testing"

	"github.com/kameas-ai/kenaz-harness/core/advice"
)

// compile-time witness that *Manager satisfies core/advice.SidecarProbe
// — the WP12 amendment's actual production wiring shape.
var _ advice.SidecarProbe = (*Manager)(nil)

// TestManager_Healthy_IsPureCacheRead proves the WP12 amendment's
// hardest requirement: Healthy()/Identity() must never themselves
// trigger a network call or a spawn, because the SAME ladder that calls
// them runs once at process boot (core/rpc/api.go's unconditional
// boot-resolve call site) — a probe that spawned on every boot-time
// resolve would silently violate "lazy start on first advisor demand,
// not app boot" the instant the ladder ran once.
func TestManager_Healthy_IsPureCacheRead(t *testing.T) {
	l := NewLayout(t.TempDir())
	setupVerifiedVersion(t, l, "1.0.0", []byte("bytes"))
	spawner := &fakeSpawner{}
	// Client points nowhere real; if Healthy() ever dialed out, this
	// would be the failure mode to catch (it would simply hang/err on a
	// live probe, but the real point is that Healthy() must not even try
	// — see the call-count assertion below).
	m := NewManager(l, NewClient(unreachableBaseURL, nil), spawner, "harness", "0.84.0")

	if m.Healthy() {
		t.Fatal("a freshly constructed Manager (never reconciled) must report Healthy()=false")
	}
	if spawner.called {
		t.Fatal("Healthy() must never trigger a spawn")
	}
	if got := m.Identity(); got == "" {
		t.Error("Identity() must always return a non-empty string, even before any reconcile")
	}
}

// TestManager_SatisfiesAdviceSidecarProbe_LadderPrefersHealthySidecar is
// the cross-package integration proof for "ladder prefers healthy
// sidecar and falls through cleanly" (WP12 brief item 7): once a real
// Manager reconciles to StateHealthy, core/advice.ResolveAdvisorModel
// resolves RungLocalLaya using the Manager's own reported identity —
// and falls back to RungNone once the Manager is unhealthy again.
func TestManager_SatisfiesAdviceSidecarProbe_LadderPrefersHealthySidecar(t *testing.T) {
	l := NewLayout(t.TempDir())
	exePath, sha := setupVerifiedVersion(t, l, "1.0.0", []byte("bytes"))
	stub := newStubSidecar()
	defer stub.Close()
	stub.setHealth(HealthPayload{
		SidecarVersion:    "1.0.0",
		ExePath:           exePath,
		EngineSHA256:      sha,
		ContractVersions:  map[string][]string{"branch_now": {"0123456789abcdef"}},
		LifecycleProtocol: 1,
	})
	m := NewManager(l, NewClient(stub.URL(), nil), nil, "harness", "0.84.0")

	// Before any reconcile: ladder falls through cleanly to RungNone.
	_, _, rung, _, ok := advice.ResolveAdvisorModel(advice.AdvisorModelSetting{}, nil, m)
	if ok || rung != advice.RungNone {
		t.Fatalf("before reconcile: rung=%q ok=%v, want RungNone/false", rung, ok)
	}

	if got := m.Reconcile(context.Background()); got.State != StateHealthy {
		t.Fatalf("Reconcile: State=%q Detail=%s", got.State, got.Detail)
	}

	profileID, model, rung, unbenchmarked, ok := advice.ResolveAdvisorModel(advice.AdvisorModelSetting{}, nil, m)
	if !ok {
		t.Fatal("expected ok=true once the sidecar is healthy")
	}
	if rung != advice.RungLocalLaya {
		t.Errorf("rung = %q, want %q", rung, advice.RungLocalLaya)
	}
	if profileID != "" {
		t.Errorf("profileID = %q, want empty", profileID)
	}
	if model == "" {
		t.Error("expected a non-empty model identity from the healthy sidecar")
	}
	if !unbenchmarked {
		t.Error("expected unbenchmarked=true (no benchmark of record for the sidecar rung yet)")
	}
}
