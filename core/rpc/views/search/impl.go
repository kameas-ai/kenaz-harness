package search

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"fmt"
	"runtime/debug"
	"strings"
	"time"

	"github.com/kameas-ai/kenaz-harness/core/logging"
)

// AuditEmitter is the narrow seam the search view calls when emitting
// search-executed audit events (cross-session-search WP07). Mirrors the
// tools view's AuditEmitter shape so the chassis can wire one
// process-wide emitter into both surfaces. nil-tolerant — when nil, no
// audit row is emitted.
//
// Privacy contract: the raw query string MUST NOT appear in the
// emitted attrs map. Callers receive only a sha256-hex prefix
// (`query_hash`), the result count, the list of applied filter names,
// and the wall-clock duration.
type AuditEmitter interface {
	Emit(ctx context.Context, kind string, attrs map[string]any)
}

// EnabledFn returns whether the search subsystem is enabled. Wired
// from the Settings store by the chassis. nil → always enabled.
type EnabledFn func() bool

// KindSearchExecuted is the audit event kind emitted on every Search
// call (cross-session-search WP07). The payload carries query_hash
// (sha256 hex prefix), result_count, filters_applied, took_ms — the
// raw query never appears.
const KindSearchExecuted = "search.executed"

// sqlQuerier is the minimal interface over *sql.DB that the search
// implementation needs.  The real backend satisfies it; tests can pass
// a fake.
type sqlQuerier interface {
	QueryContext(ctx context.Context, query string, args ...any) (*sql.Rows, error)
}

// managerAPI is the concrete SearchAPI backed by a raw *sql.DB (or
// anything that satisfies sqlQuerier).
type managerAPI struct {
	db      sqlQuerier
	audit   AuditEmitter
	enabled EnabledFn
	// Cross-corpus adapters (unified search, WP02/WP03).
	artAdapter    corpusAdapter
	memAdapter    corpusAdapter
	corpusAdapter corpusAdapter
	auditAdapter  corpusAdapter
}

// NewManagerAPI returns a SearchAPI backed by the supplied *sql.DB.
// db must be non-nil. Audit emission and the enabled gate are not
// wired by this constructor — use NewManagerAPIWithConfig for v0.3.0+
// callers that want WP07 behaviour.
func NewManagerAPI(db *sql.DB) SearchAPI {
	return &managerAPI{db: db}
}

// Config bundles the optional dependencies the search view consumes
// for WP07 (audit + enable dial) and WP03 (unified multi-corpus search).
// All fields are nil-tolerant.
type Config struct {
	// Audit receives the search.executed event on every Search call.
	// nil → no audit emission.
	Audit AuditEmitter
	// Enabled is consulted at the top of every Search call. When the
	// callback returns false, Search returns an empty result without
	// touching the FTS5 index. nil → always enabled.
	Enabled EnabledFn
	// Cross-corpus adapters for UnifiedSearch (WP03). nil adapters are
	// gracefully skipped during the fan-out.
	//
	// ArtifactsDB is the raw *sql.DB for artifact title search.
	ArtifactsDB *sql.DB
	// MemoryStore is the long-term memory store (HARNESS_MEMORY=on path).
	// The adapter calls its List() method and filters by content substring.
	MemoryStore MemoryLister
	// CorpusDB is the raw *sql.DB for corpus name/tag search.
	CorpusDB *sql.DB
	// AuditLister surfaces the in-memory audit ring buffer.
	AuditLister AuditLister
}

// MemoryLister is the subset of core/memory.Store the search view needs.
// core/memory.Store satisfies this; tests may pass a fake.
type MemoryLister interface {
	List(ctx context.Context) ([]MemoryChunk, error)
}

// MemoryChunk is the minimal cross-boundary shape for memory chunks.
// It mirrors core/memory.Chunk without importing that package from impl.go.
type MemoryChunk struct {
	ID        string
	SessionID string
	ProjectID string
	Content   string
	CreatedAt interface{ Format(string) string }
}

// NewManagerAPIWithConfig returns a SearchAPI with the WP07 audit +
// enable dial wired in. db must be non-nil; cfg fields are
// nil-tolerant.
func NewManagerAPIWithConfig(db *sql.DB, cfg Config) SearchAPI {
	m := &managerAPI{db: db, audit: cfg.Audit, enabled: cfg.Enabled}
	if cfg.ArtifactsDB != nil {
		m.artAdapter = &artifactsSearcher{db: cfg.ArtifactsDB}
	}
	if cfg.MemoryStore != nil {
		m.memAdapter = &memorySearcher{lister: cfg.MemoryStore}
	}
	if cfg.CorpusDB != nil {
		m.corpusAdapter = &corpusNameSearcher{db: cfg.CorpusDB}
	}
	if cfg.AuditLister != nil {
		m.auditAdapter = &auditSearcher{lister: cfg.AuditLister}
	}
	return m
}

// newManagerAPIFromQuerier is used by tests that supply a fake querier.
func newManagerAPIFromQuerier(q sqlQuerier) SearchAPI {
	return &managerAPI{db: q}
}

// newManagerAPIFromQuerierWithConfig is the test-side analogue of
// NewManagerAPIWithConfig.
func newManagerAPIFromQuerierWithConfig(q sqlQuerier, cfg Config) SearchAPI {
	return &managerAPI{db: q, audit: cfg.Audit, enabled: cfg.Enabled}
}

// queryHash returns the 16-hex-char prefix of sha256(lowercased trimmed
// query). Used for audit so the raw query never lands in the log.
func queryHash(q string) string {
	sum := sha256.Sum256([]byte(strings.ToLower(strings.TrimSpace(q))))
	return hex.EncodeToString(sum[:])[:16]
}

// filtersApplied returns the names of non-zero filter fields. Used as
// audit metadata so the chassis can surface "was the project filter
// active?" without leaking the actual id (which may itself be
// sensitive — e.g. a project named after a customer).
func filtersApplied(f SearchFilters) []string {
	var out []string
	if f.ProjectID != "" {
		out = append(out, "project")
	}
	if f.SessionID != "" {
		out = append(out, "session")
	}
	if f.RoleFilter != "" {
		out = append(out, "role")
	}
	if f.Limit > 0 {
		out = append(out, "limit")
	}
	return out
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
	// WP07 — settings dial. When the user has flipped SearchDisabled
	// (or the chassis hasn't wired the gate yet and we want to fail
	// closed in the future), short-circuit before touching the index.
	if a.enabled != nil && !a.enabled() {
		return nil, nil
	}

	q := sanitise(query)
	if q == "" {
		return nil, nil
	}

	// WP07 — audit. We capture the start time outside the deferred emit
	// so the took_ms attribute reflects the full Search call, not just
	// the SQL round-trip. The raw query string is hashed and truncated
	// per the privacy contract on AuditEmitter — this method MUST NOT
	// pass `query` itself into the audit attrs map.
	start := time.Now()
	resultCount := 0
	defer func() {
		if a.audit == nil {
			return
		}
		a.audit.Emit(ctx, KindSearchExecuted, map[string]any{
			"query_hash":      queryHash(query),
			"result_count":    resultCount,
			"filters_applied": filtersApplied(filters),
			"took_ms":         time.Since(start).Milliseconds(),
		})
	}()

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
	resultCount = len(hits)
	return hits, nil
}

// UnifiedSearch fans out across all configured corpora in parallel and
// returns a merged, ranked result list. filters.Corpora narrows which
// corpora are queried; an empty slice enables all five sources.
//
// Score formula: within each corpus, hits are scored 1.0 − pos/(N+1) so
// the first result from each corpus gets the highest score. Messages hits
// are pre-multiplied by 1.0 (already BM25-ranked by SQLite); other corpora
// use 0.5. Results are sorted by score descending.
//
// Privacy: the raw query never appears in audit attrs — only query_hash.
func (a *managerAPI) UnifiedSearch(ctx context.Context, query string, filters SearchFilters) ([]SearchHit, error) {
	if a.enabled != nil && !a.enabled() {
		return nil, nil
	}
	q := sanitise(query)
	if q == "" {
		return nil, nil
	}

	limit := filters.Limit
	if limit <= 0 {
		limit = defaultLimit
	}

	// Determine which corpora are enabled.
	enabled := buildCorpusSet(filters.Corpora)

	type adapterEntry struct {
		corpus  string
		adapter corpusAdapter
		weight  float32 // multiplicative weight applied to position-score
	}
	adapters := []adapterEntry{
		{CorpusMessages, a.messagesAdapter(q, filters, limit), 1.0},
		{CorpusArtifacts, a.artAdapter, 0.5},
		{CorpusMemory, a.memAdapter, 0.5},
		{CorpusCorpus, a.corpusAdapter, 0.5},
		{CorpusAudit, a.auditAdapter, 0.4},
	}

	type result struct {
		hits []SearchHit
		err  error
	}
	ch := make(chan result, len(adapters))
	for _, ae := range adapters {
		ae := ae
		if !enabled[ae.corpus] || ae.adapter == nil {
			ch <- result{}
			continue
		}
		go func() {
			// Recover panics from the per-corpus adapter (FR-003). A
			// panicking adapter returns an error for that corpus so the
			// collector is not stranded, and the process does not crash.
			defer func() {
				if r := recover(); r != nil {
					stack := debug.Stack()
					logging.L().Error("search.unified.corpus_adapter.panic",
						"corpus", ae.corpus,
						"panic", fmt.Sprintf("%v", r),
						"stack", string(stack),
					)
					ch <- result{err: fmt.Errorf("corpus %q adapter panicked: %v", ae.corpus, r)}
				}
			}()
			hits, err := ae.adapter.search(ctx, q, limit)
			// Score each hit based on its position within the corpus.
			for i := range hits {
				posScore := float32(1.0) - float32(i)/float32(len(hits)+1)
				hits[i].Score = ae.weight * posScore
				if hits[i].Corpus == "" {
					hits[i].Corpus = ae.corpus
				}
			}
			ch <- result{hits: hits, err: err}
		}()
	}

	var (
		all    []SearchHit
		firstErr error
		seen   = map[string]bool{}
	)
	for range adapters {
		r := <-ch
		if r.err != nil && firstErr == nil {
			firstErr = r.err
		}
		for _, h := range r.hits {
			key := h.Corpus + ":" + h.EntityID
			if seen[key] {
				continue
			}
			seen[key] = true
			all = append(all, h)
		}
	}
	if firstErr != nil {
		return nil, firstErr
	}

	// Sort by score descending.
	sortByScore(all)

	// Honour the overall limit.
	if len(all) > limit {
		all = all[:limit]
	}

	// Audit emission — same privacy contract as Search().
	if a.audit != nil {
		a.audit.Emit(ctx, KindSearchExecuted, map[string]any{
			"query_hash":      queryHash(query),
			"result_count":    len(all),
			"filters_applied": filtersApplied(filters),
			"unified":         true,
		})
	}
	return all, nil
}

// messagesAdapter wraps the existing FTS5 SQL path as a corpusAdapter so it
// can participate in the parallel fan-out. It returns a one-off adapter
// whose search() method executes the messages query and marks each hit with
// Corpus=CorpusMessages.
func (a *managerAPI) messagesAdapter(q string, filters SearchFilters, limit int) corpusAdapter {
	return &messagesAdapterFn{a: a, q: q, filters: filters, limit: limit}
}

type messagesAdapterFn struct {
	a       *managerAPI
	q       string
	filters SearchFilters
	limit   int
}

func (m *messagesAdapterFn) search(ctx context.Context, _ string, _ int) ([]SearchHit, error) {
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
	rows, err := m.a.db.QueryContext(ctx, sqlQ,
		m.q,
		m.filters.ProjectID, m.filters.ProjectID,
		m.filters.SessionID, m.filters.SessionID,
		m.filters.RoleFilter, m.filters.RoleFilter,
		m.limit,
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
			Corpus:      CorpusMessages,
			EntityID:    messageID,
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
	return hits, rows.Err()
}

// buildCorpusSet converts a []string filter into a set. An empty filter
// enables all five known corpora.
func buildCorpusSet(corpora []string) map[string]bool {
	all := map[string]bool{
		CorpusMessages:  true,
		CorpusArtifacts: true,
		CorpusMemory:    true,
		CorpusCorpus:    true,
		CorpusAudit:     true,
	}
	if len(corpora) == 0 {
		return all
	}
	set := make(map[string]bool, len(corpora))
	for _, c := range corpora {
		if all[c] {
			set[c] = true
		}
	}
	return set
}

// sortByScore sorts hits in-place by Score descending (highest first).
// A simple insertion sort is fine: the result set is bounded by defaultLimit (50).
func sortByScore(hits []SearchHit) {
	for i := 1; i < len(hits); i++ {
		for j := i; j > 0 && hits[j].Score > hits[j-1].Score; j-- {
			hits[j], hits[j-1] = hits[j-1], hits[j]
		}
	}
}
