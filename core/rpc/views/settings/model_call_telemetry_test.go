package settings

import (
	"context"
	"testing"

	"github.com/kameas-ai/kenaz-harness/core/fleet"
)

// TestSettings_ModelCallSharingReachesPipeline: "Share model-call
// telemetry with Fleet" reaches the live span lane through every writer
// — the dedicated setter, a whole-settings Set, and the persisted value
// when the pipeline is wired at boot — and defaults OFF. Settings persist
// through the real settings.json FileStore, not the memory store.
func TestSettings_ModelCallSharingReachesPipeline(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	dir := t.TempDir()
	store, err := NewFileStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	a := NewAPI(store)
	p := fleet.NewFleetOTLPPipeline(nil)
	a.SetFleetOTLPPipeline(p, nil, nil, nil)
	if p.ModelCallSharing() {
		t.Fatal("sharing must default OFF")
	}
	mc, err := a.GetModelCallTelemetry(ctx)
	if err != nil || !mc.StatsPullEnabled || mc.ShareWithFleet || mc.RetentionDays != 0 || mc.EffectiveRetentionDays != 30 {
		t.Fatalf("defaults = %+v, %v; want pull on, share off, 0 → 30 days", mc, err)
	}

	mc.ShareWithFleet = true
	if err := a.SetModelCallTelemetry(ctx, mc); err != nil {
		t.Fatal(err)
	}
	if !p.ModelCallSharing() {
		t.Fatal("SetModelCallTelemetry(ShareWithFleet) did not reach the pipeline")
	}

	s, _ := a.Get(ctx)
	s.ShareModelCallTelemetryWithFleet = false
	if err := a.Set(ctx, s); err != nil {
		t.Fatal(err)
	}
	if p.ModelCallSharing() {
		t.Fatal("a whole-settings Set turning sharing off did not reach the pipeline")
	}

	// Boot: a persisted ON reaches a freshly wired pipeline.
	s.ShareModelCallTelemetryWithFleet = true
	if err := a.Set(ctx, s); err != nil {
		t.Fatal(err)
	}
	store2, _ := NewFileStore(dir)
	b := NewAPI(store2)
	p2 := fleet.NewFleetOTLPPipeline(nil)
	b.SetFleetOTLPPipeline(p2, nil, nil, nil)
	if !p2.ModelCallSharing() {
		t.Fatal("the persisted opt-in did not reach a pipeline wired at boot")
	}

	s.ModelCallRetentionDays = 4000
	if err := a.Set(ctx, s); err == nil {
		t.Fatal("retention above the maximum must be refused")
	}
}
