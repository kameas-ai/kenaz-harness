package agentgraph_test

import (
	"context"
	"strings"
	"sync"
	"testing"

	"github.com/kameas-ai/kenaz-harness/core/agentgraph"
	"github.com/kameas-ai/kenaz-harness/core/agentgraph/compaction"
)

// compaction_posttool_contextwindow_test.go is AC-012
// (chat-turn-integrity-01PMZ606 WP10, CK-03). The post_tool
// CompactionInput{} literal in exec_compute.go
// (builtinToolExecutor.Execute) omitted ContextWindow entirely, so
// pipeline.Run's threshold gate always took the "context window
// unknown" skip branch — the post_tool site could never fire
// regardless of its Enabled config. The Settings checkbox
// ("Post-tool result trim" in CompactionStrategyPanel.vue) was inert BY
// CONSTRUCTION, not by configuration: flipping it on changed nothing.
//
// This drives the REAL compaction.Pipeline (not a fake Compactor)
// through the full kernel dispatch path, so the observed skip reason
// comes from the pipeline's own gate logic (core/agentgraph/compaction
// /pipeline.go's `window <= 0` check), not a hand-rolled mimic of it.

// activeModelLLM is an LLMProvider that also advertises the active
// model, exercising the optional ActiveModelSource seam.
// resolveContextWindow falls back to it when the caller (the post_tool
// site's generic builtin-tool executor) carries no explicit
// Provider/Model of its own.
type activeModelLLM struct {
	kind, model string
}

func (a *activeModelLLM) Generate(_ context.Context, _ agentgraph.LLMRequest) (agentgraph.LLMResponse, error) {
	return agentgraph.LLMResponse{}, nil
}
func (a *activeModelLLM) ProviderKind() string  { return a.kind }
func (a *activeModelLLM) ActiveModelID() string { return a.model }

// postToolSkipCapture is a compaction.EventEmitter that records every
// emitted compaction_fired event, race-safe per CLAUDE.md's fake-emitter
// pattern (the kernel may reach the post_tool site from a goroutine).
type postToolSkipCapture struct {
	mu     sync.Mutex
	events []compaction.Event
}

func (c *postToolSkipCapture) Emit(_ string, _ string, payload compaction.Event) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.events = append(c.events, payload)
	return nil
}

func (c *postToolSkipCapture) snapshot() []compaction.Event {
	c.mu.Lock()
	defer c.mu.Unlock()
	out := make([]compaction.Event, len(c.events))
	copy(out, c.events)
	return out
}

// TestKernel_PostToolCompaction_ContextWindowReachesRealPipeline is
// AC-012's affirmative half: a post-tool compaction with a known window
// and current tokens above the threshold must not skip with "context
// window unknown".
func TestKernel_PostToolCompaction_ContextWindowReachesRealPipeline(t *testing.T) {
	em := &postToolSkipCapture{}
	p := compaction.NewPipeline(compaction.WithEmitter(em))
	p.RegisterStrategy(compaction.NewDropOldestStrategy())

	// Explicitly enable post_tool. Production presets default it off
	// (core/agentgraph/compaction/presets.go — deliberate, per E-004's
	// "make it real, default it off"), but a user can flip the per-site
	// "Enabled" checkbox in CompactionStrategyPanel.vue, which writes
	// exactly this kind of override at the global/project layer. A low
	// threshold means the tool result below clears it once ContextWindow
	// is real.
	postCfg := compaction.SiteConfig{
		Enabled:               true,
		Strategy:              compaction.StrategyDropOldest,
		PreCallThreshold:      0.1,
		DropOldestKeepRecentN: 1,
	}
	postCfg.MarkAll()
	p.Resolver().Set(compaction.LayerGlobal, "", compaction.CompactionConfig{
		Sites: map[compaction.Site]compaction.SiteConfig{compaction.SitePostTool: postCfg},
	})

	// 4096 runes ≈ 1024 tokens (tokenizer.CountRequestTokens, 4
	// runes/token) — comfortably over the 200-token cutoff
	// (0.1 * testWindow) below, so the only thing that can make this
	// skip is ContextWindow never reaching the pipeline.
	const testWindow = 2000
	tools := &fakeTools{result: agentgraph.ToolResult{
		Content: strings.Repeat("x", 4096),
	}}
	graph := &agentgraph.Graph{
		ID:          "g-posttool-window",
		SpecVersion: "1",
		Entrypoints: []string{"t1"},
		Nodes: []agentgraph.Node{
			{
				ID:    "t1",
				Kind:  agentgraph.NodeKindSubagentDispatch,
				Attrs: agentgraph.SubagentDispatchAttrs{Profile: "explore", Prompt: "go"},
			},
		},
	}
	k := agentgraph.NewKernel(agentgraph.WithCompactor(p))
	env := &agentgraph.Env{
		RunID:          "r-posttool-window",
		SessionID:      "s-posttool-window",
		Graph:          graph,
		Tools:          tools,
		LLM:            &activeModelLLM{kind: "test", model: "m"},
		ContextWindows: fixedWindows(testWindow),
	}
	if err := k.Run(context.Background(), env); err != nil {
		t.Fatalf("Run: %v", err)
	}

	evs := em.snapshot()
	if len(evs) != 1 {
		t.Fatalf("expected 1 compaction_fired event from the post_tool site, got %d: %+v", len(evs), evs)
	}
	if evs[0].SkipReason == "context window unknown" {
		t.Fatalf("post_tool compaction skipped with %q — ContextWindow never reached the real pipeline (CK-03 regression)",
			evs[0].SkipReason)
	}
	if evs[0].Skipped {
		t.Fatalf("expected the post_tool site to actually fire (not skip) with a known window and current tokens over threshold; got Skipped=true reason=%q",
			evs[0].SkipReason)
	}
}

// TestKernel_PostToolCompaction_DisabledSiteSkipsForTheRightReason is
// AC-012's other half: with the site disabled (production's shipped
// default — presets.go), it must still skip, and for the honest reason
// ("site disabled"), never "context window unknown". A fix that made
// ContextWindow real but broke the Enabled gate would still be a lie,
// just a different one.
func TestKernel_PostToolCompaction_DisabledSiteSkipsForTheRightReason(t *testing.T) {
	em := &postToolSkipCapture{}
	// ProductionDefaults() is what core/rpc/api.go actually wires
	// (deliberately avoiding compaction.SafeDefaults(), which enables
	// post_tool — see api.go's "SafeDefaults ... is deliberately still
	// avoided" comment and presets.go's per-site rationale). SitePostTool
	// is Enabled=false here at every tier, by design, not by omission.
	p := compaction.NewPipeline(
		compaction.WithResolver(compaction.NewMemoryResolverWithDefaults(compaction.ProductionDefaults())),
		compaction.WithEmitter(em),
	)
	p.RegisterStrategy(compaction.NewDropOldestStrategy())

	tools := &fakeTools{result: agentgraph.ToolResult{
		Content: strings.Repeat("x", 4096),
	}}
	graph := &agentgraph.Graph{
		ID:          "g-posttool-disabled",
		SpecVersion: "1",
		Entrypoints: []string{"t1"},
		Nodes: []agentgraph.Node{
			{
				ID:    "t1",
				Kind:  agentgraph.NodeKindSubagentDispatch,
				Attrs: agentgraph.SubagentDispatchAttrs{Profile: "explore", Prompt: "go"},
			},
		},
	}
	k := agentgraph.NewKernel(agentgraph.WithCompactor(p))
	env := &agentgraph.Env{
		RunID:          "r-posttool-disabled",
		SessionID:      "s-posttool-disabled",
		Graph:          graph,
		Tools:          tools,
		LLM:            &activeModelLLM{kind: "test", model: "m"},
		ContextWindows: fixedWindows(200_000),
	}
	if err := k.Run(context.Background(), env); err != nil {
		t.Fatalf("Run: %v", err)
	}

	evs := em.snapshot()
	if len(evs) != 1 {
		t.Fatalf("expected 1 compaction_fired event, got %d: %+v", len(evs), evs)
	}
	if !evs[0].Skipped {
		t.Fatalf("expected the disabled post_tool site to skip")
	}
	if evs[0].SkipReason != "site disabled" {
		t.Fatalf("skip reason = %q, want %q", evs[0].SkipReason, "site disabled")
	}
}
