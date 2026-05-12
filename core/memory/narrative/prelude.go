package narrative

import (
	"context"
	"sort"
	"strings"
	"time"
)

// LongTermChunk is the minimal representation of a long-term chunk
// needed to build the system-prompt prelude.
type LongTermChunk struct {
	ID        string
	Content   string
	CreatedAt time.Time
}

// LongTermLoader is the narrow interface the Prelude loader uses to
// fetch chunks with ScopeKind="long_term" for the given session scope.
// The rpc layer wires the full core/memory.Store through an adapter.
type LongTermLoader interface {
	ListLongTerm(ctx context.Context, projectID string) ([]LongTermChunk, error)
}

// PreludeConfig controls the system-prompt prelude behavior.
type PreludeConfig struct {
	// TopN is the maximum number of long-term chunks to include.
	// Default 5 (NarrativePreludeTopN setting default).
	TopN int
}

// LoadPrelude returns the top-N long-term chunks for the given project,
// ordered by recency × log(retrievals+1) descending. When
// LongTermEnabled() is false this returns an empty slice immediately
// (the feature is inert in Phase 1).
//
// topN overrides PreludeConfig.TopN when > 0.
func LoadPrelude(
	ctx context.Context,
	loader LongTermLoader,
	metrics MetricsStore,
	projectID string,
	topN int,
) ([]LongTermChunk, error) {
	if !LongTermEnabled() {
		return nil, nil
	}
	if topN <= 0 {
		topN = 5
	}

	chunks, err := loader.ListLongTerm(ctx, projectID)
	if err != nil {
		return nil, err
	}
	if len(chunks) == 0 {
		return nil, nil
	}

	now := time.Now().UTC()

	type ranked struct {
		chunk LongTermChunk
		score float64
	}
	var ranked_ []ranked
	for _, c := range chunks {
		m, _ := metrics.Get(ctx, c.ID)
		score := PreludeScore(m, m.WindowStart, now)
		ranked_ = append(ranked_, ranked{c, score})
	}

	// Sort descending by score; ties broken by chunk ID ascending.
	sort.Slice(ranked_, func(i, j int) bool {
		if ranked_[i].score != ranked_[j].score {
			return ranked_[i].score > ranked_[j].score
		}
		return ranked_[i].chunk.ID < ranked_[j].chunk.ID
	})

	if len(ranked_) > topN {
		ranked_ = ranked_[:topN]
	}

	out := make([]LongTermChunk, len(ranked_))
	for i, r := range ranked_ {
		out[i] = r.chunk
	}
	return out, nil
}

// FormatPrelude renders the prelude chunks as a system-prompt string.
// Returns empty string when chunks is empty.
func FormatPrelude(chunks []LongTermChunk) string {
	if len(chunks) == 0 {
		return ""
	}
	var sb strings.Builder
	sb.WriteString("## Long-term memory (important context from previous sessions)\n\n")
	for i, c := range chunks {
		sb.WriteString("### Memory ")
		sb.WriteString(itoa(i + 1))
		sb.WriteString("\n")
		sb.WriteString(c.Content)
		sb.WriteString("\n\n")
	}
	return sb.String()
}

// itoa is a minimal integer-to-string helper that avoids importing strconv
// just for this one use. For small N (≤ NarrativePreludeTopN default 5)
// this is sufficient.
func itoa(n int) string {
	if n < 10 {
		return string(rune('0' + n))
	}
	// Fallback for larger N.
	digits := []byte{}
	for n > 0 {
		digits = append([]byte{byte('0' + n%10)}, digits...)
		n /= 10
	}
	return string(digits)
}
