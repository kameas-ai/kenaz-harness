package llm

import (
	"bytes"
	"sort"
	"strings"
	"sync/atomic"
)

// Prompt caching (tool-context-budget-01TCBUD01 WP05, spec §2.4 / FR-C1-C2).
//
// A provider prompt cache reuses the longest prefix of a request that is
// byte-identical to an earlier one. The harness keeps that prefix stable by:
//
//   - sending tools in one deterministic order (OrderTools);
//   - keeping per-call material out of GenerationRequest.System and in
//     GenerationRequest.SystemVolatile, which adapters place after the
//     cache marker;
//   - marking the end of the stable prefix with cache_control for the
//     providers that take an explicit marker (SupportsPromptCache).

// OrderTools returns specs in the order every request sends them. The
// order is a pure function of the set: the same tools in any input order
// produce the same output order, so an unchanged tool set serialises to
// the same bytes on every call. Today the order is by Name; the request
// builder's tiered segments (hot, pinned, activated) replace it here.
func OrderTools(specs []ToolSpec) []ToolSpec {
	if len(specs) == 0 {
		return specs
	}
	out := make([]ToolSpec, len(specs))
	copy(out, specs)
	sort.SliceStable(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}

// CacheMarkerToolIndex is the index into req.Tools of the tool that ends
// the stable tool segment and carries the cache marker, or -1 for none.
// CacheStableTools == 0 means every tool is stable; a negative value
// means no tool is.
func CacheMarkerToolIndex(req GenerationRequest) int {
	n := len(req.Tools)
	switch {
	case n == 0 || req.CacheStableTools < 0:
		return -1
	case req.CacheStableTools == 0 || req.CacheStableTools >= n:
		return n - 1
	default:
		return req.CacheStableTools - 1
	}
}

// FullSystem is the whole system prompt: System, then SystemVolatile,
// separated by a blank line; an empty part contributes nothing.
func (r GenerationRequest) FullSystem() string {
	stable := strings.TrimSpace(r.System)
	volatile := strings.TrimSpace(r.SystemVolatile)
	switch {
	case volatile == "":
		return r.System
	case stable == "":
		return volatile
	default:
		return stable + "\n\n" + volatile
	}
}

// FoldSystemSegments returns req with SystemVolatile appended to System,
// for an adapter that sends one system string.
func FoldSystemSegments(req GenerationRequest) GenerationRequest {
	if req.SystemVolatile == "" {
		return req
	}
	req.System = req.FullSystem()
	req.SystemVolatile = ""
	return req
}

// SystemSegmentsAdapter is implemented by adapters that place
// GenerationRequest.SystemVolatile themselves. The registry folds
// SystemVolatile into System for every other adapter before dispatch.
type SystemSegmentsAdapter interface {
	SendsSystemSegments() bool
}

// IsAnthropicFamilyModel reports whether an OpenRouter model id routes to
// an Anthropic model: "anthropic/…" or the "~anthropic/…" alias form.
func IsAnthropicFamilyModel(model string) bool {
	m := strings.ToLower(strings.TrimSpace(model))
	return strings.HasPrefix(m, "anthropic/") || strings.HasPrefix(m, "~anthropic/")
}

// SupportsPromptCache is the curated capability table for explicit
// cache_control markers: Anthropic's Messages API for Claude models, and
// OpenRouter for Anthropic-family models (OpenRouter passes cache_control
// through to Anthropic). Every other (provider, model) pair is false and
// its request carries no marker.
func SupportsPromptCache(providerKind, model string) bool {
	switch providerKind {
	case "anthropic":
		return strings.HasPrefix(strings.ToLower(strings.TrimSpace(model)), "claude")
	case "openrouter":
		return IsAnthropicFamilyModel(model)
	default:
		return false
	}
}

// PromptCacheLevel is how much cache marking an adapter still sends after
// provider rejections.
type PromptCacheLevel int32

const (
	// CacheMarkAll marks the system block and the last stable tool.
	CacheMarkAll PromptCacheLevel = iota
	// CacheMarkSystemOnly marks the system block only; set after a
	// rejection of a request that carried a tool marker.
	CacheMarkSystemOnly
	// CacheMarkNone sends no marker; set after a rejection of a request
	// that carried only the system marker.
	CacheMarkNone
)

// PromptCacheGuard is one adapter's degrade state for cache markers. It
// only moves towards CacheMarkNone, for the life of the process. The zero
// value is CacheMarkAll and is safe for concurrent use.
type PromptCacheGuard struct {
	level atomic.Int32
}

// Level is the current marking level.
func (g *PromptCacheGuard) Level() PromptCacheLevel {
	if g == nil {
		return CacheMarkNone
	}
	return PromptCacheLevel(g.level.Load())
}

// Degrade records that a request was rejected over cache_control while
// carrying the markers of level `sent`, and moves the guard to at least
// the next level down. It returns the level to resend at and whether this
// call moved the guard (so the caller logs the change once).
func (g *PromptCacheGuard) Degrade(sent PromptCacheLevel) (PromptCacheLevel, bool) {
	to := sent + 1
	if to > CacheMarkNone {
		to = CacheMarkNone
	}
	if g == nil {
		return CacheMarkNone, false
	}
	for {
		cur := g.level.Load()
		if PromptCacheLevel(cur) >= to {
			return PromptCacheLevel(cur), false
		}
		if g.level.CompareAndSwap(cur, int32(to)) {
			return to, true
		}
	}
}

// SentCacheLevel is the level whose markers a request built at level
// actually carries: a request with no stable tool carries no tool marker,
// so at CacheMarkAll it carried only CacheMarkSystemOnly's.
func SentCacheLevel(req GenerationRequest, level PromptCacheLevel) PromptCacheLevel {
	if level == CacheMarkAll && CacheMarkerToolIndex(req) < 0 {
		return CacheMarkSystemOnly
	}
	return level
}

// IsCacheControlRejection reports whether a provider error response is a
// request rejection that names cache_control.
func IsCacheControlRejection(status int, body []byte) bool {
	return status == 400 && bytes.Contains(bytes.ToLower(body), []byte("cache_control"))
}

// EphemeralCacheControl is the cache_control value both marked providers
// accept.
func EphemeralCacheControl() map[string]any {
	return map[string]any{"type": "ephemeral"}
}
