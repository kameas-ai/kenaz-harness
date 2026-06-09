package fallback

import (
	"context"
	"time"

	"github.com/kameas-ai/kenaz-harness/core/llm"
)

// Resolver maps a session / node context to the active Chain (if any).
// Implementing the 3-level hierarchy (node attr → session default → app
// default) is the caller's responsibility; the fallback package just calls
// Resolve and walks whatever chain comes back.
//
// Resolve may return (nil, nil) to indicate "no fallback configured".
type Resolver interface {
	Resolve(ctx context.Context, sessionID string) (*Chain, error)
}

// Streamer is the narrow adapter contract that the retry loop calls.
// The *Registry satisfies this interface via its Stream method.
type Streamer interface {
	Stream(ctx context.Context, req llm.GenerationRequest) (llm.Stream, error)
}

// FallbackAttemptedEvent is emitted (via AttemptHook) for each hop the
// retry loop executes. The event is also broadcast on the broker topic
// "llm:fallback-attempted" via the caller-supplied BrokerPublish hook.
//
// Privacy invariant: this struct MUST NOT contain request bytes, prompt
// text, credential material, or response content.
type FallbackAttemptedEvent struct {
	SessionID string `json:"session_id,omitempty"`
	// From is the (profileID, model) of the failed primary call.
	FromProfile string `json:"from_profile"`
	FromModel   string `json:"from_model"`
	// To is the (profileID, model) the hop resolves to.
	ToProfile string `json:"to_profile"`
	ToModel   string `json:"to_model"`
	// Reason is the TriggerCondition string that matched.
	Reason  string    `json:"reason"`
	Attempt int       `json:"attempt"`
	At      time.Time `json:"at"`
}

// FallbackBlockedEvent is emitted when Cedar denies the fallback hop.
type FallbackBlockedEvent struct {
	SessionID string    `json:"session_id,omitempty"`
	ChainID   string    `json:"chain_id"`
	Reason    string    `json:"reason"`
	At        time.Time `json:"at"`
}

// Runner wraps a Streamer with a per-turn fallback retry loop.
// Construct one via NewRunner and inject it wherever the current call
// site calls streamer.Stream(ctx, req).
type Runner struct {
	streamer      Streamer
	resolver      Resolver
	checkPolicy   func(ctx context.Context, chainID string) error
	onAttempt     func(ctx context.Context, ev FallbackAttemptedEvent)
	onBlocked     func(ctx context.Context, ev FallbackBlockedEvent)
}

// RunnerOption configures a Runner.
type RunnerOption func(*Runner)

// WithPolicyCheck wires the Cedar gate. fn should call cedar.CheckLLMFallback
// and return ErrPolicyDenied on deny; nil on allow.
func WithPolicyCheck(fn func(ctx context.Context, chainID string) error) RunnerOption {
	return func(r *Runner) { r.checkPolicy = fn }
}

// WithAttemptHook wires the audit + broker hook called on every fallback hop.
func WithAttemptHook(fn func(ctx context.Context, ev FallbackAttemptedEvent)) RunnerOption {
	return func(r *Runner) { r.onAttempt = fn }
}

// WithBlockedHook wires the audit hook called when Cedar denies a hop.
func WithBlockedHook(fn func(ctx context.Context, ev FallbackBlockedEvent)) RunnerOption {
	return func(r *Runner) { r.onBlocked = fn }
}

// NewRunner constructs a Runner. opts may add policy/audit hooks.
func NewRunner(streamer Streamer, resolver Resolver, opts ...RunnerOption) *Runner {
	r := &Runner{
		streamer: streamer,
		resolver: resolver,
	}
	for _, o := range opts {
		o(r)
	}
	return r
}

// Stream executes the primary call and, on failure, walks the fallback chain.
//
// Algorithm:
//  1. Call streamer.Stream(ctx, req) as primary.
//  2. On error: resolve the active chain (if any).
//  3. Walk entries in order; Match(err, entry.Triggers).
//  4. On match: Cedar check → on pass, reissue with entry's (provider, model,
//     param overrides). On Cedar deny → return primary error (fail-closed).
//  5. Repeat until a hop succeeds, chain is exhausted, or the
//     whole-chain ceiling (5) is reached.
//  6. On ceiling / exhaustion: return ErrFallbackExhausted wrapping last err.
func (r *Runner) Stream(ctx context.Context, req llm.GenerationRequest) (llm.Stream, error) {
	// Primary call.
	stream, err := r.streamer.Stream(ctx, req)
	if err == nil {
		return stream, nil
	}

	// No error — nothing to fall back from.
	primaryErr := err

	// Resolve active chain.
	if r.resolver == nil {
		return nil, primaryErr
	}
	chain, resolveErr := r.resolver.Resolve(ctx, req.SessionID)
	if resolveErr != nil || chain == nil {
		return nil, primaryErr
	}

	// Walk entries.
	totalAttempts := 0
	lastErr := primaryErr

	for _, entry := range chain.Entries {
		if totalAttempts >= MaxWholeChainAttempts {
			break
		}

		matched, trigger := Match(lastErr, entry.Triggers)
		if !matched {
			continue
		}

		// Cedar policy check.
		if r.checkPolicy != nil {
			if policyErr := r.checkPolicy(ctx, chain.ID); policyErr != nil {
				// Fail-closed: Cedar deny → surface primary error unchanged.
				if r.onBlocked != nil {
					r.onBlocked(ctx, FallbackBlockedEvent{
						SessionID: req.SessionID,
						ChainID:   chain.ID,
						Reason:    policyErr.Error(),
						At:        time.Now(),
					})
				}
				return nil, primaryErr
			}
		}

		totalAttempts++

		// Build the hop request.
		hopReq := req
		hopReq.ProfileID = entry.ProviderID
		if entry.Model != "" {
			hopReq.Model = entry.Model
		}
		// Apply param overrides (shallow merge into Params).
		if len(entry.ParamOverrides) > 0 {
			merged := make(map[string]any, len(hopReq.Params)+len(entry.ParamOverrides))
			for k, v := range hopReq.Params {
				merged[k] = v
			}
			for k, v := range entry.ParamOverrides {
				merged[k] = v
			}
			hopReq.Params = merged
		}

		// Emit attempt hook.
		if r.onAttempt != nil {
			r.onAttempt(ctx, FallbackAttemptedEvent{
				SessionID:   req.SessionID,
				FromProfile: req.ProfileID,
				FromModel:   req.Model,
				ToProfile:   entry.ProviderID,
				ToModel:     entry.Model,
				Reason:      string(trigger),
				Attempt:     totalAttempts,
				At:          time.Now(),
			})
		}

		// Issue the hop.
		hopStream, hopErr := r.streamer.Stream(ctx, hopReq)
		if hopErr == nil {
			return hopStream, nil
		}
		lastErr = hopErr
	}

	// Chain exhausted or ceiling reached.
	if totalAttempts >= MaxWholeChainAttempts {
		return nil, &ErrFallbackExhausted{
			ChainID:  chain.ID,
			Attempts: totalAttempts,
			LastErr:  lastErr,
		}
	}
	// No entry matched the error (or all entries inert).
	return nil, primaryErr
}
