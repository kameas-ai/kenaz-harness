# Plan — Cross-session search (`cross-session-search-01KQ8TDQ`)

**Status**: draft · **Owner**: alecfeeman · **Branch**: `mission/cross-session-search-01KQ8TDQ`

## 1. Branch contract

- Single feature branch `mission/cross-session-search-01KQ8TDQ`, branched off `main`.
- All migrations land in the `sessions` block (300-399). The next free version is **0311** (post 0310 compaction).
- DIRECTIVE_001 enforced: frontend talks to `core/` only via `core/rpc/`. The new view package is `core/rpc/views/search`.
- No new external dependencies. SQLite FTS5 is already linked.
- Feature is gated behind `Settings.SearchIndexEnabled` (default true; FR-011). Disabling MUST NOT break existing data.
- Privacy CI: query strings NEVER hit the audit log. Only a sha256 hash + result_count + filters_applied (FR-010, C-004).

## 2. Architecture

### 2.1 SQLite FTS5 schema (migration `sessions/0311-search-fts`)

External-content FTS5 virtual table mirrors `session_messages.content`. Tokenizer locked: `unicode61 + porter`.

```sql
CREATE VIRTUAL TABLE IF NOT EXISTS messages_fts USING fts5(
    content,
    role UNINDEXED,
    session_id UNINDEXED,
    created_at UNINDEXED,
    content='session_messages',
    content_rowid='rowid',
    tokenize='unicode61 porter'
);

CREATE TRIGGER IF NOT EXISTS session_messages_ai AFTER INSERT ON session_messages BEGIN
    INSERT INTO messages_fts(rowid, content, role, session_id, created_at)
    VALUES (new.rowid, new.content, new.role, new.session_id, new.created_at);
END;

CREATE TRIGGER IF NOT EXISTS session_messages_ad AFTER DELETE ON session_messages BEGIN
    INSERT INTO messages_fts(messages_fts, rowid, content, role, session_id, created_at)
    VALUES ('delete', old.rowid, old.content, old.role, old.session_id, old.created_at);
END;

CREATE TRIGGER IF NOT EXISTS session_messages_au AFTER UPDATE ON session_messages BEGIN
    INSERT INTO messages_fts(messages_fts, rowid, content, role, session_id, created_at)
    VALUES ('delete', old.rowid, old.content, old.role, old.session_id, old.created_at);
    INSERT INTO messages_fts(rowid, content, role, session_id, created_at)
    VALUES (new.rowid, new.content, new.role, new.session_id, new.created_at);
END;
```

External-content table keeps disk overhead under NFR-002's 30% target. Triggers cover insert/update/delete (compaction does both — `compacted_into_id` updates plus archive deletes). Q1 = D — index ALL roles; UI exposes "Hide tool outputs" toggle. Initial-build goroutine — first `storage.Open` after migration runs `INSERT INTO messages_fts(messages_fts) VALUES ('rebuild');` in background.

### 2.2 `core/search/` package

```go
package search

type SearchQuery struct {
    Text         string
    ProjectID    string
    SessionID    string
    DateFrom     time.Time
    DateTo       time.Time
    Models       []string
    Roles        []string
    IncludeArchived bool
    Limit        int
    Offset       int
}

type Result struct {
    MessageID     string
    SessionID     string
    SessionTitle  string
    ProjectName   string
    Role          string
    Model         string
    SnippetText   string  // plain text, NEVER HTML (Q3 = B)
    Highlights    []HighlightRange
    CreatedAt     time.Time
}

type HighlightRange struct {
    Start int  // byte offset in SnippetText (UTF-8)
    End   int  // exclusive
}

type Results struct {
    Hits          []Result
    Total         int
    IndexBuilding bool
}

type Engine struct {
    db dbadapter.DB
}

func NewEngine(db dbadapter.DB, opts ...Option) *Engine
func (e *Engine) Search(ctx context.Context, q SearchQuery) (Results, error)
func (e *Engine) BootstrapIndex(ctx context.Context) error
```

Query path: parse Text against FTS5 grammar → build SQL with filters → pull snippet via `snippet()` with token positions → convert to plain-text + offset ranges.

### 2.3 Highlight offset mapping

FTS5's `snippet()` returns text with caller-supplied delimiters. Backend strategy:

1. Call `snippet(messages_fts, 0, char(2), char(3), '…', 32)` — uses ASCII STX/ETX (0x02/0x03) as delimiters.
2. Walk returned string byte-by-byte:
   - On STX, record `Start := len(out)`.
   - On ETX, record `End := len(out)` and append `HighlightRange{Start, End}`.
   - Otherwise append the byte to `out`.
3. Return `(out_string, []HighlightRange)`. NO HTML, NO `<mark>` on the wire.

Frontend escapes plain text and overlays `<mark>` spans at offsets (Q3 = B).

### 2.4 RPC `Search.SearchMessages`

New view package `core/rpc/views/search`:

```go
type SearchHit struct {
    MessageID    string
    SessionID    string
    SessionTitle string
    ProjectName  string
    Role         string
    Model        string
    SnippetText  string
    Highlights   []Highlight
    CreatedAt    string  // RFC3339Nano UTC
}

type SearchResponse struct {
    Hits          []SearchHit
    Total         int
    IndexBuilding bool
}

type SearchAPI interface {
    SearchMessages(ctx context.Context, req SearchRequest) (SearchResponse, error)
    ListRecentModels(ctx context.Context) ([]string, error)
}
```

Wails binding name: `Search_SearchMessages`.

### 2.5 Frontend SearchView modal

New component `frontend/src/views/search/SearchView.vue`. Mounted as route `/search` AND modal overlay (Q4 = C — Cmd+F + sidebar link both open same modal).

Layout: centered overlay (z-index above nav), 80vw × 80vh, three-pane:
- Header: input field (autofocus, debounced 200ms).
- Left filter sidebar: project picker, date-range, model multi-select, role checkboxes, "Hide tool outputs" toggle, "Include archived" toggle, "Recent searches" list.
- Right result list: virtual-scroll, each row clickable.

Cmd+F binding: registered globally in `App.vue`. Short-circuits on `INPUT`/`TEXTAREA` targets.

Sidebar link: new entry in `LeftRail.vue` between "Sessions" and "Tools".

Highlight rendering — server returns `(snippetText, highlights[])`; frontend walks offsets and emits sequence of escaped-text spans + `<mark>` spans via `h()` (NEVER `v-html`).

### 2.6 URL query params + click-through navigation

Filters round-trip through URL: `?q=…&project=…&from=…&to=…&models=foo,bar&roles=user,assistant&archived=1&offset=20`.

Result click navigates to `/sessions/<session_id>?focus=<message_id>`. Sessions view reads `route.query.focus` on mount, scrolls into view, triggers 3-second pulse animation.

### 2.7 Recently-searched + Include-archived

- Recent: last 10 unique queries in `localStorage` under `harness:search:recent`.
- Include archived toggle posts `includeArchived: true` — backend drops `WHERE archived_at IS NULL` predicate.

### 2.8 Privacy + audit

- New event kind `KindSearchExecuted Kind = "search.executed"`.
- Payload: `{query_hash: <sha256-hex>, result_count: int, filters_applied: []string, took_ms: int}`. Raw query NEVER persisted.
- Hash uses `crypto/sha256` over lowercased trimmed query bytes; first 16 hex chars on the wire.

### 2.9 Settings dial `SearchIndexEnabled`

- New field `Settings.SearchIndexEnabled bool` in `core/rpc/views/settings/api.go`. Persisted as `SearchIndexDisabled bool` (inverted convention) defaulting false.
- Effect when disabled: migration still runs (idempotent); triggers DROP at runtime; re-enabling re-creates triggers and runs `('rebuild')`.

## 3. Risk register

| # | Risk | Mitigation |
|---|------|------------|
| R1 | FTS5 trigger overhead on every chat-message insert | External-content table — INSERT just passes the rowid. < 500µs/row in event-log precedent. |
| R2 | Initial index build on huge histories blocks UI | Background goroutine on `BootstrapIndex`; `IndexBuilding` signal lets UI render "indexing…" pill. |
| R3 | Snippet quality on code-heavy content | unicode61 breaks on punctuation; combined with porter, `migration_0310` matches `migrations 0310`. |
| R4 | Tokenizer chain mismatch with stored content | Locked at migration time; changing it requires new migration + full rebuild. |
| R5 | Cmd+F collision with browser find / chat composer | Listener short-circuits on `INPUT`/`TEXTAREA` targets. |
| R6 | Privacy leak — query string in audit | `query_hash` only on payload; e2e test asserts audit rows never contain raw query. |
| R7 | XSS via reflected snippet HTML | Q3 = B locks plain-text-plus-offsets; `renderSnippet` uses `h()` not `v-html`. |
| R8 | Disk overhead exceeds NFR-002 (30%) | External-content table avoids content duplication. |
| R9 | Compaction race — archived rows churn the index | Triggers handle UPDATE; IncludeArchived toggle exposes them. |
| R10 | Project cascades — orphaned messages_fts rows on session delete | `session_messages` has `ON DELETE CASCADE`; AFTER DELETE trigger fires per row. |

## 4. Rollout

WP01-WP07 land sequentially behind `SearchIndexEnabled` dial (default ON; flippable). WP08 gates merge — full integration suite must be green.

### 5-step manual smoke

1. **Index build** — fresh launch on corpus ≥ 1k messages: confirm "indexing…" pill appears briefly, then disappears within 5s; query after pill returns hits.
2. **Cmd+F** — anywhere in app, press Cmd+F: modal opens, input autofocused. Press Esc: closes.
3. **Sidebar link** — click "Search" in LeftRail: same modal opens at `/search`.
4. **Click-through** — search for token, click result: navigates to session, message scrolls into view and pulses ~3s. Highlights as `<mark>` spans (no broken HTML).
5. **Disable dial** — Settings → toggle SearchIndexEnabled OFF, run query: shows "Search index is disabled" hint; toggle ON, confirm rebuild kicks off.
