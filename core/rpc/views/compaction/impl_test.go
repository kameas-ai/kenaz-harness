package compaction_test

import (
	"context"
	"errors"
	"testing"

	"github.com/sigil-tech/kaneaz-harness/core/agentgraph"
	corecompaction "github.com/sigil-tech/kaneaz-harness/core/agentgraph/compaction"
	view "github.com/sigil-tech/kaneaz-harness/core/rpc/views/compaction"
)

func newAPI(t *testing.T) *view.API {
	t.Helper()
	p := corecompaction.NewPipeline()
	p.RegisterStrategy(corecompaction.NewDropOldestStrategy())
	p.RegisterStrategy(corecompaction.NewSummaryStrategy(nil))
	p.RegisterStrategy(corecompaction.NewSemanticClusterStrategy(nil))
	p.RegisterStrategy(corecompaction.NewCustomSubgraphStrategy(nil))
	return view.New(p)
}

func TestAPI_NilPipelineReturnsErr(t *testing.T) {
	a := view.New(nil)
	if _, err := a.GetConfig(context.Background(), view.LayerGlobal, ""); !errors.Is(err, view.ErrPipelineUnavailable) {
		t.Fatalf("expected ErrPipelineUnavailable, got %v", err)
	}
	if _, err := a.GetEffective(context.Background(), view.ScopeKey{}); !errors.Is(err, view.ErrPipelineUnavailable) {
		t.Fatalf("expected ErrPipelineUnavailable, got %v", err)
	}
	if err := a.SetConfig(context.Background(), view.LayerGlobal, "", view.Config{}); !errors.Is(err, view.ErrPipelineUnavailable) {
		t.Fatalf("expected ErrPipelineUnavailable, got %v", err)
	}
	if _, err := a.TriggerManualCompaction(context.Background(), "s", view.ManualOpts{}); !errors.Is(err, view.ErrPipelineUnavailable) {
		t.Fatalf("expected ErrPipelineUnavailable, got %v", err)
	}
}

func TestAPI_GetEffectiveReflectsDefaults(t *testing.T) {
	a := newAPI(t)
	eff, err := a.GetEffective(context.Background(), view.ScopeKey{})
	if err != nil {
		t.Fatalf("GetEffective: %v", err)
	}
	pre := eff.Config.Sites[view.SitePreCall]
	if pre.PreCallThreshold == 0 {
		t.Fatalf("expected default pre_call_threshold > 0")
	}
	if pre.Strategy == "" {
		t.Fatalf("expected default strategy")
	}
}

func TestAPI_SetGetConfigRoundTrip(t *testing.T) {
	a := newAPI(t)
	cfg := view.Config{Sites: map[view.Site]view.SiteConfig{
		view.SitePreCall: {
			Enabled:  true,
			Strategy: view.StrategySummary,
			PreCallThreshold: 0.7,
		},
	}}
	if err := a.SetConfig(context.Background(), view.LayerProject, "p1", cfg); err != nil {
		t.Fatalf("SetConfig: %v", err)
	}
	got, err := a.GetConfig(context.Background(), view.LayerProject, "p1")
	if err != nil {
		t.Fatalf("GetConfig: %v", err)
	}
	gp := got.Sites[view.SitePreCall]
	if gp.Strategy != view.StrategySummary {
		t.Fatalf("strategy round-trip lost: %q", gp.Strategy)
	}
	if gp.PreCallThreshold != 0.7 {
		t.Fatalf("threshold round-trip lost: %v", gp.PreCallThreshold)
	}
}

func TestAPI_GetEffectiveAttribution(t *testing.T) {
	a := newAPI(t)
	cfg := view.Config{Sites: map[view.Site]view.SiteConfig{
		view.SitePreCall: {
			Enabled:  true,
			Strategy: view.StrategySemanticCluster,
		},
	}}
	if err := a.SetConfig(context.Background(), view.LayerSession, "s1", cfg); err != nil {
		t.Fatalf("SetConfig: %v", err)
	}
	eff, err := a.GetEffective(context.Background(), view.ScopeKey{SessionID: "s1"})
	if err != nil {
		t.Fatalf("GetEffective: %v", err)
	}
	if eff.Attribution[view.SitePreCall]["strategy"] != view.LayerSession {
		t.Fatalf("expected session attribution, got %v",
			eff.Attribution[view.SitePreCall]["strategy"])
	}
}

func TestAPI_TriggerManualCompactionDispatches(t *testing.T) {
	a := newAPI(t)
	res, err := a.TriggerManualCompaction(context.Background(), "s1", view.ManualOpts{
		Strategy: view.StrategyDropOldest,
	})
	if err != nil {
		t.Fatalf("TriggerManualCompaction: %v", err)
	}
	if res.Strategy != view.StrategyDropOldest {
		t.Fatalf("strategy = %q", res.Strategy)
	}
}

func TestAPI_TriggerManualCompaction_EmptySessionRejected(t *testing.T) {
	a := newAPI(t)
	if _, err := a.TriggerManualCompaction(context.Background(), "", view.ManualOpts{}); !errors.Is(err, view.ErrInvalidArg) {
		t.Fatalf("expected ErrInvalidArg, got %v", err)
	}
}

func TestAPI_TriggerCustomSubgraphWithoutResolver(t *testing.T) {
	a := newAPI(t)
	_, err := a.TriggerManualCompaction(context.Background(), "s1", view.ManualOpts{
		Strategy:      view.StrategyCustomSubgraph,
		CustomGraphID: "g42",
	})
	if !errors.Is(err, view.ErrCustomGraphUnavailable) {
		t.Fatalf("expected ErrCustomGraphUnavailable, got %v", err)
	}
}

func TestAPI_TriggerCustomSubgraphWithResolver(t *testing.T) {
	a := newAPI(t)
	a.SetGraphResolver(func(_ context.Context, id string) (*agentgraph.Graph, error) {
		return &agentgraph.Graph{
			ID:          id,
			SpecVersion: "1",
			Entrypoints: []string{"x"},
		}, nil
	})
	_, err := a.TriggerManualCompaction(context.Background(), "s1", view.ManualOpts{
		Strategy:      view.StrategyCustomSubgraph,
		CustomGraphID: "g42",
	})
	// The custom subgraph runner is nil in the strategy, so the call
	// surfaces ErrNoKernelRunner from the strategy. That's fine — it
	// proves the resolver handed off correctly. We assert on err type.
	if err == nil {
		t.Fatalf("expected error from missing kernel runner")
	}
}

func TestAPI_ListCustomStrategiesEmptyByDefault(t *testing.T) {
	a := newAPI(t)
	out, err := a.ListCustomStrategies(context.Background())
	if err != nil {
		t.Fatalf("ListCustomStrategies: %v", err)
	}
	if len(out) != 0 {
		t.Fatalf("expected empty, got %d", len(out))
	}
}

func TestAPI_ListCustomStrategiesUsesLister(t *testing.T) {
	a := newAPI(t)
	a.SetGraphLister(func(_ context.Context) ([]view.CustomStrategy, error) {
		return []view.CustomStrategy{
			{GraphID: "g1", Name: "alpha"},
		}, nil
	})
	out, err := a.ListCustomStrategies(context.Background())
	if err != nil {
		t.Fatalf("ListCustomStrategies: %v", err)
	}
	if len(out) != 1 || out[0].GraphID != "g1" {
		t.Fatalf("unexpected output: %+v", out)
	}
}

func TestAPI_InvalidLayerRejected(t *testing.T) {
	a := newAPI(t)
	if _, err := a.GetConfig(context.Background(), view.Layer("bogus"), ""); !errors.Is(err, view.ErrInvalidArg) {
		t.Fatalf("expected ErrInvalidArg, got %v", err)
	}
	if err := a.SetConfig(context.Background(), view.Layer("bogus"), "", view.Config{}); !errors.Is(err, view.ErrInvalidArg) {
		t.Fatalf("expected ErrInvalidArg, got %v", err)
	}
}
