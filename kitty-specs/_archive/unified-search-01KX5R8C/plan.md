# Implementation Plan — Unified Cross-Corpus Search

## Approach

The existing `core/rpc/views/search` package is kept intact (backward-compatible). We
extend it with a second interface method `UnifiedSearch` and four new private adapter
types. The frontend gains a `search.unified()` client method while `search.sessions()`
remains available for callers that only need the messages corpus.

## Dependency Order

```
WP00 — spec scaffold (this document + tasks.md + meta.json)
  ↓
WP01 — extend SearchHit + SearchFilters types (backend)
  ↓
WP02 — per-corpus adapters (artifacts, memory, corpus, audit)
  ↓
WP03 — UnifiedSearch method + Bindings + api.go wiring
  ↓
WP04 — frontend: types + client + palette source badges + filter chips
```

## Conflict Zones

Per CLAUDE.md § "Shared-file conflict zones" the files most at risk of parallel-agent
conflicts in this mission are:

- `core/rpc/bindings.go` — new `Search_Unified` method
- `core/rpc/api.go` — wiring the new unified method
- `frontend/src/lib/harnessClient.ts` — new `unified()` method + type extensions
- `frontend/src/lib/types.ts` — if `SearchHit` or `SearchFilters` land there

Resolution: this mission adds only new exported symbols; no existing symbol is renamed.
Merge conflicts are resolved additively.

## Out of Scope

- Date-range filtering of non-message corpora (deferred)
- Semantic/vector search for messages (remains BM25)
- Corpus chunk content search (name/tag only for v0.10.0)
- Performance benchmark suite (NFR-001/NFR-002)
