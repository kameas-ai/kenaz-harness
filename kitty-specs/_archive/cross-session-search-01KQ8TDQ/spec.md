# Spec — Cross-session search (`cross-session-search-01KQ8TDQ`)

**Status**: draft · **Owner**: alecfeeman

## 1. Why

Today users can scroll the sidebar to find a session by title (or auto-titled first message). They cannot search by **content**. ChatGPT, Claude, Cursor all have this; we don't. As session count grows past ~50, the sidebar becomes useless for "where was that conversation about X?"

## 2. Goals

- Full-text search across all session messages.
- Filter by date range, project, tag, model, role.
- Search results show snippet with highlighted match + clickable link to the session at that turn.
- Fast: < 200ms for typical queries on a 10k-message corpus.
- Local-first: no external indexer; SQLite FTS5.

## 3. Functional requirements

| ID | Requirement | Status |
|---|---|---|
| FR-001 | New SQLite FTS5 virtual table `messages_fts` shadowing `session_messages`. Migration adds the FTS table + triggers (insert/update/delete) keeping it in sync with the source. | proposed |
| FR-002 | New `core/search/` package with `Search(ctx, query SearchQuery) (Results, error)`. `SearchQuery{Text, ProjectID?, SessionID?, DateFrom?, DateTo?, Models[]?, Roles[]?, Limit int, Offset int}`. | proposed |
| FR-003 | RPC `Search.SearchMessages(ctx, query) (Results, error)` returning per-result `{message_id, session_id, session_title, project_name, role, model, snippet_html, created_at}`. Snippet is FTS5's `snippet()` function output with `<mark>` tags around hits. | proposed |
| FR-004 | New `frontend/src/views/search/SearchView.vue` route at `/search`. Cmd+F focuses the search input from any screen. Empty input shows recent searches (localStorage). Typing filters live with 200ms debounce. | proposed |
| FR-005 | Result list: each row clickable, navigates to `/sessions/<session_id>?focus=<message_id>` and scrolls to the message with the match highlighted briefly (3s pulse). | proposed |
| FR-006 | Filters sidebar in SearchView: project picker, date-range picker, model multi-select, role checkboxes (user / assistant / system / tool). Filters reflected in URL query params for shareable links. | proposed |
| FR-007 | FTS5 query parsing: passes through `phrase queries`, `AND`/`OR`/`NOT`, and `column:value` filters (`role:user OR role:assistant`). Plain text falls through to the default tokenizer with prefix matching. | proposed |
| FR-008 | Recently-searched list: stores last 10 queries in localStorage; clickable to re-run. Cleared by user from the SearchView UI. | proposed |
| FR-009 | Privacy: indexed content respects `session.kind` — workflow_run sessions and archived (compacted) messages are excluded from default search. Toggle "Include archived" surfaces them. | proposed |
| FR-010 | Audit kind `KindSearchExecuted` (lightweight; payload = `{query_hash, result_count, filters_applied}`; never the raw query text — privacy hint). | proposed |
| FR-011 | Settings dial `Settings.SearchIndexEnabled bool` (default true). When false, FTS triggers stop firing and search returns empty with "indexing disabled" hint. Useful for users who want zero search index disk usage. | proposed |

## 4. Non-functional requirements

| ID | Requirement | Threshold |
|---|---|---|
| NFR-001 | Query latency | < 200ms p95 on a 10k-message corpus |
| NFR-002 | Index disk overhead | < 30% of source table size |
| NFR-003 | Initial index build (on migration) | < 30s for 10k messages on dev hardware; runs in a background goroutine, search shows "indexing..." until done |

## 5. Constraints

| ID | Constraint |
|---|---|
| C-001 | Local-first: SQLite FTS5 only; no Elasticsearch / Meilisearch / external service. |
| C-002 | DIRECTIVE_001: frontend goes through `core/rpc/views/search`. |
| C-003 | Index built incrementally via triggers; no batch re-index after the initial migration build. |
| C-004 | The query string is NOT stored in audit (only its hash). Search is privacy-sensitive. |

## 6. Locked open questions

- **Q1 = D**: Index ALL roles (user, assistant, system, tool). Tool outputs are exactly what users can't find via scrollback (they're often collapsed). Frontend filter "Hide tool outputs" toggle on the results list lets users narrow at search time without reindexing.
- **Q2 = C**: FTS5 tokenizer chain = `unicode61 + porter`. Unicode for code-identifier and non-English content; Porter stemmer for English morphology matching ("running" matches "run"). Idiomatic FTS5 combo.
- **Q3 = B**: Server returns plain text + character-offset ranges for highlights; frontend layers `<mark>` spans over escaped text. NO `v-html` in search results; cleanest security separation. Slightly more frontend code, no double-sanitization concern.
- **Q4 = C**: Search opens via Cmd+F (power-user keyboard) AND a small "Search" link in the sidebar (discoverability for new users). Same modal in both cases — modal renders centered overlay with input + result list + filter sidebar.

## 7. Success criteria

- "find that conversation where I mentioned `migration 0310`" returns the right session within 200ms.
- Filters narrow correctly (project + date range + role).
- Result click navigates to the message + highlights it.
- Disabling the dial stops indexing without breaking existing data.
