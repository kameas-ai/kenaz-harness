package compaction_test

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"

	"github.com/kameas-ai/kenaz-harness/core/agentgraph"
	"github.com/kameas-ai/kenaz-harness/core/agentgraph/compaction"
)

// recordingEmitter captures every emitted event so tests can assert
// the pipeline fires the right shape at every site.
type recordingEmitter struct {
	mu     sync.Mutex
	events []recordedEvent
}

type recordedEvent struct {
	RunID   string
	NodeID  string
	Payload compaction.Event
}

func (r *recordingEmitter) Emit(runID, nodeID string, payload compaction.Event) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.events = append(r.events, recordedEvent{RunID: runID, NodeID: nodeID, Payload: payload})
	return nil
}

func (r *recordingEmitter) snapshot() []recordedEvent {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make([]recordedEvent, len(r.events))
	copy(out, r.events)
	return out
}

func newPipeline(t *testing.T, em *recordingEmitter) *compaction.Pipeline {
	t.Helper()
	p := compaction.NewPipeline(compaction.WithEmitter(em))
	p.RegisterStrategy(compaction.NewDropOldestStrategy())
	p.RegisterStrategy(compaction.NewSummaryStrategy(nil))
	p.RegisterStrategy(compaction.NewSemanticClusterStrategy(nil))
	p.RegisterStrategy(compaction.NewCustomSubgraphStrategy(nil))
	return p
}

func TestPipeline_RunDispatchesStrategy(t *testing.T) {
	em := &recordingEmitter{}
	p := newPipeline(t, em)
	res, err := p.Run(context.Background(), compaction.CompactRequest{
		RunID:    "r1",
		NodeID:   "n1",
		Site:     compaction.SitePreCall,
		Override: compaction.StrategyDropOldest,
		Input: compaction.ContextSlice{
			Messages: []agentgraph.Message{
				{Role: "user", Content: strings.Repeat("a", 100)},
				{Role: "assistant", Content: strings.Repeat("b", 100)},
				{Role: "user", Content: strings.Repeat("c", 100)},
			},
			TargetTokens: 10,
		},
	})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if res.Skipped {
		t.Fatalf("expected not skipped")
	}
	if len(res.Compacted.Messages) >= 3 {
		t.Fatalf("expected drop_oldest to shrink, got %d", len(res.Compacted.Messages))
	}
	evs := em.snapshot()
	if len(evs) != 1 {
		t.Fatalf("expected 1 event emitted, got %d", len(evs))
	}
	if evs[0].Payload.Strategy != compaction.StrategyDropOldest {
		t.Fatalf("event strategy = %q", evs[0].Payload.Strategy)
	}
	if evs[0].Payload.Site != compaction.SitePreCall {
		t.Fatalf("event site = %q", evs[0].Payload.Site)
	}
	if evs[0].Payload.BytesSaved <= 0 {
		t.Fatalf("expected bytes_saved > 0, got %d", evs[0].Payload.BytesSaved)
	}
}

func TestPipeline_DisabledSiteSkipsAndEmits(t *testing.T) {
	em := &recordingEmitter{}
	p := newPipeline(t, em)
	// Override the global pre_call to disabled.
	disabled := compaction.SiteConfig{Enabled: false}
	disabled.MarkEnabled()
	p.Resolver().Set(compaction.LayerGlobal, "", compaction.CompactionConfig{
		Sites: map[compaction.Site]compaction.SiteConfig{
			compaction.SitePreCall: disabled,
		},
	})
	res, err := p.Run(context.Background(), compaction.CompactRequest{
		Site: compaction.SitePreCall,
		Input: compaction.ContextSlice{
			Messages: []agentgraph.Message{{Role: "user", Content: "x"}},
		},
	})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if !res.Skipped {
		t.Fatalf("expected skipped")
	}
	if res.Reason != "site disabled" {
		t.Fatalf("reason = %q", res.Reason)
	}
	evs := em.snapshot()
	if len(evs) != 1 || !evs[0].Payload.Skipped {
		t.Fatalf("expected one skipped event, got %+v", evs)
	}
}

func TestPipeline_RecursionCapEmitsSkipped(t *testing.T) {
	em := &recordingEmitter{}
	p := compaction.NewPipeline(compaction.WithEmitter(em))
	// Custom subgraph runner that just calls the pipeline again, forcing
	// recursive nesting until the cap fires.
	var runner *recursingRunner
	custom := compaction.NewCustomSubgraphStrategy(nil) // overwritten below
	_ = custom
	// We need to register a strategy that returns ErrRecursionExceeded
	// once the depth is past the cap. The CustomSubgraphStrategy already
	// does this via the depth-tracking in compactor.go; we just inject
	// a runner that drives one nested call.
	runner = &recursingRunner{p: p}
	p.RegisterStrategy(compaction.NewCustomSubgraphStrategy(runner))
	p.RegisterStrategy(compaction.NewDropOldestStrategy()) // fallback

	graph := &agentgraph.Graph{Entrypoints: []string{"x"}}
	// Build a request whose strategy is custom_subgraph; runner will
	// recurse until cap.
	_, err := p.Run(context.Background(), compaction.CompactRequest{
		Site:     compaction.SiteManual,
		Override: compaction.StrategyCustomSubgraph,
		Input: compaction.ContextSlice{
			Messages: []agentgraph.Message{{Role: "user", Content: "a"}},
		},
		Opts: compaction.CompactOpts{
			CustomGraph:       graph,
			MaxRecursionDepth: 2,
		},
	})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	// The outer call may succeed (the nested level produced a result),
	// but the depth-exceeded skip MUST have been emitted by the
	// deepest level. FR-045: every skip case emits an event.
	evs := em.snapshot()
	found := false
	for _, e := range evs {
		if e.Payload.Skipped && e.Payload.SkipReason == "depth-exceeded" {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("expected at least one depth-exceeded event in %+v", evs)
	}
}

// recursingRunner is a kernel runner that recurses by calling
// pipeline.Run with the same custom_subgraph strategy until the
// recursion-depth cap kicks in.
type recursingRunner struct {
	p *compaction.Pipeline
}

func (r *recursingRunner) RunGraph(ctx context.Context, g *agentgraph.Graph, _ map[string]agentgraph.PortValues) (map[string]agentgraph.PortValues, error) {
	res, err := r.p.Run(ctx, compaction.CompactRequest{
		Site:     compaction.SiteManual,
		Override: compaction.StrategyCustomSubgraph,
		Input: compaction.ContextSlice{
			Messages: []agentgraph.Message{{Role: "user", Content: "nested"}},
		},
		Opts: compaction.CompactOpts{
			CustomGraph:       g,
			MaxRecursionDepth: 2,
		},
	})
	if err != nil {
		return nil, err
	}
	// Surface the result via the leaf-output port the strategy reads.
	return map[string]agentgraph.PortValues{
		"leaf": {"messages": res.Compacted.Messages},
	}, nil
}

func TestPipeline_UnknownStrategyErrors(t *testing.T) {
	p := compaction.NewPipeline()
	_, err := p.Run(context.Background(), compaction.CompactRequest{
		Site:     compaction.SitePreCall,
		Override: compaction.Strategy("nope"),
		Input: compaction.ContextSlice{
			Messages: []agentgraph.Message{{Content: "x"}},
		},
	})
	if !errors.Is(err, compaction.ErrUnknownStrategy) {
		t.Fatalf("expected ErrUnknownStrategy, got %v", err)
	}
}

func TestPipeline_ResolvedStrategyFromCascade(t *testing.T) {
	em := &recordingEmitter{}
	p := newPipeline(t, em)
	// Project sets summary at pre_call.
	sc := compaction.SiteConfig{Strategy: compaction.StrategySummary}
	sc.MarkStrategy()
	p.Resolver().Set(compaction.LayerProject, "p1", compaction.CompactionConfig{
		Sites: map[compaction.Site]compaction.SiteConfig{compaction.SitePreCall: sc},
	})
	_, err := p.Run(context.Background(), compaction.CompactRequest{
		Scope: compaction.ScopeKey{ProjectID: "p1"},
		Site:  compaction.SitePreCall,
		Input: compaction.ContextSlice{
			Messages: []agentgraph.Message{{Role: "user", Content: "hello"}},
		},
	})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	evs := em.snapshot()
	if evs[0].Payload.Strategy != compaction.StrategySummary {
		t.Fatalf("expected resolved strategy summary, got %q", evs[0].Payload.Strategy)
	}
}

func TestPipeline_EventLogBridgeWritesCompactionFired(t *testing.T) {
	log := agentgraph.NewMemoryEventLog()
	p := compaction.NewPipeline(compaction.WithEmitter(compaction.EventLogEmitter(log)))
	p.RegisterStrategy(compaction.NewDropOldestStrategy())
	_, err := p.Run(context.Background(), compaction.CompactRequest{
		RunID:    "r1",
		Site:     compaction.SitePreCall,
		Override: compaction.StrategyDropOldest,
		Input: compaction.ContextSlice{
			Messages: []agentgraph.Message{
				{Role: "user", Content: strings.Repeat("a", 200)},
				{Role: "assistant", Content: strings.Repeat("b", 200)},
			},
			TargetTokens: 5,
		},
	})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	got := 0
	if err := log.Replay("r1", func(e agentgraph.Event) error {
		if e.Kind == agentgraph.EventCompactionFired {
			got++
		}
		return nil
	}); err != nil {
		t.Fatalf("Replay: %v", err)
	}
	if got != 1 {
		t.Fatalf("expected 1 compaction_fired event, got %d", got)
	}
}

func TestPipeline_AdaptsToAgentgraphCompactor(t *testing.T) {
	em := &recordingEmitter{}
	p := newPipeline(t, em)
	in := agentgraph.CompactionInput{
		Site:      agentgraph.CompactionSitePreCall,
		RunID:     "run-1",
		NodeID:    "node-1",
		SessionID: "s",
		ProjectID: "p",
		Messages: []agentgraph.Message{
			{Role: "user", Content: strings.Repeat("a", 200)},
			{Role: "assistant", Content: strings.Repeat("b", 200)},
			{Role: "user", Content: strings.Repeat("c", 200)},
		},
		TargetTokens: 10,
	}
	out, err := p.Compact(context.Background(), in)
	if err != nil {
		t.Fatalf("Compact: %v", err)
	}
	if len(out.Messages) >= len(in.Messages) {
		t.Fatalf("expected compaction to shrink messages, got %d / %d",
			len(out.Messages), len(in.Messages))
	}
	if out.Skipped {
		t.Fatalf("expected not skipped")
	}
	if len(em.snapshot()) != 1 {
		t.Fatalf("expected one event emitted")
	}
}
