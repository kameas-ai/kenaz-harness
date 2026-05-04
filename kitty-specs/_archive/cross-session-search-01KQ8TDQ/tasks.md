# Tasks — Cross-session search (`cross-session-search-01KQ8TDQ`)

Eight work packages. Each WP merges to mission branch independently.

## WP01 — FTS5 migration + tokenizer + triggers
**Owner:** backend · **Dependencies:** none
**Scope:**
- `core/session/migrations_search.go` with migration `sessions/0311-search-fts` (next free version).
- DDL per plan §2.1: `messages_fts` external-content virtual table, `tokenize='unicode61 porter'`, three triggers (insert/update/delete).
- Register migration via `migrations.go::Migrations()` ordered list.
- Down migration: `DROP TRIGGER` × 3 + `DROP TABLE messages_fts`. Source `session_messages` untouched.

**Tests:**
- `migrations_search_test.go` — apply migration on fresh DB, assert table + triggers exist.
- Insert / update / delete row, assert `messages_fts` reflects change.
- Tokenizer assertion: `'running quickly'` MATCHes `'run'` (porter); `'identifier_with_underscores'` MATCHes `'identifier'` (unicode61).

## WP02 — `core/search/` package + RPC view
**Owner:** backend · **Dependencies:** WP01
**Scope:**
- New package `core/search/` with `Engine`, `SearchQuery`, `Results`, `Result`, `HighlightRange` per plan §2.2.
- `Engine.Search(ctx, q)` runs parameterized SQL, projects rows.
- `Engine.BootstrapIndex(ctx)` runs `INSERT INTO messages_fts(messages_fts) VALUES ('rebuild')` in goroutine; tracks `indexBuilding atomic.Bool`.
- New view package `core/rpc/views/search/` with `SearchAPI` interface, wire shapes, `managerAPI` adapter.
- Wire into `core/rpc/api.go::HarnessAPI` as `Search() searchview.SearchAPI` and into `bindings.go` as `Search_SearchMessages` / `Search_ListRecentModels`.
- Frontend client: add typed `search` namespace to `frontend/src/lib/harnessClient.ts`.

**Tests:**
- `core/search/engine_test.go` — fixture corpus of 100 mixed-role messages; AND/OR/NOT queries return expected ids.
- Filter coverage: project, date range, model, role, sessionID, IncludeArchived.
- RPC integration test in `core/rpc/api_test.go`.

## WP03 — Highlight offset mapping
**Owner:** backend · **Dependencies:** WP02
**Scope:**
- `core/search/highlight.go::extractHighlights(snippetWithDelims string) (plain string, ranges []HighlightRange)`.
- Use ASCII STX (0x02) / ETX (0x03) as FTS5 snippet delimiters.
- Cap matches at 8 per snippet, snippet width at 32 tokens.
- Engine.Search calls `snippet()` with these delimiters and runs extractor before returning.

**Tests:**
- Round-trip table-driven test: input snippet with delimiters → `(plain, ranges)`; ranges align character-for-character.
- Multi-byte UTF-8 fixture (CJK + emoji) — byte offsets stay correct.
- Property test: every range satisfies `0 ≤ Start < End ≤ len(plain)`; non-overlapping and sorted ascending.

## WP04 — Frontend SearchView modal + Cmd+F binding
**Owner:** frontend · **Dependencies:** WP02
**Scope:**
- `frontend/src/views/search/SearchView.vue` — three-pane layout per plan §2.5.
- New route `/search` registered in `frontend/src/main.ts`.
- Sidebar entry "Search" added in `frontend/src/shell/LeftRail.vue`.
- Cmd+F global keydown listener registered in `frontend/src/App.vue`. Short-circuits on `INPUT`/`TEXTAREA` targets.
- Renders modal as route — Esc closes.
- Input debounced 200ms, calls `client.search.searchMessages(...)`.
- Renders snippets via `renderSnippet(text, ranges)` helper using `h()` — NEVER `v-html`.

**Tests:**
- `SearchView.spec.ts`: input → debounced API call; results render correct hit count.
- Snippet rendering: `text="hello world"` + range `[6,11]` produces `<span>hello </span><mark>world</mark>`.
- Cmd+F shortcut test: simulate keydown on `<body>` → modal shown; on `<input>` → modal not shown.

## WP05 — Result navigation + focus highlight pulse
**Owner:** frontend · **Dependencies:** WP04
**Scope:**
- Result row click → `router.push('/sessions/' + sessionId + '?focus=' + messageId)`.
- `frontend/src/views/sessions/SessionsView.vue`: read `route.query.focus` on mount; scroll target message into view; apply `pulse-once` class for 3s.
- Add `pulse-once` keyframe animation in `frontend/src/styles/global.css`.
- Modal closes automatically when navigating to a session result.

**Tests:**
- E2E flow: open search modal, type token, click first result → URL contains `focus=<id>`; element has pulse class for 3s.
- Snapshot of CSS keyframe.

## WP06 — Filter sidebar + URL state
**Owner:** frontend · **Dependencies:** WP04
**Scope:**
- Filter sidebar component: project picker, date-range, model multi-select, role checkboxes, "Hide tool outputs", "Include archived".
- Filter state ↔ URL query params round-trip. On change, `router.replace` with new query; on mount, parse query → state.
- Recently-searched panel below empty-state input: last 10 unique queries from `localStorage`.
- "Hide tool outputs" implemented client-side as post-filter on result list.

**Tests:**
- Round-trip filter URL: `?q=foo&roles=user,assistant&from=2026-01-01` → state → request → state matches.
- localStorage recent-search add/clear/dedupe.
- "Hide tool outputs" filters role=tool rows from rendered list while keeping them in underlying `hits` array.

## WP07 — Settings dial + audit + privacy
**Owner:** backend + frontend · **Dependencies:** WP02
**Scope:**
- Add `Settings.SearchIndexDisabled bool` (inverted persistence) + `SearchIndexEnabled() bool` helper.
- `SettingsStore` gains `LoadSearchIndex()` / `SaveSearchIndex()`.
- `core/search.Engine` reads dial on startup; if disabled, drops triggers + returns `{disabled: true}` envelopes.
- Toggle re-creates triggers + kicks `BootstrapIndex` rebuild.
- New event kind `KindSearchExecuted` registered.
- Search.SearchMessages emits audit event with `{query_hash, result_count, filters_applied, took_ms}`.
- Frontend: SettingsView gains "Search indexing" toggle row.

**Tests:**
- Toggle off → toggle on flow.
- Audit assertion: emit search; read back via `event.Reader`; payload has `query_hash` not `query`.
- Hash determinism.

## WP08 — Integration tests + smoke harness
**Owner:** backend + frontend · **Dependencies:** WP01-WP07
**Scope:**
- End-to-end test corpus: 1k synthetic messages across 5 sessions / 2 projects / 3 models / 4 roles.
- `core/search/integration_test.go` — full stack: storage → engine → snippet extraction. Asserts NFR-001 (<200ms p95), NFR-002 (<30% disk overhead).
- Cascade test: delete session → `messages_fts` rows for its messages are gone.
- Compaction race test: kick compaction sweep mid-search; no orphan FTS rows + no panic.
- Vitest E2E (`SearchView.spec.ts`): full flow — type → debounce → result → click → focus pulse → back → re-open with same URL → state round-trips.
- Manual smoke checklist (plan §4) executed.

**Tests** (gate criteria):
- All integration tests green.
- p95 query latency < 200ms on 1k-message corpus.
- `messages_fts` disk size < 30% of `session_messages` size.
- Manual smoke checklist all 5 steps green.

## Sequencing diagram

```
WP01 (migration + triggers)
   │
   ▼
WP02 (core/search package + RPC) ────────┬──────────┐
   │                                     │          │
   ▼                                     ▼          ▼
WP03 (highlight offsets)           WP04 (modal + Cmd+F)   WP07 (settings + audit)
   │                                     │          │
   │                                     ├──► WP05 (navigation + pulse)
   │                                     └──► WP06 (filters + URL state)
   │                                                ▲
   └────────────────────────────────────────────────┘
                         │
                         ▼
                       WP08 (integration tests + perf gate)
```

Critical path: WP01 → WP02 → WP04 → WP05 → WP08. WP03, WP06, WP07 parallelize off WP02. WP08 gates merge.
