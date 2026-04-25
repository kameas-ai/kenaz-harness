// Package retry implements WP05: provider-agnostic retry middleware
// with exponential backoff and full jitter (FR-016 / FR-017).
package retry

import (
	llm "github.com/sigil-tech/kaneaz-harness/core/llm"
)

// Policy is a usable RetryPolicy with sensible defaults filled in.
type Policy struct {
	MaxAttempts int
	BaseMS      int
	MaxMS       int
	Jitter      string // "full" | "none"
}

// FromLLM coerces an llm.RetryPolicy (possibly partially zero) into a
// fully-defaulted Policy. The defaults match plan §9 / spec OQ-3.
func FromLLM(p *llm.RetryPolicy) Policy {
	out := Policy{MaxAttempts: 3, BaseMS: 250, MaxMS: 5000, Jitter: "full"}
	if p == nil {
		return out
	}
	if p.MaxAttempts > 0 {
		out.MaxAttempts = p.MaxAttempts
	}
	if p.BaseMS > 0 {
		out.BaseMS = p.BaseMS
	}
	if p.MaxMS > 0 {
		out.MaxMS = p.MaxMS
	}
	if p.Jitter != "" {
		out.Jitter = p.Jitter
	}
	return out
}
