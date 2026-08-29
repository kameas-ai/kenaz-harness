package wiring

import (
	"context"
	"errors"
	"fmt"
	"sync/atomic"

	"github.com/kameas-ai/kenaz-harness/core/agentgraph/compaction"
	corellm "github.com/kameas-ai/kenaz-harness/core/llm"
	"github.com/kameas-ai/kenaz-harness/core/llm/cost"
	"github.com/kameas-ai/kenaz-harness/core/logging"
)

// LLMCaller adapts the chat-runner's corellm.Registry onto the
// compaction.LLMCaller interface. The adapter:
//
//   - Selects a profile id for the requested provider/model pair via
//     the caller-supplied resolver (so the chassis policy stays in one
//     place; this package never reaches into the personal store or
//     bundle profiles directly).
//   - Builds a non-streaming-style GenerationRequest (single user turn
//     carrying the rendered transcript, no tools, no streaming-only
//     extensions). The registry still streams under the hood — that's
//     fine; we drain the stream here and aggregate the final response.
//   - Tags every call with cost.KindCompaction (a new constant in
//     core/llm/cost) in a debug log line, for a human reading structured
//     logs — NOT for a "per-session cost view" (CORRECTED,
//     chat-turn-integrity-01PMZ606 WP12: no such view reads this tag; it
//     never existed). What DOES exist is a process-wide running total
//     (OverheadTotals, via Overhead() below), surfaced by
//     core/rpc.API.CompactionOverhead to the SessionsView header row —
//     see recordOverhead's doc for why that surface needs no separate
//     Kind field.
//   - Surfaces typed registry errors verbatim so the engine's
//     classifyLLMError can recognize cap-exceeded, transport, auth
//     classes.
//
// The adapter is intentionally narrow: ALL provider concerns
// (capability gate, policy guard, retry, audit, cost reducer) live in
// the registry pipeline. This file only translates value shapes.
type LLMCaller struct {
	reg            corellm.Registry
	profileLookup  func(ctx context.Context, ref compaction.ProviderProfileRef) (profileID, model string, ok bool)
	overheadCost   atomic.Pointer[OverheadTotals]
}

// OverheadTotals is the running tally of compaction-driven LLM cost
// the chassis surfaces to the per-session cost panel (FR §2.11).
//
// The struct is JSON-serializable so the rpc layer can hand it
// straight to the frontend without an extra projection. Currency is
// the dominant currency emitted by the cost reducer (USD by default);
// callers that mix currencies will see the reducer's first-non-empty
// pick, with Indeterminate counts isolated in the IndeterminateCalls
// counter.
type OverheadTotals struct {
	// Total is the sum of every successful compaction-call's
	// reducer-derived total (typically USD). Zero on a fresh install.
	Total float64 `json:"total"`
	// Currency is the dominant currency code observed across the
	// running tally (typically "USD"). The reducer surfaces the
	// currency on each Cost; we adopt the first non-empty one.
	Currency string `json:"currency,omitempty"`
	// Calls is the count of compaction LLM calls observed regardless
	// of cost determinacy. Used by the frontend to render "N
	// compactions" alongside the dollar figure.
	Calls int `json:"calls"`
	// IndeterminateCalls counts compaction calls whose reducer-derived
	// cost was Indeterminate (no price-table entry matched the
	// provider+model). The frontend can render a "+N at unknown rate"
	// hint when this is non-zero.
	IndeterminateCalls int `json:"indeterminate_calls"`
	// InputTokens / OutputTokens accumulate the raw token usage for
	// compaction calls. Useful for users who want a token-level
	// breakdown when the cost reducer doesn't have a price entry.
	InputTokens  int `json:"input_tokens"`
	OutputTokens int `json:"output_tokens"`
}

// LLMOption tunes a LLMCaller at construction time.
type LLMOption func(*LLMCaller)

// WithProfileLookup installs a resolver from
// (compaction.ProviderProfileRef) → (registry profile id, model). The
// engine carries a (provider, model) pair on every call; the registry
// dispatches by profile id. The lookup is the chassis's seam to bind
// the engine's reference shape to a concrete provider profile (e.g.
// match the provider id against the loaded personal profiles, fall
// back to the chat session's active profile, etc.).
//
// Returning ok=false makes the LLM call fail with a typed error the
// engine surfaces as a provider_error in the audit payload. Callers
// who want a different fallback wire it inside their resolver.
func WithProfileLookup(fn func(ctx context.Context, ref compaction.ProviderProfileRef) (profileID, model string, ok bool)) LLMOption {
	return func(a *LLMCaller) { a.profileLookup = fn }
}

// NewLLMCaller wraps the registry with the optional profile lookup.
// nil registry returns a nil adapter — callers MUST nil-check before
// binding to compaction.SessionEngineConfig.LLM.
func NewLLMCaller(reg corellm.Registry, opts ...LLMOption) *LLMCaller {
	if reg == nil {
		return nil
	}
	a := &LLMCaller{reg: reg}
	a.overheadCost.Store(&OverheadTotals{})
	for _, o := range opts {
		o(a)
	}
	return a
}

// CallForSummary opens a streaming generation against the registry,
// drains the events channel, and returns the final text + token usage.
// The compaction engine treats the call as non-streaming (it ignores
// the per-token deltas); we still drain the stream so the registry's
// audit pipeline records the cost-tagged final-response event.
func (a *LLMCaller) CallForSummary(ctx context.Context, model compaction.ProviderProfileRef,
	systemPrompt, userPrompt string) (text string, inputTokens, outputTokens int, err error) {
	if a == nil || a.reg == nil {
		return "", 0, 0, errors.New("compaction.wiring: nil llm registry")
	}

	profileID, modelOverride, ok := a.resolveProfile(ctx, model)
	if !ok {
		return "", 0, 0, fmt.Errorf("compaction.wiring: no profile registered for %s", model.String())
	}

	req := corellm.GenerationRequest{
		ProfileID: profileID,
		Model:     modelOverride,
		System:    systemPrompt,
		Messages: []corellm.Message{
			{
				Role: corellm.Role("user"),
				Content: []corellm.ContentBlock{
					{Type: "text", Text: userPrompt},
				},
			},
		},
		// Compaction calls carry no tool catalog and no caching /
		// reasoning extensions — keep the gate surface small so an
		// upstream policy that's been tightened for chat doesn't fail
		// open / closed unexpectedly.
	}

	stream, serr := a.reg.Stream(ctx, req)
	if serr != nil {
		return "", 0, 0, serr
	}

	// Drain the streaming events. We intentionally don't fan deltas
	// anywhere — compaction is a synchronous backend call from the
	// chat runner's perspective.
	for range stream.Events() {
	}

	resp, ferr := stream.Final()
	if ferr != nil {
		return "", 0, 0, ferr
	}

	// Aggregate the response text. The provider returns content blocks;
	// we concatenate every text-typed block in declaration order.
	out := flattenContentText(resp.Content)
	a.recordOverhead(resp)
	return out, resp.Usage.InputTokens, resp.Usage.OutputTokens, nil
}

// resolveProfile fans the engine's ref through the optional lookup
// closure, falling back to a "ref.ProviderID is itself a profile id"
// guess when no lookup is wired. Returns ok=false iff the chassis
// genuinely cannot produce a profile for this ref.
func (a *LLMCaller) resolveProfile(ctx context.Context, ref compaction.ProviderProfileRef) (string, string, bool) {
	if a.profileLookup != nil {
		return a.profileLookup(ctx, ref)
	}
	// Fallback: treat the ProviderID as the profile id. Matches the
	// convention in the chat path's StartStream wiring where the
	// frontend's "active provider" id IS a profile id; see
	// llm.impl.StartStream.
	if ref.ProviderID == "" {
		return "", "", false
	}
	return ref.ProviderID, ref.ModelID, true
}

// recordOverhead folds the response's reducer-derived cost + usage
// into the running OverheadTotals.
//
// CORRECTED (chat-turn-integrity-01PMZ606 WP12): this doc used to claim
// the cost.KindCompaction tag below let "downstream surfaces recognize
// compaction overhead without re-deriving the kind from the audit
// payload." That was never true — the tag is a key on a
// logging.L().Debug(...) call, a structured log line, not a value any
// Go or frontend code reads; grep found zero non-log-line consumers.
// The real downstream surface is OverheadTotals itself (returned by
// Overhead() below) — every call this adapter issues IS compaction cost
// by construction (LLMCaller has exactly one call site, CallForSummary,
// and the compaction engine is its only caller), so OverheadTotals needs
// no separate Kind field to be recognizable as compaction overhead.
// core/rpc.API.CompactionOverhead (WP12) reads Overhead() directly and
// is what finally gives compactionLLM a caller outside a test.
func (a *LLMCaller) recordOverhead(resp corellm.Response) {
	prev := a.overheadCost.Load()
	if prev == nil {
		prev = &OverheadTotals{}
	}
	// Copy-on-write so concurrent readers see a consistent snapshot.
	next := *prev
	next.Calls++
	next.InputTokens += resp.Usage.InputTokens
	next.OutputTokens += resp.Usage.OutputTokens
	if resp.Cost.Indeterminate {
		next.IndeterminateCalls++
	} else {
		next.Total += resp.Cost.Total
		if next.Currency == "" && resp.Cost.Currency != "" {
			next.Currency = resp.Cost.Currency
		}
	}
	a.overheadCost.Store(&next)
	logging.L().Debug("compaction.cost",
		"kind", cost.KindCompaction,
		"call_total", resp.Cost.Total,
		"running_total", next.Total,
		"input_tokens", resp.Usage.InputTokens,
		"output_tokens", resp.Usage.OutputTokens,
	)
}

// Overhead returns the running compaction-overhead tally. Safe to call
// from any goroutine; returns a copy so callers cannot mutate the
// underlying counter.
func (a *LLMCaller) Overhead() OverheadTotals {
	if a == nil {
		return OverheadTotals{}
	}
	p := a.overheadCost.Load()
	if p == nil {
		return OverheadTotals{}
	}
	return *p
}

// flattenContentText concatenates every text-typed block in declaration
// order, separating multiple blocks with double-newlines so a model
// that returns its answer in two text blocks isn't lossy.
func flattenContentText(blocks []corellm.ContentBlock) string {
	var b []byte
	first := true
	for _, blk := range blocks {
		if blk.Type != "" && blk.Type != "text" {
			continue
		}
		if blk.Text == "" {
			continue
		}
		if !first {
			b = append(b, '\n', '\n')
		}
		first = false
		b = append(b, blk.Text...)
	}
	return string(b)
}
