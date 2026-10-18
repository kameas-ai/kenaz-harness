package risk

import (
	"encoding/json"
	"fmt"
)

// NormalizeArgs canonicalizes a tool call's decoded arguments into a
// stable string representation, matching core/agentgraph's tool-dispatch
// doom-loop guard (doomLoopKey, core/agentgraph/doomloop.go) byte for
// byte: encoding/json.Marshal sorts map keys at every nesting depth and
// collapses insignificant numeric formatting (a decoded JSON number is
// already a float64 by the time it reaches this function, so "1", "1.0"
// and "1.00" all re-serialize identically). String content is never
// trimmed, case-folded, or otherwise rewritten — the same conservative
// tradeoff doomLoopKey documents: prefer under-normalizing (a spurious
// cache miss) to over-normalizing (a false cache hit that reuses a
// rating for an actually-different call).
//
// WP05 (risk-rated-autonomy-01PMRA01) uses this in two places that must
// agree: the LLMRater's cache key derivation (hashed, never stored raw)
// and the untrusted-DATA payload the rater's prompt embeds between its
// delimiters. Both derive from this one function so a cache hit and the
// text actually shown to the rating model describe the same call.
func NormalizeArgs(args map[string]any) string {
	canon, err := json.Marshal(args)
	if err != nil {
		// Should not happen for a map[string]any decoded from JSON by the
		// tool-dispatch path, but degrade to a stable-enough
		// representation rather than losing normalization outright.
		return fmt.Sprintf("%v", args)
	}
	return string(canon)
}
