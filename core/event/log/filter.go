// Package log — structured filter query + SQL builder.
//
// FilterQuery is the rich filter type for audit log queries. It is
// designed to be serialised to JSON for saved-query storage.
package log

import (
	"context"
	"strings"
	"time"
)

// FilterQuery is the canonical structured filter for audit log queries.
// All fields are optional; zero values mean "no restriction on this
// dimension".
type FilterQuery struct {
	// TimeRange restricts rows to those whose emitted_at falls in
	// [Since, Until]. Either bound may be zero.
	Since time.Time `json:"since,omitempty"`
	Until time.Time `json:"until,omitempty"`

	// ActorIDs filters by emitter_id. Empty slice = no restriction.
	ActorIDs []string `json:"actor_ids,omitempty"`

	// Kinds filters by event kind (exact match). Empty = no restriction.
	Kinds []string `json:"kinds,omitempty"`

	// Resources filters by resource tag embedded in payload. Empty = no restriction.
	// In the current in-memory implementation this is treated as a substring
	// of the serialised payload for broad compatibility.
	Resources []string `json:"resources,omitempty"`

	// Outcomes filters on outcome field in payload. Empty = no restriction.
	Outcomes []string `json:"outcomes,omitempty"`

	// FreeText performs a full-text search over payload bytes.
	// Uses FTS5 when the backend supports it, falls back to SQL LIKE.
	FreeText string `json:"free_text,omitempty"`

	// Verbose controls whether verbose-classified event kinds are included.
	// When false (default), kinds whose first segment is "verbose" are hidden.
	Verbose bool `json:"verbose,omitempty"`

	// Limit is the maximum number of rows to return. 0 = backend default.
	Limit int `json:"limit,omitempty"`

	// Offset for pagination.
	Offset int `json:"offset,omitempty"`
}

// isVerboseKind returns true when a kind is classified as verbose
// (e.g. "llm.token.stream"). Hidden by default unless Verbose=true.
func isVerboseKind(kind string) bool {
	return strings.HasPrefix(kind, "verbose.")
}

// ApplyToMemoryBackend evaluates the FilterQuery against a MemoryBackend
// and returns matching rows. This is the in-process fallback path; the
// real SQL builder will generate WHERE clauses for libSQL.
func (f FilterQuery) ApplyToMemoryBackend(ctx context.Context, backend Backend) ([]Row, error) {
	limit := f.Limit
	if limit <= 0 {
		limit = 1000
	}

	// Use SelectByTimeRange as the base selector when time bounds are set.
	var rows []Row
	var err error
	if !f.Since.IsZero() || !f.Until.IsZero() {
		rows, err = backend.SelectByTimeRange(ctx, f.Since, f.Until, "", 0, false)
	} else {
		rows, err = backend.SelectByTimeRange(ctx, time.Time{}, time.Time{}, "", 0, false)
	}
	if err != nil {
		return nil, err
	}

	// Build fast lookup sets.
	actorSet := toSet(f.ActorIDs)
	kindSet := toSet(f.Kinds)
	outcomeSet := toSet(f.Outcomes)
	resourceSet := toSet(f.Resources)

	out := make([]Row, 0, min(len(rows), limit))
	for _, r := range rows {
		// Verbose filter.
		if !f.Verbose && isVerboseKind(r.Kind) {
			continue
		}
		// Actor filter.
		if len(actorSet) > 0 {
			if _, ok := actorSet[r.EmitterID]; !ok {
				continue
			}
		}
		// Kind filter.
		if len(kindSet) > 0 {
			if _, ok := kindSet[r.Kind]; !ok {
				continue
			}
		}
		// Payload substring filters (resource + outcome + free-text).
		payloadLower := strings.ToLower(string(r.Payload))
		if len(resourceSet) > 0 {
			matched := false
			for res := range resourceSet {
				if strings.Contains(payloadLower, strings.ToLower(res)) {
					matched = true
					break
				}
			}
			if !matched {
				continue
			}
		}
		if len(outcomeSet) > 0 {
			matched := false
			for out := range outcomeSet {
				if strings.Contains(payloadLower, strings.ToLower(out)) {
					matched = true
					break
				}
			}
			if !matched {
				continue
			}
		}
		if f.FreeText != "" {
			if !strings.Contains(payloadLower, strings.ToLower(f.FreeText)) {
				continue
			}
		}
		out = append(out, r)
		if len(out) >= limit+f.Offset {
			break
		}
	}

	if f.Offset > 0 {
		if f.Offset >= len(out) {
			return nil, nil
		}
		out = out[f.Offset:]
	}
	if len(out) > limit {
		out = out[:limit]
	}
	return out, nil
}

func toSet(ss []string) map[string]struct{} {
	if len(ss) == 0 {
		return nil
	}
	m := make(map[string]struct{}, len(ss))
	for _, s := range ss {
		m[s] = struct{}{}
	}
	return m
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}

// SavedQuery is a persisted named filter query.
type SavedQuery struct {
	ID        string      `json:"id"`
	Name      string      `json:"name"`
	Query     FilterQuery `json:"query"`
	CreatedAt time.Time   `json:"created_at"`
	UserID    string      `json:"user_id,omitempty"`
}
