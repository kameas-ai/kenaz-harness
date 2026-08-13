package agentgraph

import (
	"context"
	"strings"
	"sync"
	"testing"

	coreag "github.com/kameas-ai/kenaz-harness/core/agentgraph"
	fr041 "github.com/kameas-ai/kenaz-harness/core/agentgraph/compaction"
)

// env_deps_watermark_test.go pins review finding F2 of
// agentgraph-total-convergence-01PMGX01 WP08.
//
// THE DEFECT. `Env.AutoCompaction == nil` means "no watermark, no gate":
// `(*CompactionWatermark).Admit` returns true on a nil receiver, so every
// automatic compaction site fires on its first visit. That was a correct
// and harmless default while the FR-041 presets disabled `SitePreCall` at
// all five dial tiers — an ungated site that is switched off cannot fire.
//
// WP08 enabled the site at every tier but "off". Only the chat runner
// armed a watermark, so every OTHER kernel run — a workflow, a delegate
// dispatch, a user-authored graph — would compact at its very first model
// call, before the run had accumulated anything to shed. Worse, the
// presets file CLAIMED the watermark gated the site "by construction",
// which was true only for chat: a doc asserting the opposite of reality
// for every non-chat caller.
//
// THE TESTS. Both drive the REAL production preset pipeline
// (`PresetForTier` seeded at the global layer, `DropOldestStrategy`
// registered) rather than a hand-built config, because the defect lives
// in the interaction between the preset's Enabled flag and the Env's
// default — a fixture that hardcoded either half would not have caught it.

// countingCompactor counts dispatches. It is deliberately NOT the
// pipeline: it wraps one, so the test sees exactly what the kernel asked
// the pipeline to do, including calls the pipeline then skips.
type countingCompactor struct {
	inner coreag.Compactor

	mu    sync.Mutex
	calls []coreag.CompactionInput
}

func (c *countingCompactor) Compact(ctx context.Context, in coreag.CompactionInput) (coreag.CompactionOutput, error) {
	c.mu.Lock()
	c.calls = append(c.calls, in)
	c.mu.Unlock()
	return c.inner.Compact(ctx, in)
}

func (c *countingCompactor) snapshot() []coreag.CompactionInput {
	c.mu.Lock()
	defer c.mu.Unlock()
	out := make([]coreag.CompactionInput, len(c.calls))
	copy(out, c.calls)
	return out
}

// watermarkTestLLM is a minimal LLMProvider that also answers the
// ActiveModelSource question, so resolveContextWindow finds a window and
// the pipeline has a real denominator to evaluate its threshold against.
type watermarkTestLLM struct {
	mu   sync.Mutex
	seen int
}

func (l *watermarkTestLLM) Generate(_ context.Context, _ coreag.LLMRequest) (coreag.LLMResponse, error) {
	l.mu.Lock()
	l.seen++
	l.mu.Unlock()
	return coreag.LLMResponse{Content: "ok"}, nil
}

func (l *watermarkTestLLM) ProviderKind() string  { return "test" }
func (l *watermarkTestLLM) ActiveModelID() string { return "m" }

type fixedWindow int

func (w fixedWindow) ContextWindow(_, _ string) int { return int(w) }

// buildWatermarkFixture returns a one-model-node graph plus an Env built
// the way production builds one for a graph-authored run, and a
// compactor wrapping the real balanced-tier pipeline.
//
// The history is large relative to the window so the pipeline's own
// threshold gate would NOT skip: if the kernel dispatches, the strategy
// really runs. That is what makes a zero-call assertion meaningful.
func buildWatermarkFixture(t *testing.T, runID string) (*coreag.Env, *countingCompactor) {
	t.Helper()

	pipeline := fr041.NewPipeline(
		fr041.WithResolver(fr041.NewMemoryResolverWithDefaults(fr041.PresetForTier("balanced"))),
	)
	pipeline.RegisterStrategy(fr041.NewDropOldestStrategy())

	// Sanity: the fixture is only meaningful if the site under test is
	// actually enabled at this tier. If a future preset change disables
	// it again, fail loudly rather than pass vacuously.
	if !fr041.PresetForTier("balanced").ForSite(fr041.SitePreCall).Enabled {
		t.Fatalf("balanced tier has SitePreCall disabled; this test can no longer distinguish a gated site from a switched-off one")
	}

	compactor := &countingCompactor{inner: pipeline}

	history := make([]coreag.Message, 0, 40)
	for i := 0; i < 40; i++ {
		history = append(history, coreag.Message{
			Role:    "user",
			Content: strings.Repeat("padding padding padding ", 40),
		})
	}

	env := &coreag.Env{
		RunID:     runID,
		SessionID: "s-" + runID,
		Graph: &coreag.Graph{
			ID:          "g-watermark",
			SpecVersion: "1",
			Entrypoints: []string{"llm1"},
			Nodes: []coreag.Node{
				{
					ID:   "llm1",
					Kind: coreag.NodeKindModel,
					Attrs: coreag.ModelAttrs{
						Provider: "test", Model: "m", MaxTokens: 256,
					},
				},
			},
		},
		LLM:            &watermarkTestLLM{},
		Compactor:      compactor,
		ContextWindows: fixedWindow(1000),
		History: coreag.HistoryReaderFunc(func(context.Context, string, int) ([]coreag.Message, error) {
			return history, nil
		}),
	}
	env.State = coreag.NewRunState()
	return env, compactor
}

// TestEnvDeps_ArmsWatermarkForGraphAuthoredRuns is the fix: applyTo must
// leave every Env it touches carrying a watermark, so a graph-authored
// run's first pre-call site is refused exactly as chat's is.
func TestEnvDeps_ArmsWatermarkForGraphAuthoredRuns(t *testing.T) {
	env, compactor := buildWatermarkFixture(t, "graph-authored")

	// The production seam: whatever the chassis wired, applyTo runs over
	// the Env before the kernel does. Empty deps is the honest minimum —
	// a chassis with no optional seams at all must still get the gate.
	EnvDeps{}.applyTo(env)

	if env.AutoCompaction == nil {
		t.Fatalf("applyTo left Env.AutoCompaction nil — a nil watermark ungates every automatic compaction site, so this run compacts on its first model call")
	}

	if err := coreag.NewKernel().Run(context.Background(), env); err != nil {
		t.Fatalf("Run: %v", err)
	}

	if calls := compactor.snapshot(); len(calls) != 0 {
		t.Fatalf("graph-authored run dispatched %d automatic compaction call(s) at its first pre-call site, want 0 — the run had not grown past any baseline yet: %+v",
			len(calls), calls)
	}

	// The baseline must actually have latched. A watermark that refused
	// because it never observed anything would satisfy the assertion
	// above for the wrong reason and then admit the next site blind.
	if _, latched := env.AutoCompaction.Baseline(); !latched {
		t.Fatalf("pre-call site did not latch the watermark baseline")
	}
}

// TestEnvDeps_DoesNotOverwriteACallerSuppliedWatermark guards the other
// direction. The chat runner sets AutoCompaction on the Env it builds
// and THEN invokes this callback, so an unconditional assignment here
// would silently discard the chat surface's own policy — including any
// future per-session tuning of it.
func TestEnvDeps_DoesNotOverwriteACallerSuppliedWatermark(t *testing.T) {
	env, _ := buildWatermarkFixture(t, "caller-supplied")

	mine := coreag.NewCompactionWatermark(coreag.CompactionWatermarkPolicy{
		MarginTokens: 4321, MarginFraction: -1,
	})
	env.AutoCompaction = mine

	EnvDeps{}.applyTo(env)

	if env.AutoCompaction != mine {
		t.Fatalf("applyTo replaced a caller-supplied watermark; the chat runner's own policy would be silently discarded")
	}
	if got := env.AutoCompaction.Policy().MarginTokens; got != 4321 {
		t.Fatalf("watermark policy MarginTokens = %d, want 4321", got)
	}
}

// TestEnvDeps_WatermarkAdmitsOnceTheRunGrows proves the gate is a
// watermark and not an off switch. If applyTo had been "fixed" by
// disabling compaction for graph runs, the assertions above would pass
// and mid-run compaction would be dead everywhere outside chat — which
// is the ceiling turn-context-runway-01PMAG03 was written to remove.
func TestEnvDeps_WatermarkAdmitsOnceTheRunGrows(t *testing.T) {
	env, _ := buildWatermarkFixture(t, "grows")
	EnvDeps{}.applyTo(env)

	w := env.AutoCompaction
	if w == nil {
		t.Fatalf("applyTo left Env.AutoCompaction nil")
	}
	if w.Admit(1000) {
		t.Fatalf("first observation admitted; it is the one that latches the baseline")
	}
	if w.Admit(1000) {
		t.Fatalf("a second observation of the SAME size admitted; nothing has grown")
	}
	if !w.Admit(1_000_000) {
		t.Fatalf("a run that grew by ~1M tokens past its baseline was refused — the gate is behaving as an off switch, not a watermark")
	}
}
