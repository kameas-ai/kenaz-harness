package search

import (
	"context"
	"database/sql"
	"fmt"
	"testing"
	"time"

	_ "modernc.org/sqlite"
)

// ── sanitise tests ─────────────────────────────────────────────────────

func TestSanitise(t *testing.T) {
	tests := []struct {
		in   string
		want string
	}{
		{"", ""},
		{"   ", ""},
		{"hello", "hello"},
		{"hello world", "hello world"},
		{"(hello)", "hello"},
		{`"phrase query"`, "phrase query"},
		{"star*", "star"},
		{"col:value", "colvalue"},
		{"AND OR NOT", "AND OR NOT"},   // boolean ops preserved
		{"  spaces  ", "spaces"},
		{"(nested (deep))", "nested deep"},
		{"migration 0310", "migration 0310"},
	}
	for _, tc := range tests {
		got := sanitise(tc.in)
		if got != tc.want {
			t.Errorf("sanitise(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

// ── extractHighlights tests ────────────────────────────────────────────

func TestExtractHighlights(t *testing.T) {
	stx := string([]byte{0x02})
	etx := string([]byte{0x03})

	tests := []struct {
		name       string
		input      string
		wantPlain  string
		wantRanges []Highlight
	}{
		{
			name:       "no highlights",
			input:      "hello world",
			wantPlain:  "hello world",
			wantRanges: nil,
		},
		{
			name:       "single highlight",
			input:      "hello " + stx + "world" + etx,
			wantPlain:  "hello world",
			wantRanges: []Highlight{{Start: 6, End: 11}},
		},
		{
			name:       "two highlights",
			input:      stx + "foo" + etx + " bar " + stx + "baz" + etx,
			wantPlain:  "foo bar baz",
			wantRanges: []Highlight{{Start: 0, End: 3}, {Start: 8, End: 11}},
		},
		{
			name:      "multibyte utf8",
			input:     "こんにちは " + stx + "world" + etx,
			wantPlain: "こんにちは world",
			// "こんにちは " = 5 × 3 bytes + 1 ASCII space = 16 bytes
			wantRanges: []Highlight{{Start: 16, End: 21}},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			plain, ranges := extractHighlights(tc.input)
			if plain != tc.wantPlain {
				t.Errorf("plain = %q, want %q", plain, tc.wantPlain)
			}
			if len(ranges) != len(tc.wantRanges) {
				t.Fatalf("ranges len = %d, want %d; got %v", len(ranges), len(tc.wantRanges), ranges)
			}
			for i, r := range ranges {
				if r != tc.wantRanges[i] {
					t.Errorf("ranges[%d] = %v, want %v", i, r, tc.wantRanges[i])
				}
			}
		})
	}
}

// ranges must satisfy: 0 ≤ Start < End ≤ len(plain), non-overlapping, ascending.
func TestExtractHighlightsProperty(t *testing.T) {
	stx := string([]byte{0x02})
	etx := string([]byte{0x03})
	input := "prefix " + stx + "alpha" + etx + " middle " + stx + "beta" + etx + " suffix"
	plain, ranges := extractHighlights(input)
	plen := len(plain)
	for i, r := range ranges {
		if r.Start < 0 || r.End > plen || r.Start >= r.End {
			t.Errorf("ranges[%d] = %v out of bounds (plain len=%d)", i, r, plen)
		}
		if i > 0 && ranges[i-1].End > r.Start {
			t.Errorf("ranges overlap: [%d]=%v, [%d]=%v", i-1, ranges[i-1], i, r)
		}
	}
}

// ── capturingQuerier: records args, returns configurable rows ──────────

// capturingQuerier records what was passed to QueryContext and returns
// rows built from an in-memory SQLite database so we don't need real FTS.
type capturingQuerier struct {
	rows      [][]any // each inner slice is one row: sessionID,sessionName,messageID,role,snippet,createdAt,projectID
	err       error
	lastQuery string
	lastArgs  []any
}

func (c *capturingQuerier) QueryContext(_ context.Context, query string, args ...any) (*sql.Rows, error) {
	c.lastQuery = query
	c.lastArgs = append([]any(nil), args...)
	if c.err != nil {
		return nil, c.err
	}
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		return nil, fmt.Errorf("capturingQuerier: open: %w", err)
	}
	_, err = db.Exec(`CREATE TABLE t (
		session_id TEXT, session_name TEXT, message_id TEXT,
		role TEXT, snippet TEXT, created_at INTEGER, project_id TEXT)`)
	if err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("capturingQuerier: create: %w", err)
	}
	for _, row := range c.rows {
		_, err = db.Exec(`INSERT INTO t VALUES(?,?,?,?,?,?,?)`, row...)
		if err != nil {
			_ = db.Close()
			return nil, fmt.Errorf("capturingQuerier: insert: %w", err)
		}
	}
	return db.QueryContext(context.Background(), `SELECT * FROM t`)
}

// ── Search unit tests ──────────────────────────────────────────────────

func TestSearch_EmptyQuery(t *testing.T) {
	api := newManagerAPIFromQuerier(&capturingQuerier{})
	hits, err := api.Search(context.Background(), "", SearchFilters{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(hits) != 0 {
		t.Errorf("expected 0 hits for empty query, got %d", len(hits))
	}
}

func TestSearch_WhitespaceQuery(t *testing.T) {
	api := newManagerAPIFromQuerier(&capturingQuerier{})
	hits, err := api.Search(context.Background(), "   ", SearchFilters{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(hits) != 0 {
		t.Errorf("expected 0 hits for whitespace query, got %d", len(hits))
	}
}

func TestSearch_SingleTermMatch(t *testing.T) {
	stx := string([]byte{0x02})
	etx := string([]byte{0x03})
	ts := time.Now().UnixNano()
	cq := &capturingQuerier{rows: [][]any{
		{"s1", "Test session", "m1", "user", "hello " + stx + "world" + etx, ts, ""},
	}}
	api := newManagerAPIFromQuerier(cq)
	hits, err := api.Search(context.Background(), "world", SearchFilters{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(hits) != 1 {
		t.Fatalf("expected 1 hit, got %d", len(hits))
	}
	if hits[0].Snippet != "hello world" {
		t.Errorf("Snippet = %q, want %q", hits[0].Snippet, "hello world")
	}
	if len(hits[0].Highlights) != 1 {
		t.Errorf("Highlights len = %d, want 1", len(hits[0].Highlights))
	}
	if hits[0].SessionName != "Test session" {
		t.Errorf("SessionName = %q", hits[0].SessionName)
	}
}

func TestSearch_MultiSessionMatch(t *testing.T) {
	ts := time.Now().UnixNano()
	cq := &capturingQuerier{rows: [][]any{
		{"s1", "Alpha", "m1", "user", "match here", ts, ""},
		{"s2", "Beta", "m2", "assistant", "match there", ts, ""},
	}}
	api := newManagerAPIFromQuerier(cq)
	hits, err := api.Search(context.Background(), "match", SearchFilters{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(hits) != 2 {
		t.Errorf("expected 2 hits, got %d", len(hits))
	}
}

func TestSearch_NoResults(t *testing.T) {
	api := newManagerAPIFromQuerier(&capturingQuerier{})
	hits, err := api.Search(context.Background(), "nomatch", SearchFilters{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(hits) != 0 {
		t.Errorf("expected 0 hits, got %d", len(hits))
	}
}

func TestSearch_RoleFilter(t *testing.T) {
	cq := &capturingQuerier{}
	api := newManagerAPIFromQuerier(cq)
	_, _ = api.Search(context.Background(), "ok", SearchFilters{RoleFilter: "user"})
	found := false
	for _, a := range cq.lastArgs {
		if s, ok := a.(string); ok && s == "user" {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("RoleFilter 'user' not found in SQL args: %v", cq.lastArgs)
	}
}

func TestSearch_ProjectFilter(t *testing.T) {
	cq := &capturingQuerier{}
	api := newManagerAPIFromQuerier(cq)
	_, _ = api.Search(context.Background(), "ok", SearchFilters{ProjectID: "p1"})
	found := false
	for _, a := range cq.lastArgs {
		if s, ok := a.(string); ok && s == "p1" {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("ProjectID 'p1' not found in SQL args: %v", cq.lastArgs)
	}
}

func TestSearch_SanitisationStripsMeta(t *testing.T) {
	// "(dangerous*)" sanitises to "dangerous", which is non-empty and
	// should reach the DB. The raw "(dangerous*)" must NOT reach the DB.
	cq := &capturingQuerier{}
	api := newManagerAPIFromQuerier(cq)
	_, _ = api.Search(context.Background(), "(dangerous*)", SearchFilters{})
	if len(cq.lastArgs) > 0 {
		q, ok := cq.lastArgs[0].(string)
		if ok && q == "(dangerous*)" {
			t.Errorf("unsanitised query reached DB: %q", q)
		}
	}
}

func TestSearch_DefaultLimit(t *testing.T) {
	cq := &capturingQuerier{}
	api := newManagerAPIFromQuerier(cq)
	_, _ = api.Search(context.Background(), "hello", SearchFilters{Limit: 0})
	if len(cq.lastArgs) > 0 {
		last := cq.lastArgs[len(cq.lastArgs)-1]
		if n, ok := last.(int); ok && n != defaultLimit {
			t.Errorf("limit arg = %d, want %d", n, defaultLimit)
		}
	}
}
