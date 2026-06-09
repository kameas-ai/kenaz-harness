package dials

import (
	"context"
	"errors"
	"testing"
	"time"

	coredials "github.com/kameas-ai/kenaz-harness/core/agentgraph/dials"
)

func TestSetGet_Roundtrip(t *testing.T) {
	t.Parallel()
	api := New(Config{})
	ctx := context.Background()
	cfg := DialConfig{
		MaxTokensPerRun:    50_000,
		MaxTokensPerRunSet: true,
	}
	key := ScopeKey{Scope: string(coredials.ScopeProject), ID: "p1"}
	if err := api.SetDials(ctx, key, cfg); err != nil {
		t.Fatalf("SetDials: %v", err)
	}
	got, err := api.GetDials(ctx, key)
	if err != nil {
		t.Fatalf("GetDials: %v", err)
	}
	if got.MaxTokensPerRun != 50_000 || !got.MaxTokensPerRunSet {
		t.Fatalf("roundtrip lost values: %+v", got)
	}
	if got.UpdatedAt.IsZero() {
		t.Fatalf("UpdatedAt not stamped")
	}
}

func TestSetDials_PersisterCalled_NotForRun(t *testing.T) {
	t.Parallel()
	called := 0
	api := New(Config{Persister: func(_ ScopeKey, _ DialConfig) error {
		called++
		return nil
	}})
	ctx := context.Background()
	_ = api.SetDials(ctx, ScopeKey{Scope: string(coredials.ScopeProject), ID: "p1"}, DialConfig{})
	_ = api.SetDials(ctx, ScopeKey{Scope: string(coredials.ScopePerRun), ID: "r1"}, DialConfig{})
	if called != 1 {
		t.Fatalf("Persister called %d times, want 1 (run scope is ephemeral)", called)
	}
}

func TestGetEffective_Cascades(t *testing.T) {
	t.Parallel()
	api := New(Config{})
	ctx := context.Background()
	_ = api.SetDials(ctx, ScopeKey{Scope: string(coredials.ScopeGlobal)}, DialConfig{
		MaxTokensPerRun: 100_000, MaxTokensPerRunSet: true,
	})
	_ = api.SetDials(ctx, ScopeKey{Scope: string(coredials.ScopeProject), ID: "p1"}, DialConfig{
		MaxTokensPerRun: 50_000, MaxTokensPerRunSet: true,
	})
	_ = api.SetDials(ctx, ScopeKey{Scope: string(coredials.ScopeSession), ID: "s1"}, DialConfig{
		MaxCostUSD: 1.5, MaxCostUSDSet: true,
	})
	eff, err := api.GetEffective(ctx, "p1", "s1", "", "")
	if err != nil {
		t.Fatalf("GetEffective: %v", err)
	}
	if eff.MaxTokensPerRun.Value != 50_000 || eff.MaxTokensPerRun.From != "project" {
		t.Fatalf("token cascade wrong: %+v", eff.MaxTokensPerRun)
	}
	if eff.MaxCostUSD.Value != 1.5 || eff.MaxCostUSD.From != "session" {
		t.Fatalf("cost cascade wrong: %+v", eff.MaxCostUSD)
	}
}

func TestBumpAndResume_NoResumer_ErrNoPause(t *testing.T) {
	t.Parallel()
	api := New(Config{})
	err := api.BumpAndResume(context.Background(), "r1", DialDelta{AddTokensPerRun: 10_000})
	if !errors.Is(err, ErrNoPause) {
		t.Fatalf("err = %v, want ErrNoPause", err)
	}
	// But the run-scoped layer should still hold the bump.
	cfg, _ := api.GetDials(context.Background(), ScopeKey{
		Scope: string(coredials.ScopePerRun), ID: "r1",
	})
	if cfg.MaxTokensPerRun != 10_000 {
		t.Fatalf("bump not stored: %+v", cfg)
	}
}

type stubResumer struct {
	called string
}

func (s *stubResumer) Resume(_ context.Context, runID string) error {
	s.called = runID
	return nil
}

func TestBumpAndResume_ResumerNotified(t *testing.T) {
	t.Parallel()
	res := &stubResumer{}
	api := New(Config{Resumer: res})
	if err := api.BumpAndResume(context.Background(), "r1", DialDelta{AddTokensPerRun: 1000}); err != nil {
		t.Fatalf("BumpAndResume: %v", err)
	}
	if res.called != "r1" {
		t.Fatalf("Resumer called for %q, want r1", res.called)
	}
}

func TestApplyDelta_CompoundsExisting(t *testing.T) {
	t.Parallel()
	cfg := DialConfig{MaxTokensPerRun: 5000, MaxTokensPerRunSet: true}
	out := applyDelta(cfg, DialDelta{AddTokensPerRun: 3000})
	if out.MaxTokensPerRun != 8000 {
		t.Fatalf("compound = %d, want 8000", out.MaxTokensPerRun)
	}
}

func TestToViewEffective_DurationsSerialiseAsSeconds(t *testing.T) {
	t.Parallel()
	eff := coredials.EffectiveDials{
		MaxWallclock:        90 * time.Second,
		MaxWallclockFrom:    coredials.ScopeProject,
		MemoryPruneInterval: 6 * time.Hour,
	}
	out := toViewEffective(eff)
	if out.MaxWallclockSeconds.Value != 90 || out.MaxWallclockSeconds.From != "project" {
		t.Fatalf("wallclock: %+v", out.MaxWallclockSeconds)
	}
	if out.MemoryPruneIntervalSeconds.Value != 6*60*60 {
		t.Fatalf("prune interval: %+v", out.MemoryPruneIntervalSeconds)
	}
}
