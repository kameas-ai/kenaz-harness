package capabilities

// Package capabilities — per-knob policy (provider-implementation-uniformity-01KQ8V4F WP08).
//
// KnobPolicy validates a RequestKnobs value against a ProviderCapabilities
// record before any adapter builds its wire request. Unsupported knobs
// return ErrUnsupportedFeature with a user-readable Hint. Knobs that
// have a natural fallback (e.g. TopP silently dropped) are stripped and
// reported as warnings via a Dropped slice.
//
// Usage:
//
//	policy := capabilities.NewKnobPolicy(caps)
//	cleaned, dropped, err := policy.Apply(knobs)
//	if err != nil {
//	    // surface ErrUnsupportedFeature to the user
//	}
//	// use cleaned; log dropped if non-empty

import (
	"fmt"

	llm "github.com/kameas-ai/kenaz-harness/core/llm"
)

// DroppedKnob records a knob that was silently removed because the
// provider does not support it. The adapter may log these at debug
// level; they are NOT surfaced as errors.
type DroppedKnob struct {
	// Name is the knob's identifier (matches the RequestKnobs JSON key,
	// e.g. "seed", "top_k", "frequency_penalty").
	Name string
	// Reason is a brief human-readable explanation, e.g.
	// "provider openai does not support top_k".
	Reason string
}

// KnobPolicy validates and sanitises a RequestKnobs against a
// ProviderCapabilities record. Construct via NewKnobPolicy.
type KnobPolicy struct {
	caps llm.ProviderCapabilities
}

// NewKnobPolicy constructs a KnobPolicy for the given capability record.
func NewKnobPolicy(caps llm.ProviderCapabilities) *KnobPolicy {
	return &KnobPolicy{caps: caps}
}

// Apply validates knobs against the receiver's ProviderCapabilities.
// It returns:
//
//   - cleaned: a copy of knobs with unsupported-but-droppable fields zeroed.
//   - dropped: the list of silently-removed knob names + reasons.
//   - err:     ErrUnsupportedFeature when a knob cannot be silently dropped
//              (e.g. a reasoning config that explicitly targets the wrong
//              provider style, or a feature with no fallback).
//
// Apply never modifies knobs in-place.
func (p *KnobPolicy) Apply(knobs *llm.RequestKnobs) (cleaned *llm.RequestKnobs, dropped []DroppedKnob, err error) {
	if knobs == nil {
		return nil, nil, nil
	}

	// Work on a shallow copy so we don't mutate the caller's value.
	c := *knobs
	cleaned = &c

	// ── Reasoning ──────────────────────────────────────────────────────
	if c.Reasoning != nil {
		drop, e := p.applyReasoning(cleaned)
		if e != nil {
			return nil, nil, e
		}
		if drop != nil {
			dropped = append(dropped, *drop)
		}
	}

	// ── Seed ───────────────────────────────────────────────────────────
	if c.Seed != nil && !p.caps.Seed {
		dropped = append(dropped, DroppedKnob{
			Name:   "seed",
			Reason: fmt.Sprintf("provider %s/%s does not support seed", p.caps.Provider, p.caps.Model),
		})
		cleaned.Seed = nil
	}

	// ── TopK ───────────────────────────────────────────────────────────
	if c.TopK != nil && !p.caps.TopK {
		dropped = append(dropped, DroppedKnob{
			Name:   "top_k",
			Reason: fmt.Sprintf("provider %s/%s does not support top_k", p.caps.Provider, p.caps.Model),
		})
		cleaned.TopK = nil
	}

	// ── TopP ───────────────────────────────────────────────────────────
	if c.TopP != nil && !p.caps.TopP {
		dropped = append(dropped, DroppedKnob{
			Name:   "top_p",
			Reason: fmt.Sprintf("provider %s/%s does not support top_p", p.caps.Provider, p.caps.Model),
		})
		cleaned.TopP = nil
	}

	// ── FrequencyPenalty ───────────────────────────────────────────────
	if c.FrequencyPenalty != nil && !p.caps.FrequencyPenalty {
		dropped = append(dropped, DroppedKnob{
			Name:   "frequency_penalty",
			Reason: fmt.Sprintf("provider %s/%s does not support frequency_penalty", p.caps.Provider, p.caps.Model),
		})
		cleaned.FrequencyPenalty = nil
	}

	// ── PresencePenalty ────────────────────────────────────────────────
	if c.PresencePenalty != nil && !p.caps.PresencePenalty {
		dropped = append(dropped, DroppedKnob{
			Name:   "presence_penalty",
			Reason: fmt.Sprintf("provider %s/%s does not support presence_penalty", p.caps.Provider, p.caps.Model),
		})
		cleaned.PresencePenalty = nil
	}

	// ── ParallelToolCalls ──────────────────────────────────────────────
	if c.ParallelToolCalls != nil && !p.caps.ParallelToolCalls {
		dropped = append(dropped, DroppedKnob{
			Name:   "parallel_tool_calls",
			Reason: fmt.Sprintf("provider %s/%s does not support parallel_tool_calls", p.caps.Provider, p.caps.Model),
		})
		cleaned.ParallelToolCalls = nil
	}

	return cleaned, dropped, nil
}

// applyReasoning validates the Reasoning sub-knob against the
// capability record's ReasoningStyle. Returns the knob to clear and a
// DroppedKnob if the config has the wrong style but can be silently
// dropped; returns ErrUnsupportedFeature when the mismatch is
// irreconcilable (e.g. user explicitly set an effort string on a
// token-budget model, which implies they know what they're doing).
func (p *KnobPolicy) applyReasoning(cleaned *llm.RequestKnobs) (*DroppedKnob, error) {
	r := cleaned.Reasoning
	if r == nil {
		return nil, nil
	}

	// Model does not support reasoning at all.
	if !p.caps.Reasoning {
		drop := &DroppedKnob{
			Name: "reasoning",
			Reason: fmt.Sprintf("provider %s/%s does not support reasoning; knob dropped",
				p.caps.Provider, p.caps.Model),
		}
		cleaned.Reasoning = nil
		return drop, nil
	}

	style := p.caps.Reasoning_

	// Validate the supplied ReasoningConfig against the model's style.
	hasEffort := r.OpenAIEffort != ""
	hasBudget := r.AnthropicThinkingBudget > 0

	switch style {
	case llm.ReasoningEffortString:
		// Model only accepts effort strings.
		if hasBudget && !hasEffort {
			return nil, &llm.ErrUnsupportedFeature{
				ModelID: p.caps.Model,
				Feature: "reasoning.anthropic_thinking_budget",
				Hint: fmt.Sprintf(
					"Model %q uses effort strings (low/medium/high/minimal). "+
						"Use /effort low|medium|high|minimal instead of a token budget.",
					p.caps.Model),
			}
		}

	case llm.ReasoningTokenBudget:
		// Model only accepts token budgets.
		if hasEffort && !hasBudget {
			return nil, &llm.ErrUnsupportedFeature{
				ModelID: p.caps.Model,
				Feature: "reasoning.openai_effort",
				Hint: fmt.Sprintf(
					"Model %q uses token budgets, not effort strings. "+
						"Use /effort <tokens> (e.g. /effort 16000) instead.",
					p.caps.Model),
			}
		}

	case llm.ReasoningBoth:
		// Accept either form; nothing to reject.

	default:
		// ReasoningNone — already handled above (p.caps.Reasoning == false).
	}

	return nil, nil
}

