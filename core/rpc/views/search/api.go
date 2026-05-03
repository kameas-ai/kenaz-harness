// Package search defines the SearchAPI view-scoped accessor on the
// HarnessAPI surface. It executes FTS5 full-text queries against the
// messages_fts virtual table and returns ranked snippet results.
//
// Sanitisation rule (DIRECTIVE_001 § sanitise):
//   - Empty / whitespace-only query → return empty result immediately.
//   - Strip FTS5 meta-chars the caller is unlikely to mean:
//     '(', ')', '"', '*', '^', '-', '+', ':' (column operator).
//   - Multi-word queries are left as-is so FTS5 treats them as implicit
//     AND (the default tokenizer behaviour with unicode61+porter).
//
// The sanitised string is bound as a plain positional parameter (?) so
// SQLite's FTS5 interprets it verbatim, avoiding any injection risk.
package search

import "context"

// SearchFilters carries optional filter predicates for a Search call.
// Zero values mean "no filter". Limit defaults to 50 when zero.
type SearchFilters struct {
	// ProjectID restricts results to sessions that belong to this project.
	// Empty string = any project.
	ProjectID string `json:"projectId,omitempty"`
	// SessionID restricts results to a single session (in-session search).
	// Rarely used; empty = all sessions.
	SessionID string `json:"sessionId,omitempty"`
	// RoleFilter is one of "", "user", "assistant", or "system".
	// Empty = all roles.
	RoleFilter string `json:"roleFilter,omitempty"`
	// Limit is the max number of hits; defaults to 50 when 0.
	Limit int `json:"limit,omitempty"`
}

// SearchHit is a single full-text match returned by Search.
type SearchHit struct {
	SessionID   string `json:"sessionId"`
	SessionName string `json:"sessionName"`
	MessageID   string `json:"messageId"`
	Role        string `json:"role"`
	// Snippet is a plain-text excerpt with STX/ETX delimiters stripped.
	// Highlight ranges are conveyed via Highlights.
	Snippet    string      `json:"snippet"`
	Highlights []Highlight `json:"highlights"`
	CreatedAt  string      `json:"createdAt"`
	ProjectID  string      `json:"projectId,omitempty"`
}

// Highlight is a byte-offset range within Snippet where the query matched.
// Start is inclusive, End is exclusive (UTF-8 byte offsets).
type Highlight struct {
	Start int `json:"start"`
	End   int `json:"end"`
}

// SearchAPI is the view-scoped interface for full-text search.
// Implementations MUST be safe for concurrent use.
type SearchAPI interface {
	// Search executes a full-text query against the messages_fts table.
	// An empty / whitespace-only query returns an empty slice without error.
	Search(ctx context.Context, query string, filters SearchFilters) ([]SearchHit, error)
}
