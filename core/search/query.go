package search

// Query carries the parameters for a full-text search request.
//
// Text is the only required field. An empty Text returns an error.
// Limit defaults to 20 when zero. Offset supports page-based
// navigation. SessionID, when non-empty, restricts results to a single
// session.
type Query struct {
	// Text is the FTS5 match expression. Simple word queries ("rust"),
	// phrase queries ("borrow checker"), and FTS5 operators are all valid.
	Text string

	// Limit is the maximum number of results to return. Zero is treated
	// as 20.
	Limit int

	// Offset skips the first Offset results. Used for paging.
	Offset int

	// SessionID, if non-empty, restricts results to messages in that
	// session.
	SessionID string
}

// HighlightRange is a half-open byte range [Start, End) into the
// Snippet string. The frontend renders each range as a <mark> tag.
type HighlightRange struct {
	Start int
	End   int
}

// Result is one search hit.
type Result struct {
	// SessionID is the session that owns the matching message.
	SessionID string

	// MessageID is the id of the matching session_messages row.
	MessageID string

	// Snippet is a short excerpt of the message text produced by SQLite's
	// snippet() function. It always uses the delimiter pair defined by
	// snippetOpen / snippetClose so the parser can extract highlight ranges.
	Snippet string

	// Highlights lists the byte-offset ranges within Snippet where matched
	// terms appear. Ordered by Start ascending. Start and End are byte
	// (not rune) offsets into Snippet.
	Highlights []HighlightRange
}
