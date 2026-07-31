package registry

// knobParamKeys is the set of req.Params map keys that mirror a
// llm.RequestKnobs field policed by capabilities.KnobPolicy.Apply. Every
// production caller wires per-node sampling knobs onto GenerationRequest.
// Params (see llm_provider_adapter.go), not the typed Knobs field — so
// this is the seam that widens KnobPolicy's coverage from "callers that
// happen to set req.Knobs" (nobody, in production) to "every caller,
// via the map channel every adapter actually reads."
//
// Deliberately excluded: max_tokens/temperature (no corresponding
// llm.ProviderCapabilities flag — KnobPolicy.Apply never inspects them,
// so routing them through would be a no-op) and stop_sequences/reasoning
// (carried on typed GenerationRequest fields with different shapes than
// RequestKnobs' Reasoning/StopSequences — reasoning in particular is
// already gated one layer up by the CapReasoning capability check, and
// adapters such as azure/adapter.go perform their own reasoning-style
// mapping; folding it through KnobPolicy's stricter style-mismatch logic
// risks refusing requests that today succeed).
import (
	llm "github.com/kameas-ai/kenaz-harness/core/llm"
)

// knobsFromParams extracts the KnobPolicy-relevant fields from req.Params
// into a synthetic RequestKnobs view. The returned bool set records which
// keys were present with a recognised type, so the caller can write back
// only the keys it actually inspected (leaving everything else in Params
// untouched).
func knobsFromParams(params map[string]any) (*llm.RequestKnobs, map[string]bool) {
	if len(params) == 0 {
		return nil, nil
	}
	var k llm.RequestKnobs
	had := map[string]bool{}

	if v, ok := params["seed"]; ok {
		if i, ok := numToInt(v); ok {
			k.Seed = &i
			had["seed"] = true
		}
	}
	if v, ok := params["top_k"]; ok {
		if i, ok := numToInt(v); ok {
			k.TopK = &i
			had["top_k"] = true
		}
	}
	if v, ok := params["top_p"]; ok {
		if f, ok := numToFloat(v); ok {
			k.TopP = &f
			had["top_p"] = true
		}
	}
	if v, ok := params["frequency_penalty"]; ok {
		if f, ok := numToFloat(v); ok {
			k.FrequencyPenalty = &f
			had["frequency_penalty"] = true
		}
	}
	if v, ok := params["presence_penalty"]; ok {
		if f, ok := numToFloat(v); ok {
			k.PresencePenalty = &f
			had["presence_penalty"] = true
		}
	}
	if v, ok := params["parallel_tool_calls"]; ok {
		if b, ok := v.(bool); ok {
			k.ParallelToolCalls = &b
			had["parallel_tool_calls"] = true
		}
	}

	if len(had) == 0 {
		return nil, nil
	}
	return &k, had
}

// mergeRequestKnobs combines the caller's explicit req.Knobs (if any) with
// the synthetic view derived from req.Params. Precedence: a field already
// set on existing (req.Knobs) wins over the Params-derived value for that
// same field — req.Knobs is the deliberate, typed configuration surface a
// caller opts into explicitly, so the wider best-effort Params channel
// must not silently clobber it. Neither input is mutated.
func mergeRequestKnobs(existing, fromParams *llm.RequestKnobs) *llm.RequestKnobs {
	if existing == nil && fromParams == nil {
		return nil
	}
	if existing == nil {
		c := *fromParams
		return &c
	}
	if fromParams == nil {
		c := *existing
		return &c
	}
	merged := *existing
	if merged.Seed == nil {
		merged.Seed = fromParams.Seed
	}
	if merged.TopK == nil {
		merged.TopK = fromParams.TopK
	}
	if merged.TopP == nil {
		merged.TopP = fromParams.TopP
	}
	if merged.FrequencyPenalty == nil {
		merged.FrequencyPenalty = fromParams.FrequencyPenalty
	}
	if merged.PresencePenalty == nil {
		merged.PresencePenalty = fromParams.PresencePenalty
	}
	if merged.ParallelToolCalls == nil {
		merged.ParallelToolCalls = fromParams.ParallelToolCalls
	}
	return &merged
}

// writeKnobsToParams returns a copy of params with the KnobPolicy-governed
// keys (those present in hadKeys) reconciled against cleaned: a key whose
// cleaned field is nil (dropped, or never set) is removed from the map;
// otherwise the map is updated to the cleaned value (covering the
// precedence edge case where req.Knobs' value — not the original Params
// value — won the merge). Every other key in params is preserved
// unchanged: Params is a general-purpose map and this function only
// governs the six sampling-knob keys KnobPolicy inspects.
func writeKnobsToParams(params map[string]any, hadKeys map[string]bool, cleaned *llm.RequestKnobs) map[string]any {
	if len(hadKeys) == 0 || cleaned == nil {
		return params
	}
	out := make(map[string]any, len(params))
	for k, v := range params {
		out[k] = v
	}
	set := func(key string, present bool, val any, isNil bool) {
		if !present {
			return
		}
		if isNil {
			delete(out, key)
			return
		}
		out[key] = val
	}
	set("seed", hadKeys["seed"], derefInt(cleaned.Seed), cleaned.Seed == nil)
	set("top_k", hadKeys["top_k"], derefInt(cleaned.TopK), cleaned.TopK == nil)
	set("top_p", hadKeys["top_p"], derefFloat(cleaned.TopP), cleaned.TopP == nil)
	set("frequency_penalty", hadKeys["frequency_penalty"], derefFloat(cleaned.FrequencyPenalty), cleaned.FrequencyPenalty == nil)
	set("presence_penalty", hadKeys["presence_penalty"], derefFloat(cleaned.PresencePenalty), cleaned.PresencePenalty == nil)
	set("parallel_tool_calls", hadKeys["parallel_tool_calls"], derefBool(cleaned.ParallelToolCalls), cleaned.ParallelToolCalls == nil)
	return out
}

// numToInt accepts int, int64, and float64 (JSON-decoded numbers land as
// float64; callers that build Params in Go code may use int directly).
func numToInt(v any) (int, bool) {
	switch n := v.(type) {
	case int:
		return n, true
	case int64:
		return int(n), true
	case float64:
		return int(n), true
	default:
		return 0, false
	}
}

// numToFloat accepts float64, float32, int, and int64.
func numToFloat(v any) (float64, bool) {
	switch n := v.(type) {
	case float64:
		return n, true
	case float32:
		return float64(n), true
	case int:
		return float64(n), true
	case int64:
		return float64(n), true
	default:
		return 0, false
	}
}

func derefInt(p *int) any {
	if p == nil {
		return nil
	}
	return *p
}

func derefFloat(p *float64) any {
	if p == nil {
		return nil
	}
	return *p
}

func derefBool(p *bool) any {
	if p == nil {
		return nil
	}
	return *p
}
