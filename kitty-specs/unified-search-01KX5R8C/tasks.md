# Work Packages — Unified Cross-Corpus Search

## WP00 — Spec scaffold

**Files**: `kitty-specs/unified-search-01KX5R8C/{meta.json,spec.md,plan.md,tasks.md}`  
**Commit**: `docs(unified-search): scaffold plan.md + tasks.md + meta.json (closes spec-debt)`

Creates the mission governance artifacts that were missing before implementation began.

---

## WP01 — Extend core search types

**Files**: `core/rpc/views/search/api.go`  
**Commit**: `feat(search): WP01 — extend SearchHit + SearchFilters for multi-corpus`

- Add `Corpus string` to `SearchHit` (one of `messages|artifacts|memory|corpus|audit`).
- Add `Corpora []string` to `SearchFilters` (empty = all corpora enabled).
- Add `UnifiedSearch(ctx, query, filters)` to `SearchAPI` interface.
- Keep existing `Search()` method unchanged (backward-compat).

---

## WP02 — Per-corpus adapters

**Files**: `core/rpc/views/search/adapters.go`, `core/rpc/views/search/adapters_test.go`  
**Commit**: `feat(search): WP02 — artifacts / memory / corpus / audit adapters`

Implement four private adapter types in `core/rpc/views/search/adapters.go`:

- `artifactsSearcher` — queries `artifacts` table for rows where title LIKE `%q%`.
  Uses `sqlQuerier.QueryContext`. Returns `SearchHit{Corpus: "artifacts", ...}`.
- `memorySearcher` — calls `memory.Store.List()` then filters chunks where
  `strings.Contains(content, q)`. Truncates snippet to 256 chars. Returns
  `SearchHit{Corpus: "memory", ...}`. No-ops when store is nil.
- `corpusSearcher` — queries `corpora` table for rows where name/tag LIKE `%q%`.
  Returns `SearchHit{Corpus: "corpus", ...}`.
- `auditSearcher` — calls `log.Backend.SearchFTS(ctx, q, "", nil, limit)` and maps
  each Row to a `SearchHit{Corpus: "audit", ...}`. No-ops when backend is nil.

Each adapter returns `([]SearchHit, error)`. They are safe for concurrent use.

---

## WP03 — UnifiedSearch + Bindings wiring

**Files**: `core/rpc/views/search/impl.go`, `core/rpc/bindings.go`, `core/rpc/api.go`  
**Commit**: `feat(search): WP03 — UnifiedSearch method + RPC wiring`

- Add `UnifiedSearch` to `managerAPI` in `impl.go`: fan out to all adapters in
  parallel goroutines (using `errgroup`-style coordination), respect `Corpora` filter,
  merge results, score, deduplicate.
- Add `Search_Unified(query string, filters SearchFilters) ([]SearchHit, error)` to
  `Bindings` in `bindings.go` (Wails-bound).
- Wire the four adapters onto `managerAPI` via a `Config` extension in `api.go`.
- Update `stubSearch.UnifiedSearch` to return `nil, nil`.

---

## WP04 — Frontend: types + client + palette source badges

**Files**:  
- `frontend/src/lib/harnessClient.ts`  
- `frontend/src/components/search/SearchPalette.vue`  
- `frontend/src/components/search/SearchModal.vue`  
- `frontend/src/components/search/__tests__/SearchPalette.test.ts`  
**Commit**: `feat(search): WP04 — frontend unified search + source badges`

- Add `corpus?: string` to `SearchHit` TypeScript interface.
- Add `corpora?: string[]` to `SearchFilters`.
- Add `unified(query: string, filters?: SearchFilters): Promise<SearchHit[]>` to
  `SearchClient` interface.
- Wire `search.unified` in the runtime client (calls `b().Search_Unified(...)`).
- Add stub for `search.unified` in the fake client (`async () => []`).
- `SearchPalette.vue`: call `client.search.unified()` instead of `sessions()`, render
  corpus badge chip on each hit row (`messages`, `artifacts`, `memory`, `corpus`, `audit`).
- `SearchModal.vue`: same corpus badge + add corpus filter chip strip above results.
- Update `SearchPalette.test.ts` to cover the corpus badge rendering path.
