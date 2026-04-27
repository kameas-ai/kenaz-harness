package dials

import (
	"testing"
	"time"
)

func TestResolve_NoLayers_ReturnsDefaults(t *testing.T) {
	t.Parallel()
	eff := Resolve(nil)
	want := Defaults()
	if eff.MaxTokensPerRun != want.MaxTokensPerRun {
		t.Fatalf("MaxTokensPerRun = %d, want default %d", eff.MaxTokensPerRun, want.MaxTokensPerRun)
	}
	if eff.MaxTokensPerRunFrom != ScopeGlobal {
		t.Fatalf("MaxTokensPerRunFrom = %s, want global", eff.MaxTokensPerRunFrom)
	}
}

func TestResolve_HigherLayerOverrides(t *testing.T) {
	t.Parallel()
	layers := []Layer{
		{Scope: ScopeGlobal, Config: DialConfig{MaxTokensPerRun: IntPtr(100_000)}},
		{Scope: ScopeProject, Config: DialConfig{MaxTokensPerRun: IntPtr(50_000)}},
		{Scope: ScopeSession, Config: DialConfig{MaxTokensPerRun: IntPtr(25_000)}},
	}
	eff := Resolve(layers)
	if eff.MaxTokensPerRun != 25_000 {
		t.Fatalf("MaxTokensPerRun = %d, want 25000 (session override)", eff.MaxTokensPerRun)
	}
	if eff.MaxTokensPerRunFrom != ScopeSession {
		t.Fatalf("attribution = %s, want session", eff.MaxTokensPerRunFrom)
	}
}

func TestResolve_FieldByFieldCascade(t *testing.T) {
	t.Parallel()
	// Project sets tokens; session sets cost; per-run sets wallclock.
	// Resolve should pick each field from the layer that set it.
	layers := []Layer{
		{Scope: ScopeGlobal, Config: DialConfig{MaxTokensPerRun: IntPtr(100_000), MaxCostUSD: Float64Ptr(2.0)}},
		{Scope: ScopeProject, Config: DialConfig{MaxTokensPerRun: IntPtr(50_000)}},
		{Scope: ScopeSession, Config: DialConfig{MaxCostUSD: Float64Ptr(1.0)}},
		{Scope: ScopePerRun, Config: DialConfig{MaxWallclock: DurPtr(2 * time.Minute)}},
	}
	eff := Resolve(layers)
	if eff.MaxTokensPerRun != 50_000 || eff.MaxTokensPerRunFrom != ScopeProject {
		t.Fatalf("tokens = %d from %s, want 50000 from project", eff.MaxTokensPerRun, eff.MaxTokensPerRunFrom)
	}
	if eff.MaxCostUSD != 1.0 || eff.MaxCostUSDFrom != ScopeSession {
		t.Fatalf("cost = %v from %s, want 1.0 from session", eff.MaxCostUSD, eff.MaxCostUSDFrom)
	}
	if eff.MaxWallclock != 2*time.Minute || eff.MaxWallclockFrom != ScopePerRun {
		t.Fatalf("wallclock = %v from %s, want 2m from run", eff.MaxWallclock, eff.MaxWallclockFrom)
	}
	// Defaulted fields keep global attribution.
	if eff.PlanVerbosityFrom != ScopeGlobal {
		t.Fatalf("PlanVerbosityFrom = %s, want global default", eff.PlanVerbosityFrom)
	}
}

func TestResolve_PointerSentinelDistinguishesUnset(t *testing.T) {
	t.Parallel()
	// Explicit zero must override the default 5.0 with 0.
	layers := []Layer{
		{Scope: ScopeGlobal, Config: DialConfig{}},
		{Scope: ScopeSession, Config: DialConfig{MaxCostUSD: Float64Ptr(0)}},
	}
	eff := Resolve(layers)
	if eff.MaxCostUSD != 0 || eff.MaxCostUSDFrom != ScopeSession {
		t.Fatalf("explicit zero override lost: %v from %s", eff.MaxCostUSD, eff.MaxCostUSDFrom)
	}
}

func TestBump_AppliesDelta(t *testing.T) {
	t.Parallel()
	cfg := DialConfig{
		MaxTokensPerRun: IntPtr(10_000),
		MaxCostUSD:      Float64Ptr(1.0),
	}
	cfg.Bump(DialDelta{AddTokensPerRun: 5_000, AddCostUSD: 0.5})
	if *cfg.MaxTokensPerRun != 15_000 {
		t.Fatalf("tokens = %d, want 15000", *cfg.MaxTokensPerRun)
	}
	if *cfg.MaxCostUSD != 1.5 {
		t.Fatalf("cost = %v, want 1.5", *cfg.MaxCostUSD)
	}
}

func TestBump_NilFieldStaysUnset(t *testing.T) {
	t.Parallel()
	cfg := DialConfig{}
	cfg.Bump(DialDelta{AddTokensPerRun: 5000})
	if cfg.MaxTokensPerRun != nil {
		t.Fatalf("Bump on nil field invented a value: %v", *cfg.MaxTokensPerRun)
	}
}

func TestDefaultLayerOrder_MatchesSpec(t *testing.T) {
	t.Parallel()
	got := DefaultLayerOrder()
	want := []Scope{ScopeGlobal, ScopeProject, ScopeSession, ScopePerGraph, ScopePerRun}
	if len(got) != len(want) {
		t.Fatalf("layer count = %d, want %d", len(got), len(want))
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("layer[%d] = %s, want %s", i, got[i], want[i])
		}
	}
}
