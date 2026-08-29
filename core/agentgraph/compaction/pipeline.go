// Pipeline orchestrates strategy selection + budget enforcement at
// the three invocation sites. The kernel calls Pipeline at the
// pre_call / post_tool / manual sites; the pipeline reads the
// resolved CompactionConfig, picks a strategy, runs it, and emits a
// compaction_fired event.
//
// Strategy selection is one-pass: the pipeline calls strategy.Compact
// once, records the event, and returns. Cascading retry / fallback
// is the caller's job — the kernel can decide to call again with a
// different strategy if the first one underperforms.
package compaction

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/kameas-ai/kenaz-harness/core/agentgraph"
)

// Pipeline is the top-level entrypoint the kernel uses. Construct one
// with NewPipeline, register strategies with RegisterStrategy, and
// dispatch with Compact.
type Pipeline struct {
	mu sync.RWMutex

	resolver   Resolver
	strategies map[Strategy]Compactor
	emitter    EventEmitter
	now        func() time.Time
}

// PipelineOption tunes a Pipeline at construction.
type PipelineOption func(*Pipeline)

// WithResolver overrides the cascading-config resolver. Default is a
// memory-only resolver constructed in-place.
func WithResolver(r Resolver) PipelineOption {
	return func(p *Pipeline) {
		if r != nil {
			p.resolver = r
		}
	}
}

// WithEmitter wires the event emitter. Nil disables event emission
// (the pipeline still runs the strategy).
func WithEmitter(e EventEmitter) PipelineOption {
	return func(p *Pipeline) { p.emitter = e }
}

// WithClock overrides the clock used for event timestamps.
//
// CK-04/CK-05/CK-06 justify(blocker: "compaction.Event (the
// EventCompactionFired payload) has no timestamp field to stamp — the
// clock this overrides is read (p.now / the cloned CompactOpts.Now
// field) but never called anywhere in this package", owner: alec,
// date: 2026-08-29; chat-turn-integrity-01PMZ606 WP13): this doc used
// to say "Tests use this to make event payloads deterministic", but no
// event payload carries a timestamp for a clock to make deterministic
// — see compactor.go's Event struct. Wiring this for real means adding
// a Timestamp field to Event and a call site that reads it, which is a
// new capability, not a missing wire.
func WithClock(now func() time.Time) PipelineOption {
	return func(p *Pipeline) {
		if now != nil {
			p.now = now
		}
	}
}

// NewPipeline constructs a Pipeline. Strategies must still be
// registered via RegisterStrategy before they can be dispatched.
func NewPipeline(opts ...PipelineOption) *Pipeline {
	p := &Pipeline{
		strategies: make(map[Strategy]Compactor, len(AllStrategies())),
		resolver:   NewMemoryResolver(),
		now:        func() time.Time { return time.Now().UTC() },
	}
	for _, o := range opts {
		o(p)
	}
	return p
}

// RegisterStrategy installs a strategy implementation. Last-write-wins
// on collision so production wiring can swap defaults for tests.
func (p *Pipeline) RegisterStrategy(c Compactor) {
	if c == nil {
		return
	}
	p.mu.Lock()
	p.strategies[c.Strategy()] = c
	p.mu.Unlock()
}

// Bind returns a shallow clone of the pipeline with c installed as an
// additional strategy, for strategies that cannot be registered once
// at construction because they need per-run identity.
//
// StrategySessionRewrite is the motivating case: it rewrites the
// persisted history of one conversation using one model, so its
// implementation closes over the run's (session, profile, model)
// triple. Registering it globally would either race between concurrent
// chat sessions or force that triple through every strategy's
// signature.
//
// The clone SHARES the resolver, the emitter and the clock — there is
// still exactly one cascading-config surface and one event log, which
// is the property that makes this "one pipeline, bound per run" rather
// than "a second pipeline". Only the strategy table is copied, so a
// binding cannot leak into another run.
//
// A nil c returns the receiver unchanged.
func (p *Pipeline) Bind(c Compactor) *Pipeline {
	if p == nil {
		return nil
	}
	if c == nil {
		return p
	}
	p.mu.RLock()
	clone := &Pipeline{
		resolver:   p.resolver,
		emitter:    p.emitter,
		now:        p.now,
		strategies: make(map[Strategy]Compactor, len(p.strategies)+1),
	}
	for k, v := range p.strategies {
		clone.strategies[k] = v
	}
	p.mu.RUnlock()
	clone.strategies[c.Strategy()] = c
	return clone
}

// Strategies returns the registered strategy names (sorted by the
// canonical AllStrategies order).
func (p *Pipeline) Strategies() []Strategy {
	p.mu.RLock()
	defer p.mu.RUnlock()
	out := make([]Strategy, 0, len(p.strategies))
	for _, s := range AllStrategies() {
		if _, ok := p.strategies[s]; ok {
			out = append(out, s)
		}
	}
	return out
}

// Resolver returns the configured Resolver. The kernel exposes this
// to the RPC view so SetConfig / GetConfig calls land on the same
// in-memory state the pipeline reads from.
func (p *Pipeline) Resolver() Resolver {
	p.mu.RLock()
	defer p.mu.RUnlock()
	return p.resolver
}

// CompactRequest is the per-fire input to Pipeline.Compact. The
// kernel populates it from the active scope (run + node) and the
// invocation site.
type CompactRequest struct {
	// RunID + NodeID are stamped on the emitted compaction_fired
	// event. NodeID is empty for SiteManual.
	RunID  string
	NodeID string

	// Scope identifies the resolution chain the pipeline walks. Empty
	// fields fall through to the next layer.
	Scope ScopeKey
	// Site is which kernel boundary fired. Required.
	Site Site
	// Override forces a particular strategy regardless of the resolved
	// config. Used by SiteManual when the user picks a strategy from
	// the UI. Empty falls back to resolved config.
	Override Strategy
	// Input is the messages + budget the strategy works on.
	Input ContextSlice
	// Opts are the strategy-specific knobs (custom graph, model name,
	// keep-recent N). The pipeline merges these on top of any per-node
	// override pulled from the resolver.
	Opts CompactOpts
}

// CompactResult is the per-fire output. Always populated; on the
// skipped path Compacted is the input unchanged and Skipped is true.
type CompactResult struct {
	Compacted CompactedContext
	Skipped   bool
	Reason    string
}

// Run runs one compaction request end-to-end:
//  1. Resolve the effective config for the request scope + site.
//  2. Pick the strategy (override > resolved > default drop_oldest).
//  3. Look up the registered compactor.
//  4. Run it, emit compaction_fired, return.
//
// Returns ErrUnknownStrategy if the resolved name has no registered
// compactor, ErrRecursionExceeded if a custom_subgraph nests past the
// depth bound, or whatever the strategy itself surfaces.
func (p *Pipeline) Run(ctx context.Context, req CompactRequest) (CompactResult, error) {
	if req.Site == "" {
		return CompactResult{}, errors.New("compaction: pipeline: empty site")
	}
	cfg := p.resolver.Resolve(req.Scope)
	siteCfg := cfg.ForSite(req.Site)

	// If the site is disabled at the effective layer, skip.
	if !siteCfg.Enabled {
		bytesIn := bytesOf(req.Input.Messages)
		p.emit(req.RunID, req.NodeID, Event{
			Strategy:   siteCfg.Strategy,
			Site:       req.Site,
			MsgsIn:     len(req.Input.Messages),
			MsgsOut:    len(req.Input.Messages),
			BytesIn:    bytesIn,
			BytesOut:   bytesIn,
			Skipped:    true,
			SkipReason: "site disabled",
		})
		passthrough := append([]agentgraph.Message(nil), req.Input.Messages...)
		return CompactResult{
			Compacted: CompactedContext{
				Messages:    passthrough,
				TokensAfter: approxTokens(bytesIn),
				Strategy:    siteCfg.Strategy,
			},
			Skipped: true,
			Reason:  "site disabled",
		}, nil
	}

	strat := req.Override
	if strat == "" {
		strat = siteCfg.Strategy
	}
	if strat == "" {
		strat = StrategyDropOldest
	}

	p.mu.RLock()
	c, ok := p.strategies[strat]
	p.mu.RUnlock()
	if !ok {
		return CompactResult{}, fmt.Errorf("%w: %q", ErrUnknownStrategy, strat)
	}

	// Budget evaluation for the automatic sites (pre_call / post_tool).
	//
	// These fire on every node execution, so firing must be conditional
	// on actually approaching the context limit — otherwise a strategy
	// like summary collapses the whole transcript on every turn. Two
	// things are needed and neither existed before: a real denominator
	// (the model's context window) and an enforced threshold.
	//
	// PreCallThreshold is the fraction of the window above which we
	// compact. Below it, skip. At or above it, hand the strategy a
	// concrete TargetTokens to land under — strategies that ignore
	// TargetTokens (summary, semantic_cluster) still rewrite the whole
	// slice, but only once we are genuinely over budget, which is when
	// that is the intended behaviour.
	//
	// An unknown window (0) means skip: compacting toward a guessed
	// target is worse than not compacting.
	//
	// SiteManual is exempt throughout — an explicit request to compact
	// means it, and the compact node carries its own budget attr.
	// An explicitly pinned TargetTokens is a caller saying "compact to
	// this size" — honour it without threshold evaluation. The kernel's
	// automatic sites pass 0, so they always go through the gate below;
	// only a deliberate caller bypasses it.
	if req.Site != SiteManual && req.Input.TargetTokens <= 0 {
		threshold := siteCfg.PreCallThreshold
		if threshold <= 0 || threshold > 1 {
			threshold = DefaultPreCallThreshold
		}
		window := req.Input.ContextWindow
		current := req.Input.CurrentTokens
		if window <= 0 || current <= 0 || float64(current) < threshold*float64(window) {
			reason := "under compaction threshold"
			if window <= 0 {
				reason = "context window unknown"
			}
			bytesIn := bytesOf(req.Input.Messages)
			p.emit(req.RunID, req.NodeID, Event{
				Strategy:   strat,
				Site:       req.Site,
				MsgsIn:     len(req.Input.Messages),
				MsgsOut:    len(req.Input.Messages),
				BytesIn:    bytesIn,
				BytesOut:   bytesIn,
				Skipped:    true,
				SkipReason: reason,
			})
			passthrough := append([]agentgraph.Message(nil), req.Input.Messages...)
			return CompactResult{
				Compacted: CompactedContext{
					Messages:    passthrough,
					TokensAfter: approxTokens(bytesIn),
					Strategy:    strat,
				},
				Skipped: true,
				Reason:  reason,
			}, nil
		}
		// Over threshold: give the strategy a real target if the caller
		// did not pin one.
		if req.Input.TargetTokens <= 0 {
			req.Input.TargetTokens = int(threshold * float64(window))
		}
	}

	// Merge resolved-config opts on top of caller opts (caller wins).
	merged := mergeOpts(siteCfg, req.Opts)

	cc, err := c.Compact(ctx, req.Input, merged)
	if err != nil {
		// Recursion-cap is a soft skip per FR-045: emit the
		// "skipped: depth-exceeded" event and return Skipped, no err.
		if errors.Is(err, ErrRecursionExceeded) {
			p.emit(req.RunID, req.NodeID, Event{
				Strategy:   strat,
				Site:       req.Site,
				MsgsIn:     len(req.Input.Messages),
				MsgsOut:    len(req.Input.Messages),
				BytesIn:    bytesOf(req.Input.Messages),
				BytesOut:   bytesOf(req.Input.Messages),
				Skipped:    true,
				SkipReason: "depth-exceeded",
			})
			return CompactResult{
				Compacted: CompactedContext{
					Messages:    append([]agentgraph.Message(nil), req.Input.Messages...),
					Strategy:    strat,
					TokensAfter: approxTokens(bytesOf(req.Input.Messages)),
				},
				Skipped: true,
				Reason:  "depth-exceeded",
			}, nil
		}
		return CompactResult{}, err
	}

	bytesIn := bytesOf(req.Input.Messages)
	bytesOut := bytesOf(cc.Messages)
	p.emit(req.RunID, req.NodeID, Event{
		Strategy:   strat,
		Site:       req.Site,
		BytesSaved: bytesIn - bytesOut,
		BytesIn:    bytesIn,
		BytesOut:   bytesOut,
		MsgsIn:     len(req.Input.Messages),
		MsgsOut:    len(cc.Messages),
	})
	return CompactResult{Compacted: cc}, nil
}

// emit records a compaction_fired event via the configured emitter.
// Errors are swallowed: an EventLog append failure should not break
// the kernel's hot path. The pipeline relies on the kernel's outer
// log batch to surface upstream errors instead.
func (p *Pipeline) emit(runID, nodeID string, ev Event) {
	if p.emitter == nil {
		return
	}
	_ = p.emitter.Emit(runID, nodeID, ev)
}

// mergeOpts merges site-level config + caller opts. The caller's opts
// win on every field set; site config fills in zero-valued fields.
func mergeOpts(site SiteConfig, in CompactOpts) CompactOpts {
	out := in
	if out.MaxRecursionDepth == 0 {
		out.MaxRecursionDepth = site.MaxRecursionDepth
	}
	if out.DropOldestKeepRecentN == 0 {
		out.DropOldestKeepRecentN = site.DropOldestKeepRecentN
	}
	if out.SemanticClusterCount == 0 {
		out.SemanticClusterCount = site.SemanticClusterCount
	}
	if out.SummaryProvider == "" {
		out.SummaryProvider = site.SummaryProvider
	}
	if out.SummaryModel == "" {
		out.SummaryModel = site.SummaryModel
	}
	if out.SubgraphInputPort == "" {
		out.SubgraphInputPort = site.SubgraphInputPort
	}
	if out.SubgraphOutputPort == "" {
		out.SubgraphOutputPort = site.SubgraphOutputPort
	}
	if out.CustomGraph == nil {
		out.CustomGraph = site.CustomGraph
	}
	return out
}

// EventEmitterFunc adapts a function to the EventEmitter interface.
type EventEmitterFunc func(runID, nodeID string, payload Event) error

// Emit satisfies EventEmitter.
func (f EventEmitterFunc) Emit(runID, nodeID string, payload Event) error {
	return f(runID, nodeID, payload)
}

// EventLogEmitter bridges this package's EventEmitter to an
// agentgraph.EventLog. Production wiring uses this so every
// compaction_fired hits the same on-disk log every other kernel
// event lands in.
//
// The bridge produces an EventBatch with one entry of kind
// agentgraph.EventCompactionFired carrying a JSON payload that
// matches the local Event struct.
func EventLogEmitter(log agentgraph.EventLog) EventEmitter {
	if log == nil {
		return nil
	}
	return EventEmitterFunc(func(runID, nodeID string, payload Event) error {
		var batch agentgraph.EventBatch
		if err := batch.AppendKind(runID, nodeID, agentgraph.EventCompactionFired, payload); err != nil {
			return err
		}
		_, err := log.Append(batch)
		return err
	})
}

// ---- agentgraph.Compactor adapter ----

// kernelSiteToSite translates the kernel's CompactionSite enum to
// the local Site enum. Both enums share string identities so this
// is a value-mirror.
func kernelSiteToSite(s agentgraph.CompactionSite) Site {
	switch s {
	case agentgraph.CompactionSitePreCall:
		return SitePreCall
	case agentgraph.CompactionSitePostTool:
		return SitePostTool
	case agentgraph.CompactionSiteManual:
		return SiteManual
	}
	return Site(string(s))
}

// Compact satisfies agentgraph.Compactor. The kernel calls into this
// from LLMNode pre-call and ToolNode post-tool firing sites; the
// pipeline resolves cascading config, dispatches the strategy, and
// returns the kernel-shaped output.
//
// This is the seam that keeps the import direction one-way:
// core/agentgraph defines the Compactor interface, this package
// implements it via *Pipeline, and the kernel holds the interface
// value (no compile-time dependency on this package).
func (p *Pipeline) Compact(ctx context.Context, in agentgraph.CompactionInput) (agentgraph.CompactionOutput, error) {
	res, err := p.Run(ctx, CompactRequest{
		RunID:  in.RunID,
		NodeID: in.NodeID,
		Scope: ScopeKey{
			ProjectID: in.ProjectID,
			SessionID: in.SessionID,
			RunID:     in.RunID,
			NodeID:    in.NodeID,
		},
		Site: kernelSiteToSite(in.Site),
		Input: ContextSlice{
			Messages:      in.Messages,
			SystemPrompt:  in.SystemPrompt,
			TargetTokens:  in.TargetTokens,
			CurrentTokens: in.CurrentTokens,
			ContextWindow: in.ContextWindow,
			Site:          kernelSiteToSite(in.Site),
			SessionID:     in.SessionID,
		},
	})
	if err != nil {
		return agentgraph.CompactionOutput{}, err
	}
	return agentgraph.CompactionOutput{
		Messages:    res.Compacted.Messages,
		TokensAfter: res.Compacted.TokensAfter,
		Skipped:     res.Skipped,
		Reason:      res.Reason,
	}, nil
}
