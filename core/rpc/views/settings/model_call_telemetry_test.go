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

	// Consent evidence: the setter stamps the current text revision + time.
	stored, _ := a.Get(ctx)
	if stored.ShareModelCallTelemetryTextRevision != ModelCallOptInTextRevision || stored.ShareModelCallTelemetryAcceptedAt == "" {
		t.Fatalf("opt-in evidence = rev %d at %q, want rev %d and a time",
			stored.ShareModelCallTelemetryTextRevision, stored.ShareModelCallTelemetryAcceptedAt, ModelCallOptInTextRevision)
	}

	// Boot: a persisted ON reaches a freshly wired pipeline.
	store2, _ := NewFileStore(dir)
	b := NewAPI(store2)
	p2 := fleet.NewFleetOTLPPipeline(nil)
	b.SetFleetOTLPPipeline(p2, nil, nil, nil)
	if !p2.ModelCallSharing() {
		t.Fatal("the persisted opt-in did not reach a pipeline wired at boot")
	}

	// Off through the panel action.
	mc.ShareWithFleet = false
	if err := a.SetModelCallTelemetry(ctx, mc); err != nil {
		t.Fatal(err)
	}
	if p.ModelCallSharing() {
		t.Fatal("SetModelCallTelemetry(off) did not reach the pipeline")
	}

	s := stored // a STALE copy loaded while sharing was on
	s.ModelCallRetentionDays = 4000
	if err := a.Set(ctx, s); err == nil {
		t.Fatal("retention above the maximum must be refused")
	}
}

// #422 review finding 4: a whole-settings Set is not a consent action. A
// stale settings object (loaded while sharing was on) saved after the user
// turned sharing off in the panel must NOT turn it back on, and a Set can
// never turn it on at all.
func TestSettings_WholeSettingsSetCannotChangeModelCallSharing(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	store, err := NewFileStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	a := NewAPI(store)
	p := fleet.NewFleetOTLPPipeline(nil)
	a.SetFleetOTLPPipeline(p, nil, nil, nil)

	mc, _ := a.GetModelCallTelemetry(ctx)
	mc.ShareWithFleet = true
	if err := a.SetModelCallTelemetry(ctx, mc); err != nil {
		t.Fatal(err)
	}
	stale, _ := a.Get(ctx) // e.g. SettingsView's object, loaded once
	mc.ShareWithFleet = false
	if err := a.SetModelCallTelemetry(ctx, mc); err != nil {
		t.Fatal(err)
	}
	// The user changes an unrelated setting; the view saves its stale copy.
	stale.WebSearchEnabled = !stale.WebSearchEnabled
	if err := a.Set(ctx, stale); err != nil {
		t.Fatal(err)
	}
	if p.ModelCallSharing() {
		t.Fatal("a stale whole-settings save turned model-call sharing back on")
	}
	if got, _ := a.GetModelCallTelemetry(ctx); got.ShareWithFleet {
		t.Fatal("stored opt-in flipped back on by a whole-settings save")
	}

	// A Set that sets the flag directly cannot turn sharing on either.
	s, _ := a.Get(ctx)
	s.ShareModelCallTelemetryWithFleet = true
	s.ShareModelCallTelemetryTextRevision = ModelCallOptInTextRevision
	if err := a.Set(ctx, s); err != nil {
		t.Fatal(err)
	}
	if p.ModelCallSharing() {
		t.Fatal("a whole-settings Set turned model-call sharing on")
	}
}

// #422 review finding 3: an opt-in given under an older text revision does
// not take effect; the user must agree to the current wording.
func TestSettings_ModelCallSharingRequiresCurrentTextRevision(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	dir := t.TempDir()
	store, err := NewFileStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	// Persist an opt-in from before revisions existed (revision 0).
	old := Settings{ShareModelCallTelemetryWithFleet: true}
	if err := store.SaveAll(old); err != nil {
		t.Fatal(err)
	}
	a := NewAPI(store)
	p := fleet.NewFleetOTLPPipeline(nil)
	a.SetFleetOTLPPipeline(p, nil, nil, nil)
	if p.ModelCallSharing() {
		t.Fatal("an opt-in under an older text revision took effect")
	}
	if mc, _ := a.GetModelCallTelemetry(ctx); mc.ShareWithFleet {
		t.Fatal("the panel shows sharing on for an outdated opt-in")
	}
}
