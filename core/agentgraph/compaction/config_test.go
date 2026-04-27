package compaction_test

import (
	"testing"

	"github.com/sigil-tech/kaneaz-harness/core/agentgraph/compaction"
)

func TestMemoryResolver_DefaultsApplied(t *testing.T) {
	r := compaction.NewMemoryResolver()
	cfg := r.Resolve(compaction.ScopeKey{})
	pre := cfg.ForSite(compaction.SitePreCall)
	if pre.PreCallThreshold != 0.85 {
		t.Fatalf("expected default pre_call_threshold 0.85, got %v", pre.PreCallThreshold)
	}
	post := cfg.ForSite(compaction.SitePostTool)
	if post.ToolResultMaxBytes != 16*1024 {
		t.Fatalf("expected default tool_result_max_bytes 16384, got %v", post.ToolResultMaxBytes)
	}
}

func TestMemoryResolver_LayerPrecedence(t *testing.T) {
	r := compaction.NewMemoryResolver()
	// Set distinct strategies at every layer for the pre_call site.
	for layer, strat := range map[compaction.Layer]compaction.Strategy{
		compaction.LayerProject: compaction.StrategyDropOldest,
		compaction.LayerSession: compaction.StrategySemanticCluster,
		compaction.LayerRun:     compaction.StrategySummary,
		compaction.LayerNode:    compaction.StrategyCustomSubgraph,
	} {
		sc := compaction.SiteConfig{Strategy: strat}
		sc.MarkStrategy()
		cfg := compaction.CompactionConfig{Sites: map[compaction.Site]compaction.SiteConfig{
			compaction.SitePreCall: sc,
		}}
		var scopeID string
		switch layer {
		case compaction.LayerProject:
			scopeID = "p1"
		case compaction.LayerSession:
			scopeID = "s1"
		case compaction.LayerRun:
			scopeID = "r1"
		case compaction.LayerNode:
			scopeID = "r1/n1"
		}
		r.Set(layer, scopeID, cfg)
	}

	tests := []struct {
		name string
		key  compaction.ScopeKey
		want compaction.Strategy
	}{
		{"only-project",
			compaction.ScopeKey{ProjectID: "p1"},
			compaction.StrategyDropOldest,
		},
		{"project+session",
			compaction.ScopeKey{ProjectID: "p1", SessionID: "s1"},
			compaction.StrategySemanticCluster,
		},
		{"project+session+run",
			compaction.ScopeKey{ProjectID: "p1", SessionID: "s1", RunID: "r1"},
			compaction.StrategySummary,
		},
		{"all-layers-with-node",
			compaction.ScopeKey{ProjectID: "p1", SessionID: "s1", RunID: "r1", NodeID: "n1"},
			compaction.StrategyCustomSubgraph,
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := r.Resolve(tc.key).ForSite(compaction.SitePreCall).Strategy
			if got != tc.want {
				t.Fatalf("strategy = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestMemoryResolver_AttributionTracksContributingLayer(t *testing.T) {
	r := compaction.NewMemoryResolver()
	// Project sets pre_call threshold; session overrides strategy.
	pc := compaction.SiteConfig{PreCallThreshold: 0.7}
	pc.MarkPreCallThreshold()
	r.Set(compaction.LayerProject, "p1", compaction.CompactionConfig{
		Sites: map[compaction.Site]compaction.SiteConfig{compaction.SitePreCall: pc},
	})
	sc := compaction.SiteConfig{Strategy: compaction.StrategySummary}
	sc.MarkStrategy()
	r.Set(compaction.LayerSession, "s1", compaction.CompactionConfig{
		Sites: map[compaction.Site]compaction.SiteConfig{compaction.SitePreCall: sc},
	})
	attr := r.Attribution(compaction.ScopeKey{ProjectID: "p1", SessionID: "s1"})
	if attr[compaction.SitePreCall]["pre_call_threshold"] != compaction.LayerProject {
		t.Fatalf("expected project attribution for threshold, got %v",
			attr[compaction.SitePreCall]["pre_call_threshold"])
	}
	if attr[compaction.SitePreCall]["strategy"] != compaction.LayerSession {
		t.Fatalf("expected session attribution for strategy, got %v",
			attr[compaction.SitePreCall]["strategy"])
	}
}

func TestMemoryResolver_GetSetRoundTrip(t *testing.T) {
	r := compaction.NewMemoryResolver()
	sc := compaction.SiteConfig{Strategy: compaction.StrategyDropOldest}
	sc.MarkStrategy()
	cfg := compaction.CompactionConfig{Sites: map[compaction.Site]compaction.SiteConfig{
		compaction.SitePreCall: sc,
	}}
	r.Set(compaction.LayerProject, "p1", cfg)
	got := r.Get(compaction.LayerProject, "p1")
	if got.ForSite(compaction.SitePreCall).Strategy != compaction.StrategyDropOldest {
		t.Fatalf("round-trip lost strategy: %+v", got)
	}
}

func TestSafeDefaults_AllSitesPopulated(t *testing.T) {
	d := compaction.SafeDefaults()
	for _, s := range compaction.AllSites() {
		sc := d.ForSite(s)
		if sc.Strategy == "" {
			t.Fatalf("site %q has empty strategy", s)
		}
		if !sc.Enabled {
			t.Fatalf("site %q is disabled by default", s)
		}
	}
}
