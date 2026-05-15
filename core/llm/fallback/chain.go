// Package fallback implements per-turn model-fallback routing for the
// kenaz-harness connector layer (model-fallback-routing-01NDFSEX04).
//
// A FallbackChain is an ordered list of (provider, model, param overrides)
// entries each annotated with trigger conditions. When the connector's
// primary adapter call fails with a matching error, it walks the chain
// entry-by-entry, re-issuing the request under the next entry's
// (provider, model, overrides) until one succeeds or the whole-chain
// attempt ceiling (5) is reached.
//
// Chain resolution hierarchy (high → low priority):
//  1. Graph-node attr:  model/escalate node's fallback_chain_id
//  2. Session default:  session.FallbackChainID
//  3. App default:      settings.FallbackChainID
package fallback

import "fmt"

// TriggerCondition is the set of error classes that cause a chain
// entry to be attempted. Multiple conditions on one entry are OR'd;
// TriggerErrorAny is a catch-all.
type TriggerCondition string

const (
	// TriggerError5xx fires on any 5xx HTTP response from the provider,
	// including status 529 (Anthropic overloaded) and 503 (upstream).
	TriggerError5xx TriggerCondition = "error_5xx"

	// TriggerError429 fires on HTTP 429 (Too Many Requests / rate-limited).
	TriggerError429 TriggerCondition = "error_429"

	// TriggerErrorAuthFailed fires when the primary returns ErrAuth (401/403).
	TriggerErrorAuthFailed TriggerCondition = "error_auth_failed"

	// TriggerErrorContextOverflow fires when the request's token count
	// exceeds the model's context window. Adapters surface this as
	// ErrInvalidRequest with Status 400 and a message containing
	// "context" / "token" / "length" keywords (heuristic).
	TriggerErrorContextOverflow TriggerCondition = "error_context_overflow"

	// TriggerErrorSafetyBlock fires when the provider returns a safety /
	// content-policy rejection. Adapters surface this as ErrInvalidRequest
	// with Status 400 and a safety-block message pattern.
	TriggerErrorSafetyBlock TriggerCondition = "error_safety_block"

	// TriggerErrorAny matches any non-nil error from the primary adapter.
	// Use this as a last-resort catch-all entry.
	TriggerErrorAny TriggerCondition = "error_any"
)

// AllTriggerConditions is the canonical ordered list (used for validation).
var AllTriggerConditions = []TriggerCondition{
	TriggerError5xx,
	TriggerError429,
	TriggerErrorAuthFailed,
	TriggerErrorContextOverflow,
	TriggerErrorSafetyBlock,
	TriggerErrorAny,
}

// IsKnown reports whether t is a known TriggerCondition.
func (t TriggerCondition) IsKnown() bool {
	for _, c := range AllTriggerConditions {
		if t == c {
			return true
		}
	}
	return false
}

// MaxWholeChainAttempts is the hard ceiling on total entries attempted
// per turn. Chains with more matching entries are truncated at this value
// to prevent degenerate infinite-escalation loops.
const MaxWholeChainAttempts = 5

// ChainEntry describes one fallback hop.
type ChainEntry struct {
	// ProviderID is the profile identifier of the target provider (e.g.
	// "openrouter", "bedrock-claude"). Must match a registered provider.
	ProviderID string `json:"provider_id" yaml:"provider_id"`

	// Model is the model identifier to use for this hop. When empty,
	// the provider's default model is used.
	Model string `json:"model,omitempty" yaml:"model,omitempty"`

	// ParamOverrides is a free-form map of GenerationRequest parameter
	// overrides applied on top of the original request's params for
	// this hop. Common keys: "temperature", "max_tokens", "system".
	// Overrides are shallow-merged (map[string]any) before dispatch.
	ParamOverrides map[string]any `json:"param_overrides,omitempty" yaml:"param_overrides,omitempty"`

	// Triggers lists the error conditions that activate this entry.
	// An empty slice means this entry is never triggered (inert).
	// Use TriggerErrorAny as a catch-all.
	Triggers []TriggerCondition `json:"triggers" yaml:"triggers"`

	// MaxAttempts caps how many times this single entry may be retried
	// within the chain walk (default 1 when zero; the global
	// MaxWholeChainAttempts ceiling applies regardless).
	MaxAttempts int `json:"max_attempts,omitempty" yaml:"max_attempts,omitempty"`
}

// Chain is an ordered sequence of ChainEntry hops with identifying
// metadata. Chains are persisted as YAML files under
// <DataDir>/llm/fallback_chains/ or embedded in the binary bundle.
type Chain struct {
	// ID is the stable identifier for this chain (URL-safe slug, e.g.
	// "anthropic-then-openrouter"). Used as the file stem when persisted.
	ID string `json:"id" yaml:"id"`

	// Name is the human-readable display name shown in the LLM Routing panel.
	Name string `json:"name" yaml:"name"`

	// Description is an optional operator-facing note explaining when to
	// apply this chain (e.g. "Use when Anthropic is rate-limited or down").
	Description string `json:"description,omitempty" yaml:"description,omitempty"`

	// Entries is the ordered list of fallback hops. The first matching
	// entry wins per attempt; non-matching entries are skipped.
	Entries []ChainEntry `json:"entries" yaml:"entries"`
}

// Validate returns an error if the chain is not structurally sound.
// It does NOT validate that provider IDs correspond to registered
// providers — that check lives at dispatch time in the connector.
func (c *Chain) Validate() error {
	if c.ID == "" {
		return fmt.Errorf("fallback chain: id is required")
	}
	if c.Name == "" {
		return fmt.Errorf("fallback chain %q: name is required", c.ID)
	}
	for i, e := range c.Entries {
		if e.ProviderID == "" {
			return fmt.Errorf("fallback chain %q entry[%d]: provider_id is required", c.ID, i)
		}
		if len(e.Triggers) == 0 {
			return fmt.Errorf("fallback chain %q entry[%d]: triggers must not be empty", c.ID, i)
		}
		for _, t := range e.Triggers {
			if !t.IsKnown() {
				return fmt.Errorf("fallback chain %q entry[%d]: unknown trigger %q", c.ID, i, t)
			}
		}
		maxAttempts := e.MaxAttempts
		if maxAttempts == 0 {
			maxAttempts = 1
		}
		if maxAttempts > MaxWholeChainAttempts {
			return fmt.Errorf(
				"fallback chain %q entry[%d]: max_attempts %d exceeds global ceiling %d",
				c.ID, i, maxAttempts, MaxWholeChainAttempts,
			)
		}
	}
	return nil
}

// ErrFallbackExhausted is returned by the connector retry loop when
// the whole-chain attempt ceiling is reached without a successful
// response. LastErr wraps the final adapter error for caller inspection.
type ErrFallbackExhausted struct {
	ChainID  string
	Attempts int
	LastErr  error
}

func (e *ErrFallbackExhausted) Error() string {
	return fmt.Sprintf(
		"llm: fallback chain %q exhausted after %d attempt(s): %v",
		e.ChainID, e.Attempts, e.LastErr,
	)
}

func (e *ErrFallbackExhausted) Unwrap() error { return e.LastErr }
