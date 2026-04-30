package search

import (
	"context"
	"errors"
	"strings"
)

// ErrEmptyQuery is returned when Query.Text is blank.
var ErrEmptyQuery = errors.New("search: query text must not be empty")

// defaultLimit is used when Query.Limit is zero or negative.
const defaultLimit = 20

// snippetOpen and snippetClose are the delimiters injected by SQLite's
// snippet() function around each highlighted term. They are chosen to be
// unlikely to appear in message text and to be valid UTF-8, so byte-offset
// arithmetic is unambiguous.
const snippetOpen = "\x02"
const snippetClose = "\x03"

// Reader is the minimal read surface the Manager needs from a DB. It
// matches the shape of storage.Reader and session.Reader so callers can
// wire either directly.
type Reader interface {
	QueryRow(ctx context.Context, query string, args ...any) Row
	Query(ctx context.Context, query string, args ...any) (Rows, error)
}

// Row mirrors database/sql.Row.
type Row interface {
	Scan(dest ...any) error
}

// Rows mirrors database/sql.Rows.
type Rows interface {
	Next() bool
	Scan(dest ...any) error
	Close() error
	Err() error
}

// DB is the minimal connection contract the Manager consumes. Both
// storage.DB (via its Reader() method) and any type that exposes a
// Reader() satisfying the interface above will work.
type DB interface {
	Reader() Reader
}

// Manager provides full-text search over session messages. It is safe
// for concurrent reads.
type Manager struct {
	db DB
}

// NewManager constructs a Manager. db must not be nil.
func NewManager(db DB) *Manager {
	return &Manager{db: db}
}

// Search executes a full-text search and returns up to q.Limit results
// ordered by BM25 relevance (most relevant first).
//
// The FTS5 snippet() function is called with the open/close delimiters
// defined by snippetOpen/snippetClose. parseHighlights then walks the
// returned string, stripping the delimiters while recording each
// highlighted range as a HighlightRange byte-offset pair into the plain
// snippet.
func (m *Manager) Search(ctx context.Context, q Query) ([]Result, error) {
	if strings.TrimSpace(q.Text) == "" {
		return nil, ErrEmptyQuery
	}
	limit := q.Limit
	if limit <= 0 {
		limit = defaultLimit
	}
	offset := q.Offset
	if offset < 0 {
		offset = 0
	}

	var (
		sqlQuery string
		args     []any
	)

	// snippet(messages_fts, 0, open, close, ellipsis, tokens)
	// Column 0 is the content column. Tokens=-15 means up to 15 tokens
	// of context around the first match in the snippet.
	if q.SessionID != "" {
		sqlQuery = `
            SELECT sm.session_id, sm.id,
                   snippet(messages_fts, 0, ?, ?, '…', -15) AS snip
            FROM messages_fts
            JOIN session_messages sm ON sm.rowid = messages_fts.rowid
            WHERE messages_fts MATCH ?
              AND sm.session_id = ?
              AND sm.archived_at IS NULL
            ORDER BY rank
            LIMIT ? OFFSET ?
        `
		args = []any{snippetOpen, snippetClose, q.Text, q.SessionID, limit, offset}
	} else {
		sqlQuery = `
            SELECT sm.session_id, sm.id,
                   snippet(messages_fts, 0, ?, ?, '…', -15) AS snip
            FROM messages_fts
            JOIN session_messages sm ON sm.rowid = messages_fts.rowid
            WHERE messages_fts MATCH ?
              AND sm.archived_at IS NULL
            ORDER BY rank
            LIMIT ? OFFSET ?
        `
		args = []any{snippetOpen, snippetClose, q.Text, limit, offset}
	}

	rows, err := m.db.Reader().Query(ctx, sqlQuery, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []Result
	for rows.Next() {
		var (
			sessionID string
			messageID string
			rawSnip   string
		)
		if err := rows.Scan(&sessionID, &messageID, &rawSnip); err != nil {
			return nil, err
		}
		snippet, highlights := parseHighlights(rawSnip)
		out = append(out, Result{
			SessionID:  sessionID,
			MessageID:  messageID,
			Snippet:    snippet,
			Highlights: highlights,
		})
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return out, nil
}

// parseHighlights walks rawSnip (which contains snippetOpen/snippetClose
// delimiters around each matched term) and returns the plain snippet text
// and the half-open byte ranges [Start, End) within that plain text where
// each match appears.
//
// Algorithm:
//  1. Scan the raw string byte by byte.
//  2. When snippetOpen is seen, record the current write position as
//     Start and skip the delimiter bytes.
//  3. When snippetClose is seen, record the current write position as
//     End, emit the range, and skip the delimiter bytes.
//  4. All other bytes are copied verbatim to the output buffer.
func parseHighlights(rawSnip string) (string, []HighlightRange) {
	openLen := len(snippetOpen)
	closeLen := len(snippetClose)

	buf := make([]byte, 0, len(rawSnip))
	var highlights []HighlightRange

	inMatch := false
	matchStart := 0

	i := 0
	for i < len(rawSnip) {
		if strings.HasPrefix(rawSnip[i:], snippetOpen) {
			// Start of a highlighted span.
			inMatch = true
			matchStart = len(buf)
			i += openLen
			continue
		}
		if strings.HasPrefix(rawSnip[i:], snippetClose) {
			if inMatch {
				highlights = append(highlights, HighlightRange{
					Start: matchStart,
					End:   len(buf),
				})
				inMatch = false
			}
			i += closeLen
			continue
		}
		buf = append(buf, rawSnip[i])
		i++
	}
	return string(buf), highlights
}
