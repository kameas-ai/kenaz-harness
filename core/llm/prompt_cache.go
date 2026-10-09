package llm

import (
	"bytes"
	"sort"
	"strings"
	"sync"
	"sync/atomic"

	"github.com/kameas-ai/kenaz-harness/core/logging"
)

// Prompt caching (spec §2.4, FR-C1-C2).
//
// A provider prompt cache reuses the longest prefix of a request that is
// byte-identical to an earlier one. The prefix stays stable only while:
//
//   - tools are sent in the deterministic order OrderTools produces;
//   - per-call material stays out of GenerationRequest.System and in
//     GenerationRequest.SystemVolatile, which adapters place after the
//     cache marker;
//   - the end of the stable prefix carries cache_control for the
//     providers that take an explicit marker (SupportsPromptCache).

// OrderTools returns the request's tool list and the length of its stable
// (cacheable) leading segment. Order: hot and pinned, each by Name, then
// activated in the order given (most recently used first). Only hot and
// pinned are stable, so an activated tool never enters the prefix however
// it sorts by name. A name already placed by an earlier segment is
// dropped from later ones.
func OrderTools(hot, pinned, activated []ToolSpec) (tools []ToolSpec, stable int) {
	seen := make(map[string]bool, len(hot)+len(pinned)+len(activated))
	take := func(seg []ToolSpec, byName bool) []ToolSpec {
		out := make([]ToolSpec, 0, len(seg))
		for _, t := range seg {
			if seen[t.Name] {
				continue
			}
			seen[t.Name] = true
			out = append(out, t)
		}
		if byName {
			sort.SliceStable(out, func(i, j int) bool { return out[i].Name < out[j].Name })
		}
		return out
	}
	h := take(hot, true)
	p := take(pinned, true)
	a := take(activated, false)
	tools = make([]ToolSpec, 0, len(h)+len(p)+len(a))
	tools = append(append(append(tools, h...), p...), a...)
	return tools, len(h) + len(p)
}

// SetTools sets the request's tools and the CacheStableTools value that
// marks the first `stable` of them, from OrderTools' results.
func (r *GenerationRequest) SetTools(tools []ToolSpec, stable int) {
	r.Tools = tools
	switch {
	case len(tools) == 0:
		r.CacheStableTools = 0
	case stable <= 0:
		r.CacheStableTools = -1
	default:
		r.CacheStableTools = stable
	}
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

// SystemVolatileAdapter is implemented by adapters that place
// GenerationRequest.SystemVolatile themselves. The registry folds
// SystemVolatile into System for every other adapter before dispatch.
type SystemVolatileAdapter interface {
	PlacesSystemVolatile()
}

// IsAnthropicFamilyModel reports whether an OpenRouter model id routes to
// an Anthropic model: "anthropic/…" or the "~anthropic/…" alias form.
func IsAnthropicFamilyModel(model string) bool {
	m := strings.ToLower(strings.TrimSpace(model))
	return strings.HasPrefix(m, "anthropic/") || strings.HasPrefix(m, "~anthropic/")
}

// SupportsPromptCache is the curated table for explicit cache_control
// markers: Anthropic's Messages API for Claude models, and OpenRouter for
// Anthropic-family models (OpenRouter passes cache_control through to
// Anthropic). Every other (provider, model) pair is false and its request
// carries no marker.
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

// PromptCacheLevel is how much cache marking a request carries.
type PromptCacheLevel int32

const (
	// CacheMarkAll marks the system block and the last stable tool.
	CacheMarkAll PromptCacheLevel = iota
	// CacheMarkSystemOnly marks the system block only.
	CacheMarkSystemOnly
	// CacheMarkNone sends no marker.
	CacheMarkNone
)

// String is the log value for a level.
func (l PromptCacheLevel) String() string {
	switch l {
	case CacheMarkAll:
		return "system+tools"
	case CacheMarkSystemOnly:
		return "system"
	default:
		return "none"
	}
}

// PromptCacheGuard is one adapter's degrade state for cache markers,
// keyed by (profile id, model id): a profile is one endpoint + credential,
// so a proxy or one routed model rejecting cache_control leaves every
// other profile and model marking. A key's level only moves towards
// CacheMarkNone, for the life of the process. The zero value marks
// everything and is safe for concurrent use.
type PromptCacheGuard struct {
	levels sync.Map // guardKey -> *atomic.Int32
}

type guardKey struct{ profileID, model string }

func (g *PromptCacheGuard) slot(profileID, model string) *atomic.Int32 {
	v, _ := g.levels.LoadOrStore(guardKey{profileID, model}, new(atomic.Int32))
	return v.(*atomic.Int32)
}

// Level is the current marking level for model on profileID.
func (g *PromptCacheGuard) Level(profileID, model string) PromptCacheLevel {
	if g == nil {
		return CacheMarkNone
	}
	v, ok := g.levels.Load(guardKey{profileID, model})
	if !ok {
		return CacheMarkAll
	}
	return PromptCacheLevel(v.(*atomic.Int32).Load())
}

// Degrade records that a request for (profileID, model) was rejected over
// cache_control while carrying the markers of level `sent`, and moves that
// key to at least the next level down. It returns the level to resend at
// and whether this call moved the guard.
func (g *PromptCacheGuard) Degrade(profileID, model string, sent PromptCacheLevel) (PromptCacheLevel, bool) {
	to := sent + 1
	if to > CacheMarkNone {
		to = CacheMarkNone
	}
	if g == nil {
		return CacheMarkNone, false
	}
	s := g.slot(profileID, model)
	for {
		cur := s.Load()
		if PromptCacheLevel(cur) >= to {
			return PromptCacheLevel(cur), false
		}
		if s.CompareAndSwap(cur, int32(to)) {
			return to, true
		}
	}
}

// Rejected decides what an adapter does with a failed response to a
// request for (profileID, model) built at level. When the response is a
// cache_control rejection of a marked request it degrades that key, logs
// llm.prompt_cache.unsupported once per step, and returns the level to
// resend at with retry=true; otherwise retry is false and the caller
// returns the error as usual.
func (g *PromptCacheGuard) Rejected(provider, profileID, model string, req GenerationRequest, level PromptCacheLevel, status int, body []byte) (next PromptCacheLevel, retry bool) {
	if level == CacheMarkNone || !IsCacheControlRejection(status, body) {
		return level, false
	}
	next, changed := g.Degrade(profileID, model, SentCacheLevel(req, level))
	if changed {
		snippet := body
		if len(snippet) > 512 {
			snippet = snippet[:512]
		}
		logging.L().Warn("llm.prompt_cache.unsupported",
			"provider", provider, "profile_id", profileID, "model", model, "status", status,
			"now_marking", next.String(), "body", string(snippet))
	}
	return next, true
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
