// Package search provides full-text search over session messages.
//
// The search backend is an FTS5 virtual table (messages_fts) backed by
// the session_messages table. FTS5 uses porter wrapping unicode61 as its
// tokenizer chain (DDL form: "porter unicode61"), so search terms are
// matched after Unicode normalisation, diacritic removal, and Porter
// stemming.
//
// Usage:
//
//	mgr := search.NewManager(db)
//	results, err := mgr.Search(ctx, search.Query{Text: "rust"})
//
// Results are ordered by FTS5 BM25 rank (most relevant first). Each
// result carries a Snippet (a short excerpt from the message produced by
// SQLite's snippet() function) and HighlightRanges that give the byte
// offsets of the matched terms inside the snippet string.
package search
