# Unified Cross-Corpus Search Backend

**Mission ID**: unified-search-01KX5R8C  
**Target**: v0.10.0  
**Status**: In Progress

---

## Context

The Cmd+K palette UI shipped in v0.5.6 (commit 84afd30 — "fix(ui): relocate search to
top-right ⌘K palette") and the Cmd+F modal shipped with full filter UI. Both components
call `client.search.sessions()` which hits `Bindings.Search_Sessions` → the
`core/rpc/views/search` backend → SQLite FTS5 over `messages_fts`. That single-source
backend is what this mission replaces with a five-source unified view.

---

## Goal

Ship a cross-corpus search backend that queries all five harness data stores in parallel
and returns a unified, ranked result list through the existing palette RPC seam. The
frontend receives a `corpus` source badge per hit so the palette can render source chips
and per-source filter toggles.

---

## Corpora

| # | Name | Data Source | Search Mechanism |
|---|------|------------|-----------------|
| 1 | **messages** | `session_messages` FTS5 | Already shipped (keep) |
| 2 | **artifacts** | `artifacts` table | `LIKE`/`GLOB` on `title` + `mime_type` |
| 3 | **memory** | `core/memory.Store` | `List()` + substring filter on content |
| 4 | **corpus** | `core/corpus.Manager` | Name/tag substring on `corpus` rows |
| 5 | **audit** | `core/event/log.Backend.SearchFTS` | FTS over redacted payload text |

---

## Key Surface Changes

### Backend (`core/rpc/views/search/`)

1. **Extended `SearchHit`** — add `Corpus string` field (`"messages"`, `"artifacts"`,
   `"memory"`, `"corpus"`, `"audit"`).
2. **Extended `SearchFilters`** — add `Corpora []string` to allow per-source toggling.
3. **`UnifiedSearch` method on `SearchAPI`** — fans out to all enabled adapters in
   parallel, merges & ranks results, deduplicates by `(corpus, id)`.
4. **New `Bindings.Search_Unified` method** in `core/rpc/bindings.go`.
5. **Per-corpus adapters** — `artifactsSearcher`, `memorySearcher`, `corpusSearcher`,
   `auditSearcher` live in `core/rpc/views/search/`.

### Frontend

6. **Extended `SearchHit` TypeScript type** — add `corpus` field.
7. **Extended `SearchClient`** — add `unified(query, filters?)` method.
8. **`SearchPalette.vue`** — source badges on each hit row; filter chips for corpus selection.
9. **`SearchModal.vue`** — same badge + filter treatment.

---

## Scoring

Results within each corpus are returned ordered by the corpus adapter's native rank.
The unified merge orders: messages (highest weight, BM25 ranked by SQLite) → artifacts
→ corpus → memory → audit. Score is a float32 in [0,1]; messages get 1.0−position/N,
other corpora 0.5−position/N. Deduplication is by `(corpus, entityID)`.

---

## Privacy Constraints

- Memory chunk raw content crosses the RPC boundary in truncated form (≤256 chars
  snippet). No embeddings are included.
- Audit payloads are already redacted at write time; `SearchFTS` returns the redacted
  form only.
- The raw query string MUST NOT appear in audit emission (hash only, per WP07 of the
  messages backend).

---

## Feature Gate

`HARNESS_MEMORY=on` is required for memory results. When memory is not configured,
the memory adapter returns an empty slice without error. All other adapters are
unconditionally enabled when the underlying store is wired.

---

## Build + Verify

```bash
go build ./core/...
go test -count=1 -race -short ./core/search/... ./core/rpc/...
cd frontend && ./node_modules/.bin/vitest run --reporter=basic
```
