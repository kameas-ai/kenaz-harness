package search

import (
	"context"
	"database/sql"
	"strings"
	"time"
)

// sqlQuerier is the minimal interface over *sql.DB that the search
// implementation needs.  The real backend satisfies it; tests can pass
// a fake.
type sqlQuerier interface {
	QueryContext(ctx context.Context, query string, args ...any) (*sql.Rows, error)
}

// managerAPI is the concrete SearchAPI backed by a raw *sql.DB (or
// anything that satisfies sqlQuerier).
type managerAPI struct {
	db sqlQuerier
}

// NewManagerAPI returns a SearchAPI backed by the supplied *sql.DB.
// db must be non-nil.
func NewManagerAPI(db *sql.DB) SearchAPI {
	return &managerAPI{db: db}
}

// newManagerAPIFromQuerier is used by tests that supply a fake querier.
func newManagerAPIFromQuerier(q sqlQuerier) SearchAPI {
	return &managerAPI{db: q}
}

// sanitise returns a safe FTS5 query term, or "" if the input is
// empty / whitespace-only.
//
// Rule: strip characters that have syntactic meaning in FTS5 but are
// unlikely to be intentional from a plain-text search input:
//   ( ) " * ^ - + :
//
// We do NOT strip AND / OR / NOT so power users can use them, but we
// do NOT add implicit quotes — single-token terms are fine as-is and
// multi-token terms fall through to FTS5's default implicit-AND.
func sanitise(q string) string {
	q = strings.TrimSpace(q)
	if q == "" {
		return ""
	}
	var b strings.Builder
	b.Grow(len(q))
	for _, r := range q {
		switch r {
		case '(', ')', '"', '*', '^', '+', ':', '-':
			// drop FTS5 special chars
		default:
			b.WriteRune(r)
		}
	}
	return strings.TrimSpace(b.String())
}

// extractHighlights splits a snippet string that uses ASCII STX (0x02)
// as the highlight-open delimiter and ETX (0x03) as the highlight-close
// delimiter. It returns the plain text with delimiters removed and the
// corresponding byte-offset ranges.
func extractHighlights(s string) (plain string, ranges []Highlight) {
	const maxHighlights = 8
	var out strings.Builder
	out.Grow(len(s))
	var start int
	inMark := false

	for i := 0; i < len(s); {
		b := s[i]
		switch b {
		case 0x02: // STX — open highlight
			if !inMark {
				start = out.Len()
				inMark = true
			}
			i++
		case 0x03: // ETX — close highlight
			if inMark && len(ranges) < maxHighlights {
				ranges = append(ranges, Highlight{Start: start, End: out.Len()})
				inMark = false
			}
			i++
		default:
			// Multi-byte UTF-8: copy the full rune so byte offsets stay valid.
			_, sz := decodeRuneLen(s[i:])
			out.WriteString(s[i : i+sz])
			i += sz
		}
	}
	return out.String(), ranges
}

// decodeRuneLen returns the byte length of the first rune encoded at
// the beginning of b.  Mirrors utf8.DecodeRuneInString but avoids
// importing unicode/utf8 just for the size helper.
func decodeRuneLen(b string) (rune, int) {
	if len(b) == 0 {
		return 0, 0
	}
	c := b[0]
	switch {
	case c < 0x80:
		return rune(c), 1
	case c < 0xC0:
		return 0xFFFD, 1 // continuation byte without leader — replacement
	case c < 0xE0:
		if len(b) < 2 {
			return 0xFFFD, 1
		}
		return rune(c&0x1F)<<6 | rune(b[1]&0x3F), 2
	case c < 0xF0:
		if len(b) < 3 {
			return 0xFFFD, 1
		}
		return rune(c&0x0F)<<12 | rune(b[1]&0x3F)<<6 | rune(b[2]&0x3F), 3
	default:
		if len(b) < 4 {
			return 0xFFFD, 1
		}
		return rune(c&0x07)<<18 | rune(b[1]&0x3F)<<12 | rune(b[2]&0x3F)<<6 | rune(b[3]&0x3F), 4
	}
}

// defaultLimit is the default result cap.
const defaultLimit = 50

// Search implements SearchAPI.
//
// SQL shape:
//
//	SELECT s.id, s.name, m.id, m.role,
//	       snippet(messages_fts, 0, char(2), char(3), '…', 32),
//	       m.created_at, COALESCE(s.project_id, '')
//	  FROM messages_fts
//	  JOIN session_messages m ON m.rowid = messages_fts.rowid
//	  JOIN sessions s ON s.id = m.session_id
//	 WHERE messages_fts MATCH ?
//	   AND (? = '' OR s.project_id = ?)
//	   AND (? = '' OR m.session_id = ?)
//	   AND (? = '' OR m.role = ?)
//	 ORDER BY rank
//	 LIMIT ?
func (a *managerAPI) Search(ctx context.Context, query string, filters SearchFilters) ([]SearchHit, error) {
	q := sanitise(query)
	if q == "" {
		return nil, nil
	}

	limit := filters.Limit
	if limit <= 0 {
		limit = defaultLimit
	}

	const sqlQ = `
SELECT s.id, s.name, m.id, m.role,
       snippet(messages_fts, 0, char(2), char(3), '…', 32),
       m.created_at, COALESCE(s.project_id, '')
  FROM messages_fts
  JOIN session_messages m ON m.rowid = messages_fts.rowid
  JOIN sessions s ON s.id = m.session_id
 WHERE messages_fts MATCH ?
   AND (? = '' OR s.project_id = ?)
   AND (? = '' OR m.session_id = ?)
   AND (? = '' OR m.role = ?)
 ORDER BY rank
 LIMIT ?`

	rows, err := a.db.QueryContext(ctx, sqlQ,
		q,
		filters.ProjectID, filters.ProjectID,
		filters.SessionID, filters.SessionID,
		filters.RoleFilter, filters.RoleFilter,
		limit,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var hits []SearchHit
	for rows.Next() {
		var (
			sessionID   string
			sessionName string
			messageID   string
			role        string
			snipRaw     string
			createdAtNs int64
			projectID   string
		)
		if err := rows.Scan(&sessionID, &sessionName, &messageID, &role,
			&snipRaw, &createdAtNs, &projectID); err != nil {
			return nil, err
		}
		plain, hlRanges := extractHighlights(snipRaw)
		ts := time.Unix(0, createdAtNs).UTC().Format(time.RFC3339Nano)
		hits = append(hits, SearchHit{
			SessionID:   sessionID,
			SessionName: sessionName,
			MessageID:   messageID,
			Role:        role,
			Snippet:     plain,
			Highlights:  hlRanges,
			CreatedAt:   ts,
			ProjectID:   projectID,
		})
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return hits, nil
}
