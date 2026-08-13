package agentgraph_test

import (
	"context"
	"strconv"
	"strings"
	"sync"
	"testing"

	"github.com/kameas-ai/kenaz-harness/core/agentgraph"
)

// TestCompactionWatermark_FirstObservationIsAlwaysRefused is the
// turn-context-runway-01PMAG03 WP02 core invariant, and the reason the
// watermark is a safe replacement for SuppressAutomaticCompaction: the
// site that latches the baseline can never be the site that fires.
//
// This is compaction-convergence-01PMDL05's double-fire condition
// restated. It holds for ANY observation, however large, because the
// baseline is the observation.
func TestCompactionWatermark_FirstObservationIsAlwaysRefused(t *testing.T) {
	t.Parallel()
	for _, live := range []int{0, 1, 8192, 500_000, 10_000_000} {
		w := agentgraph.NewCompactionWatermark(agentgraph.CompactionWatermarkPolicy{})
		if w.Admit(live) {
			t.Fatalf("first Admit(%d) = true, want false — the baseline-latching observation must never fire", live)
		}
		if base, ok := w.Baseline(); !ok || base != live {
			t.Fatalf("Baseline() = (%d, %v), want (%d, true)", base, ok, live)
		}
	}
}

// TestCompactionWatermark_AdmitsOnlyPastTheMargin walks the lifecycle a
// long turn actually has: latch, grow a little (refused), grow past the
// margin (admitted).
func TestCompactionWatermark_AdmitsOnlyPastTheMargin(t *testing.T) {
	t.Parallel()
	w := agentgraph.NewCompactionWatermark(agentgraph.CompactionWatermarkPolicy{
		MarginTokens:   1000,
		MarginFraction: -1, // absolute-only, so the arithmetic is exact
	})
	if w.Admit(10_000) {
		t.Fatalf("first observation fired")
	}
	if w.Admit(10_500) {
		t.Fatalf("growth of 500 fired against a margin of 1000")
	}
	if w.Admit(11_000) {
		t.Fatalf("growth exactly to the threshold fired; the gate is strictly-greater")
	}
	if !w.Admit(11_001) {
		t.Fatalf("growth past the threshold was refused — a long turn can never compact")
	}
	if !w.Crossed() {
		t.Fatalf("Crossed() = false after an admitted observation")
	}
}

// TestCompactionWatermark_MarginComposition pins how the absolute floor
// and the relative fraction combine: max(), so the floor protects a
// small baseline and the fraction protects a large one.
func TestCompactionWatermark_MarginComposition(t *testing.T) {
	t.Parallel()
	p := agentgraph.CompactionWatermarkPolicy{MarginTokens: 1000, MarginFraction: 0.5}
	if got, want := p.Margin(100), 1000; got != want {
		t.Errorf("Margin(100) = %d, want %d (absolute floor dominates)", got, want)
	}
	if got, want := p.Margin(10_000), 5000; got != want {
		t.Errorf("Margin(10000) = %d, want %d (fraction dominates)", got, want)
	}
	if got, want := p.Threshold(10_000), 15_000; got != want {
		t.Errorf("Threshold(10000) = %d, want %d", got, want)
	}
	// A zero-margin policy would let the latching site fire against
	// itself. The floor of 1 forbids that.
	zero := agentgraph.CompactionWatermarkPolicy{MarginTokens: -1, MarginFraction: -1}
	if got := zero.Margin(0); got < 1 {
		t.Errorf("Margin(0) with an all-negative policy = %d, want >= 1", got)
	}

	// Default-filling must be idempotent. NewCompactionWatermark stores
	// a resolved policy and Margin resolves again on every call, so a
	// resolution that mapped "disabled" onto the zero value would
	// resurrect the default on the second pass — a disabled fraction
	// would silently become 0.5 and every margin would scale with the
	// baseline.
	absoluteOnly := agentgraph.CompactionWatermarkPolicy{MarginTokens: 1000, MarginFraction: -1}
	armed := agentgraph.NewCompactionWatermark(absoluteOnly)
	if got, want := armed.Policy().Margin(1_000_000), 1000; got != want {
		t.Errorf("Margin after double resolution = %d, want %d (a disabled fraction must stay disabled)", got, want)
	}
}

// TestCompactionWatermark_RearmRebaselines proves a turn that compacts
// mid-run does not then compact on every subsequent site: after a
// compaction lands the watermark re-baselines from the post-compaction
// transcript.
func TestCompactionWatermark_RearmRebaselines(t *testing.T) {
	t.Parallel()
	w := agentgraph.NewCompactionWatermark(agentgraph.CompactionWatermarkPolicy{
		MarginTokens: 1000, MarginFraction: -1,
	})
	w.Admit(10_000)
	if !w.Admit(20_000) {
		t.Fatalf("grown run was refused")
	}
	w.Rearm()
	if _, ok := w.Baseline(); ok {
		t.Fatalf("Rearm did not clear the latch")
	}
	// Post-compaction transcript is smaller; it re-latches there and is
	// refused again until it grows past the NEW baseline.
	if w.Admit(6000) {
		t.Fatalf("re-latching observation fired")
	}
	if w.Admit(6500) {
		t.Fatalf("fired 500 past a 1000 margin from the new baseline")
	}
	if !w.Admit(7500) {
		t.Fatalf("refused 1500 past a 1000 margin from the new baseline")
	}
}

// TestCompactionWatermark_NilIsUngated pins the graph-authored default:
// a run with no watermark keeps pre-mission behaviour, every automatic
// site free to fire.
func TestCompactionWatermark_NilIsUngated(t *testing.T) {
	t.Parallel()
	var w *agentgraph.CompactionWatermark
	if !w.Admit(1) {
		t.Errorf("nil watermark refused a site; nil must mean no gate")
	}
	if !w.Crossed() {
		t.Errorf("nil watermark reported not-crossed")
	}
	w.Rearm() // must not panic
}

// TestCompactionWatermark_ConcurrentAdmit is the race-discipline check:
// a LoopNode body reaches compaction sites from multiple goroutines.
func TestCompactionWatermark_ConcurrentAdmit(t *testing.T) {
	t.Parallel()
	w := agentgraph.NewCompactionWatermark(agentgraph.CompactionWatermarkPolicy{})
	var wg sync.WaitGroup
	for i := 0; i < 32; i++ {
		wg.Add(1)
		go func(n int) {
			defer wg.Done()
			_ = w.Admit(n * 1000)
			_ = w.Crossed()
		}(i)
	}
	wg.Wait()
	if _, ok := w.Baseline(); !ok {
		t.Fatalf("no baseline latched after 32 concurrent observations")
	}
}

// TestKernel_WatermarkSuppressesFirstPreCallSite is the WP02
// replacement for what a chat Env used to get from
// SuppressAutomaticCompaction. Same graph as
// TestKernel_FiresPreCallCompactionOnLLMNode (which fires exactly once
// with an ungated Env); with a watermark armed, zero calls.
func TestKernel_WatermarkSuppressesFirstPreCallSite(t *testing.T) {
	compactor := &recordingCompactor{}
	llm := &fakeLLM{resp: "ok"}
	graph := &agentgraph.Graph{
		ID:          "g-watermark-precall",
		SpecVersion: "1",
		Entrypoints: []string{"llm1"},
		Nodes: []agentgraph.Node{
			{
				ID:   "llm1",
				Kind: agentgraph.NodeKindModel,
				Attrs: agentgraph.ModelAttrs{
					Provider: "test", Model: "m", MaxTokens: 100,
				},
			},
		},
	}
	k := agentgraph.NewKernel(agentgraph.WithCompactor(compactor))
	env := &agentgraph.Env{
		RunID:          "r-watermark-precall",
		SessionID:      "s-watermark-precall",
		Graph:          graph,
		LLM:            llm,
		AutoCompaction: agentgraph.NewCompactionWatermark(agentgraph.CompactionWatermarkPolicy{}),
		// A large history: the point is that SIZE is not what gates the
		// first site. Nothing has *grown* yet.
		History: agentgraph.HistoryReaderFunc(func(_ context.Context, _ string, _ int) ([]agentgraph.Message, error) {
			hist := make([]agentgraph.Message, 0, 400)
			for i := 0; i < 400; i++ {
				hist = append(hist, agentgraph.Message{
					Role:    "user",
					Content: "turn " + strconv.Itoa(i) + " " + strings.Repeat("padding ", 40),
				})
			}
			return hist, nil
		}),
	}
	env.State = agentgraph.NewRunState()
	if err := k.Run(context.Background(), env); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if len(compactor.calls) != 0 {
		t.Fatalf("expected 0 compaction calls at the first pre-call site with a watermark armed, got %d — this is the compaction-convergence-01PMDL05 double-fire",
			len(compactor.calls))
	}
}

// TestKernel_WatermarkAdmitsGrownRun is the other half: once the run's
// live context HAS grown past the margin, the site fires. This is the
// runway the mission is named for — without it a turn compacts once
// pre-send and then grows monotonically until it overflows.
func TestKernel_WatermarkAdmitsGrownRun(t *testing.T) {
	compactor := &recordingCompactor{}
	watermark := agentgraph.NewCompactionWatermark(agentgraph.CompactionWatermarkPolicy{
		MarginTokens: 100, MarginFraction: -1,
	})
	// Pre-latch a small baseline, standing in for the state the run is
	// in after its first (suppressed) pre-call site.
	if watermark.Admit(10) {
		t.Fatalf("latching observation fired")
	}

	llm := &fakeLLM{resp: "ok"}
	graph := &agentgraph.Graph{
		ID:          "g-watermark-grown",
		SpecVersion: "1",
		Entrypoints: []string{"llm1"},
		Nodes: []agentgraph.Node{
			{
				ID:   "llm1",
				Kind: agentgraph.NodeKindModel,
				Attrs: agentgraph.ModelAttrs{
					Provider: "test", Model: "m", MaxTokens: 100,
				},
			},
		},
	}
	k := agentgraph.NewKernel(agentgraph.WithCompactor(compactor))
	env := &agentgraph.Env{
		RunID:          "r-watermark-grown",
		SessionID:      "s-watermark-grown",
		Graph:          graph,
		LLM:            llm,
		AutoCompaction: watermark,
		History: agentgraph.HistoryReaderFunc(func(_ context.Context, _ string, _ int) ([]agentgraph.Message, error) {
			hist := make([]agentgraph.Message, 0, 200)
			for i := 0; i < 200; i++ {
				hist = append(hist, agentgraph.Message{
					Role:    "user",
					Content: "accumulated tool output " + strconv.Itoa(i) + " " + strings.Repeat("x ", 60),
				})
			}
			return hist, nil
		}),
	}
	env.State = agentgraph.NewRunState()
	if err := k.Run(context.Background(), env); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if len(compactor.calls) != 1 {
		t.Fatalf("expected 1 compaction call once the run grew past the watermark, got %d — a long turn still cannot compact itself",
			len(compactor.calls))
	}
	if got := compactor.calls[0].Site; got != agentgraph.CompactionSitePreCall {
		t.Fatalf("site = %q, want pre_call", got)
	}
}

// TestKernel_NilCompactorBeatsACrossedWatermark is the rewritten form
// of the precedence test agentgraph-total-convergence-01PMGX01 WP08
// retired.
//
// The original pinned that the Env's hard suppression boolean beat an
// armed watermark, so a graph author always had an unconditional off
// switch. WP08 deleted that boolean — it is the symbol spec §6 I4
// forbids — but the underlying guarantee still has to hold: a crossed
// watermark must never be able to conjure a compaction on its own. It
// is a *gate*, not a trigger.
//
// The off switch that remains is the honest one: a run with no
// Compactor cannot compact, whatever the watermark says. This test
// arms a watermark with a margin of one token, latches it at zero, and
// feeds the run 10KB of history so the watermark is emphatically
// crossed — then asserts the site still does nothing, because there is
// nothing to do it with. The graph author's off switch is now "do not
// wire a compactor", and the user's is the "off" dial tier, which
// resolves to SiteConfig.Enabled=false in the pipeline. Both are
// controls that already existed and mean what they say.
func TestKernel_NilCompactorBeatsACrossedWatermark(t *testing.T) {
	watermark := agentgraph.NewCompactionWatermark(agentgraph.CompactionWatermarkPolicy{
		MarginTokens: 1, MarginFraction: -1,
	})
	watermark.Admit(0) // latch at zero so ANY growth crosses

	llm := &fakeLLM{resp: "ok"}
	graph := &agentgraph.Graph{
		ID:          "g-nil-compactor-beats-watermark",
		SpecVersion: "1",
		Entrypoints: []string{"llm1"},
		Nodes: []agentgraph.Node{
			{ID: "llm1", Kind: agentgraph.NodeKindModel, Attrs: agentgraph.ModelAttrs{Provider: "test", Model: "m", MaxTokens: 100}},
		},
	}
	// No WithCompactor: the kernel has nothing to dispatch through.
	k := agentgraph.NewKernel()
	env := &agentgraph.Env{
		RunID:          "r-nil-compactor-beats-watermark",
		Graph:          graph,
		LLM:            llm,
		AutoCompaction: watermark,
		History: agentgraph.HistoryReaderFunc(func(_ context.Context, _ string, _ int) ([]agentgraph.Message, error) {
			return []agentgraph.Message{{Role: "user", Content: strings.Repeat("y ", 5000)}}, nil
		}),
	}
	env.State = agentgraph.NewRunState()
	if err := k.Run(context.Background(), env); err != nil {
		t.Fatalf("Run: %v", err)
	}
	// The run completing without error IS the assertion: an ungated
	// site with a nil Compactor would nil-panic in admitAutomaticCompaction
	// or in the dispatch below it.
	if _, latched := watermark.Baseline(); !latched {
		t.Fatalf("watermark lost its latch")
	}
}
