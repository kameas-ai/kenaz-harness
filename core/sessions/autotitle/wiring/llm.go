// Package wiring adapts core/llm/registry onto the autotitle.LLMCaller
// interface. It is separated from the autotitle package so that
// core/sessions/autotitle never imports core/llm/registry directly
// (DIRECTIVE_001 / tasks.md WP02 constraint).
package wiring

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync/atomic"

	autotitle "github.com/kameas-ai/kenaz-harness/core/sessions/autotitle"

	corellm "github.com/kameas-ai/kenaz-harness/core/llm"
	"github.com/kameas-ai/kenaz-harness/core/llm/cost"
	"github.com/kameas-ai/kenaz-harness/core/logging"
)

// LLMRegistry is the narrow slice of core/llm/registry.Registry that
// the adapter requires. Using an interface here allows tests to inject
// a fake registry without pulling in the full registry package.
type LLMRegistry interface {
	Stream(ctx context.Context, req corellm.GenerationRequest) (corellm.Stream, error)
}

// ProfileResolver maps a (profileID, modelOverride) pair to a concrete
// registry profile-id and model string. Returning ok=false causes the
// call to fail with a typed error, matching the compaction wiring
// pattern (core/compaction/wiring/llm.go::resolveProfile).
type ProfileResolver func(ctx context.Context, profileID, modelOverride string) (resolvedProfileID, resolvedModel string, ok bool)

// OverheadTotals is the running tally of auto-title LLM cost. The
// struct mirrors core/compaction/wiring.OverheadTotals so the rpc
// layer can surface it in the same per-session cost panel.
type OverheadTotals struct {
	// Total is the sum of every auto-title call's reducer-derived cost.
	Total float64 `json:"total"`
	// Currency is the dominant currency code (typically "USD").
	Currency string `json:"currency,omitempty"`
	// Calls is the total number of auto-title LLM calls.
	Calls int `json:"calls"`
	// IndeterminateCalls counts calls whose cost was Indeterminate.
	IndeterminateCalls int `json:"indeterminate_calls"`
	// InputTokens / OutputTokens accumulate raw usage figures.
	InputTokens  int `json:"input_tokens"`
	OutputTokens int `json:"output_tokens"`
}

// LLMCaller adapts the registry's streaming surface onto the
// autotitle.LLMCaller interface. The adapter:
//
//   - Resolves a registry profile id via the caller-supplied ProfileResolver.
//   - Builds a non-streaming GenerationRequest (single user turn with the
//     system prompt and user prompt).
//   - Drains the stream and aggregates the final text response.
//   - Tags every call with cost.KindAutoTitle so the per-session cost
//     view can break out auto-title overhead (plan §2.8).
//
// The adapter is intentionally narrow: all provider concerns (capability
// gate, policy guard, retry, audit, cost reducer) live in the registry
// pipeline; this file only translates value shapes.
type LLMCaller struct {
	reg      LLMRegistry
	resolver ProfileResolver
	// profileID is the default profile to use when the resolver is nil.
	profileID     string
	modelOverride string
	overhead      atomic.Pointer[OverheadTotals]
}

// Option tunes an LLMCaller at construction time.
type Option func(*LLMCaller)

// WithProfileResolver installs a custom resolver for (profileID, model)
// → registry profile id. Overrides the default passthrough behaviour.
func WithProfileResolver(fn ProfileResolver) Option {
	return func(c *LLMCaller) { c.resolver = fn }
}

// WithProfileID sets the default profile id used when no resolver is
// wired or when the resolver falls back to a passthrough.
func WithProfileID(id, model string) Option {
	return func(c *LLMCaller) {
		c.profileID = id
		c.modelOverride = model
	}
}

// NewLLMCaller wraps reg with the given options. Returns nil when reg
// is nil; callers must nil-check before binding to autotitle.Generator.
func NewLLMCaller(reg LLMRegistry, opts ...Option) *LLMCaller {
	if reg == nil {
		return nil
	}
	c := &LLMCaller{reg: reg}
	c.overhead.Store(&OverheadTotals{})
	for _, o := range opts {
		o(c)
	}
	return c
}

// Call satisfies autotitle.LLMCaller. It opens a streaming generation,
// drains the events channel, and returns the concatenated text content.
// Every call is tagged with cost.KindAutoTitle in the debug log so the
// overhead tally can be tracked without re-parsing audit events.
func (c *LLMCaller) Call(ctx context.Context, systemPrompt, userPrompt string) (text string, inputTokens, outputTokens int, err error) {
	if c == nil || c.reg == nil {
		return "", 0, 0, errors.New("autotitle.wiring: nil llm registry")
	}

	profileID, model, ok := c.resolveProfile(ctx)
	if !ok {
		return "", 0, 0, fmt.Errorf("autotitle.wiring: no profile resolved (id=%q)", c.profileID)
	}

	req := corellm.GenerationRequest{
		ProfileID: profileID,
		Model:     model,
		System:    systemPrompt,
		Messages: []corellm.Message{
			{
				Role: corellm.Role("user"),
				Content: []corellm.ContentBlock{
					{Type: "text", Text: userPrompt},
				},
			},
		},
		// Auto-title calls carry no tool catalog, no caching, no reasoning
		// extensions — keep the surface minimal.
	}

	stream, serr := c.reg.Stream(ctx, req)
	if serr != nil {
		return "", 0, 0, serr
	}

	// Drain the streaming events. We intentionally don't fan deltas
	// anywhere — auto-title is a synchronous backend call.
	for range stream.Events() {
	}

	resp, ferr := stream.Final()
	if ferr != nil {
		return "", 0, 0, ferr
	}

	out := flattenContentText(resp.Content)
	c.recordOverhead(resp)
	return out, resp.Usage.InputTokens, resp.Usage.OutputTokens, nil
}

// Overhead returns the running auto-title overhead tally. Safe to call
// from any goroutine; returns a copy so callers cannot mutate the counter.
func (c *LLMCaller) Overhead() OverheadTotals {
	if c == nil {
		return OverheadTotals{}
	}
	p := c.overhead.Load()
	if p == nil {
		return OverheadTotals{}
	}
	return *p
}

// resolveProfile fans the request through the optional resolver closure,
// falling back to the configured profileID/model pair when no resolver
// is wired.
func (c *LLMCaller) resolveProfile(ctx context.Context) (profileID, model string, ok bool) {
	if c.resolver != nil {
		return c.resolver(ctx, c.profileID, c.modelOverride)
	}
	if c.profileID == "" {
		return "", "", false
	}
	return c.profileID, c.modelOverride, true
}

// recordOverhead folds the response's cost + usage into the running
// OverheadTotals. Tagged with cost.KindAutoTitle in the log so downstream
// dashboards can correlate the spend (plan §2.8).
func (c *LLMCaller) recordOverhead(resp corellm.Response) {
	prev := c.overhead.Load()
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
	c.overhead.Store(&next)
	logging.L().Debug("autotitle.cost",
		"kind", cost.KindAutoTitle,
		"call_total", resp.Cost.Total,
		"running_total", next.Total,
		"input_tokens", resp.Usage.InputTokens,
		"output_tokens", resp.Usage.OutputTokens,
	)
}

// flattenContentText concatenates every text-typed content block in
// declaration order, separating multiple blocks with double-newlines.
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
	return strings.TrimSpace(string(b))
}

// Ensure LLMCaller satisfies the autotitle.LLMCaller interface at compile time.
var _ autotitle.LLMCaller = (*LLMCaller)(nil)
