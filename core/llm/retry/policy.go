// Package retry implements WP05: provider-agnostic retry middleware
// with exponential backoff and full jitter (FR-016 / FR-017).
package retry

import (
	"time"

	llm "github.com/kameas-ai/kenaz-harness/core/llm"
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

// StreamPolicyFromLLM converts a profile-resolved llm.RetryPolicy (as
// carried on llm.ProviderProfile.Retry / GenerationRequest.RetryOverride)
// into a StreamPolicy for RetryStream — the stream-open / mid-stream
// retry wrapper the chat live-path (chat.LLMProviderAdapter.Generate)
// wraps around registry.Stream. This is a distinct layer from the
// registry's own internal RetryMiddleware (which already consumes
// FromLLM directly); StreamPolicyFromLLM lets that same per-profile
// policy also drive the chat-adapter's stream-open retry instead of a
// hardcoded literal (model-request-path-live-01PMDL01 WP02).
//
// p may be nil — FromLLM's own defaults apply (3 attempts, 250ms base,
// full jitter), matching DefaultRetryPolicy so the "no profile override"
// case stays consistent with the rest of the retry system rather than a
// separately-hardcoded literal.
//
// StreamPolicy has no equivalent of RetryPolicy.MaxMS (a backoff cap);
// RetryStream's backoff grows unbounded across MaxAttempts today, so
// MaxMS is intentionally not translated here — adding a cap to
// StreamPolicy is a separate, larger change than this conversion.
func StreamPolicyFromLLM(p *llm.RetryPolicy) StreamPolicy {
	resolved := FromLLM(p)
	var jitterPct float64
	if resolved.Jitter == "full" {
		jitterPct = 0.10
	}
	return StreamPolicy{
		MaxAttempts: resolved.MaxAttempts,
		BaseDelay:   time.Duration(resolved.BaseMS) * time.Millisecond,
		JitterPct:   jitterPct,
	}
}
