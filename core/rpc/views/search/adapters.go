package search

// adapters.go — per-corpus search adapters for UnifiedSearch.
//
// Each adapter implements a narrow interface:
//
//	type corpusAdapter interface {
//	    search(ctx context.Context, q string, limit int) ([]SearchHit, error)
//	}
//
// adapters are composed into managerAPI by the Config extension introduced in
// WP03. All adapters are nil-safe (returning an empty slice without error when
// their backing store is not wired) and safe for concurrent use.

import (
	"context"
	"database/sql"
	"strings"
	"time"
)

// corpusAdapter is the narrow interface all per-corpus adapters implement.
type corpusAdapter interface {
	search(ctx context.Context, q string, limit int) ([]SearchHit, error)
}

// ─── artifacts adapter ────────────────────────────────────────────────────────

// artifactsSearcher queries the artifacts table for rows where title
// contains q (case-insensitive). Returns SearchHit{Corpus: "artifacts"} per row.
type artifactsSearcher struct {
	db *sql.DB
}

func (a *artifactsSearcher) search(ctx context.Context, q string, limit int) ([]SearchHit, error) {
	if a == nil || a.db == nil || q == "" {
		return nil, nil
	}
	like := "%" + q + "%"
	const sqlQ = `
SELECT id, title, mime_type, COALESCE(session_id, ''),
       COALESCE(project_id, ''), created_at
  FROM artifacts
 WHERE title LIKE ? OR mime_type LIKE ?
 ORDER BY created_at DESC
 LIMIT ?`
	rows, err := a.db.QueryContext(ctx, sqlQ, like, like, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close() //nolint:errcheck

	var hits []SearchHit
	for rows.Next() {
		var (
			id        string
			title     string
			mimeType  string
			sessionID string
			projectID string
			createdNs int64
		)
		if err := rows.Scan(&id, &title, &mimeType, &sessionID, &projectID, &createdNs); err != nil {
			return nil, err
		}
		ts := time.Unix(0, createdNs).UTC().Format(time.RFC3339Nano)
		snippet := title
		if mimeType != "" {
			snippet += " (" + mimeType + ")"
		}
		hits = append(hits, SearchHit{
			Corpus:    CorpusArtifacts,
			EntityID:  id,
			SessionID: sessionID,
			ProjectID: projectID,
			Snippet:   snippet,
			CreatedAt: ts,
		})
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return hits, nil
}

// ─── memory adapter ──────────────────────────────────────────────────────────

// memorySearcher searches the long-term memory store for chunks whose
// content contains q as a substring. Returns SearchHit{Corpus: "memory"}.
//
// Memory search requires HARNESS_MEMORY=on; when the lister is nil the
// adapter returns an empty slice without error (graceful degradation).
type memorySearcher struct {
	lister MemoryLister
}

const maxSnippetBytes = 256

func (m *memorySearcher) search(ctx context.Context, q string, limit int) ([]SearchHit, error) {
	if m == nil || m.lister == nil || q == "" {
		return nil, nil
	}
	chunks, err := m.lister.List(ctx)
	if err != nil {
		return nil, err
	}
	ql := strings.ToLower(q)
	var hits []SearchHit
	for _, chunk := range chunks {
		if !strings.Contains(strings.ToLower(chunk.Content), ql) {
			continue
		}
		snippet := chunk.Content
		if len(snippet) > maxSnippetBytes {
			snippet = snippet[:maxSnippetBytes] + "…"
		}
		var ts string
		if chunk.CreatedAt != nil {
			ts = chunk.CreatedAt.Format(time.RFC3339Nano)
		}
		hits = append(hits, SearchHit{
			Corpus:    CorpusMemory,
			EntityID:  chunk.ID,
			SessionID: chunk.SessionID,
			ProjectID: chunk.ProjectID,
			Snippet:   snippet,
			CreatedAt: ts,
		})
		if len(hits) >= limit {
			break
		}
	}
	return hits, nil
}

// ─── corpus adapter ───────────────────────────────────────────────────────────

// corpusNameSearcher queries the corpora table for rows where name or tag
// contains q (case-insensitive). Returns SearchHit{Corpus: "corpus"}.
type corpusNameSearcher struct {
	db *sql.DB
}

func (c *corpusNameSearcher) search(ctx context.Context, q string, limit int) ([]SearchHit, error) {
	if c == nil || c.db == nil || q == "" {
		return nil, nil
	}
	like := "%" + q + "%"
	const sqlQ = `
SELECT id, name, COALESCE(tag, ''), scope, COALESCE(scope_id, ''), created_at
  FROM corpora
 WHERE name LIKE ? OR tag LIKE ?
 ORDER BY created_at DESC
 LIMIT ?`
	rows, err := c.db.QueryContext(ctx, sqlQ, like, like, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close() //nolint:errcheck

	var hits []SearchHit
	for rows.Next() {
		var (
			id        string
			name      string
			tag       string
			scope     string
			scopeID   string
			createdNs int64
		)
		if err := rows.Scan(&id, &name, &tag, &scope, &scopeID, &createdNs); err != nil {
			return nil, err
		}
		_ = scopeID // reserved for future per-scope filtering
		ts := time.Unix(0, createdNs).UTC().Format(time.RFC3339Nano)
		snippet := name
		if tag != "" {
			snippet += " [" + tag + "]"
		}
		if scope != "" && scope != "global" {
			snippet += " (" + scope + ")"
		}
		hits = append(hits, SearchHit{
			Corpus:    CorpusCorpus,
			EntityID:  id,
			Snippet:   snippet,
			CreatedAt: ts,
		})
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return hits, nil
}

// ─── audit adapter ────────────────────────────────────────────────────────────

// AuditLister is the narrow read surface the audit adapter needs from the
// in-memory audit ring buffer. audit.API satisfies it.
type AuditLister interface {
	// ListEntries returns audit entries that match the given subjects.
	// The search adapter calls it with an empty filter and performs its
	// own substring matching on the Subject + Trailing fields so it does
	// not need to import the audit view types.
	ListEntriesForSearch(ctx context.Context, limit int) ([]AuditEntry, error)
}

// AuditEntry is the narrow subset of audit.Entry the search adapter reads.
type AuditEntry struct {
	ID        string
	Timestamp string
	Category  string
	Subject   string
	Trailing  string
}

// auditSearcher searches the in-memory audit ring buffer for entries
// whose Subject or Category contains q as a substring.
type auditSearcher struct {
	lister AuditLister
}

func (a *auditSearcher) search(ctx context.Context, q string, limit int) ([]SearchHit, error) {
	if a == nil || a.lister == nil || q == "" {
		return nil, nil
	}
	// Fetch a batch from the ring buffer; we do substring filter here.
	entries, err := a.lister.ListEntriesForSearch(ctx, limit*10)
	if err != nil {
		return nil, err
	}
	ql := strings.ToLower(q)
	var hits []SearchHit
	for _, e := range entries {
		subject := strings.ToLower(e.Subject)
		category := strings.ToLower(e.Category)
		if !strings.Contains(subject, ql) && !strings.Contains(category, ql) {
			continue
		}
		snippet := e.Subject
		if e.Category != "" {
			snippet = "[" + e.Category + "] " + snippet
		}
		hits = append(hits, SearchHit{
			Corpus:    CorpusAudit,
			EntityID:  e.ID,
			Snippet:   snippet,
			CreatedAt: e.Timestamp,
		})
		if len(hits) >= limit {
			break
		}
	}
	return hits, nil
}
