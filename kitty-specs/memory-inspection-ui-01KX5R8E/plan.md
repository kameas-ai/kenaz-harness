# plan.md — Memory inspection UI (capstone, v0.10.0)

## Branch contract

- **Branch**: `worktree-agent-a1f61c8ba7d7ca9eb` (merges into `release/v0.10.0`)
- **Base**: `release/v0.10.0` (= current `main` at 9aba0fe).
- **Merge gate**: all WPs green; `go build ./core/...` clean; `go test -count=1 -race -short ./core/memory/... ./core/rpc/views/memory/...` green; frontend vitest clean.

## Already shipped (cherry-picks — do NOT re-implement)

| Feature | Shipped in | PR |
|---|---|---|
| §2.4 Memory health dashboard (`MemoryHealthPanel.vue`, `Memory_HealthSnapshot`) | v0.5.3 | #89 |
| §2.5 Prune preview drill-down (`PrunePreviewModal.vue`, `PrunePreview.Rows`) | v0.5.3 | #89 |
| §2.7 LegendBar capture-rate widget (`Memory_CaptureRate`, `CaptureRateSnapshot`) | v0.5.4 | #93 |
| §2.6 partial — per-chunk Kind/Weight/TurnID fields on wire shape | v0.9.0 | #118 |
| Narrative layer `MarkImportant`, `NarrativeMetricsForChunk`, failed-job surface | v0.9.0 | #118 |

## Capstone scope (this mission, v0.10.0)

### WP01 — Scaffold spec artifacts
- Create `kitty-specs/memory-inspection-ui-01KX5R8E/{meta.json,plan.md,tasks.md}`.
- Copy `spec.md` from main-repo working tree into the worktree (spec was untracked).
- Commit: `docs(memory-inspector): scaffold plan.md + tasks.md + meta.json (capstone scope)`.

### WP02 — Retrieval history ring buffer + `Memory_LastRetrieval` RPC
Backend-only. The retriever currently returns snippets but discards all context about
the call. This WP captures that context in a bounded in-memory ring buffer and exposes
it via a new RPC.

Files:
- `core/memory/retrieval_history.go` (new) — `RetrievalRecord`, `RetrievalHistory` ring buffer (last 200 per session, 10k total), `GlobalRetrievalHistory()` accessor.
- `core/memory/retriever.go` — after a successful `retrieve()` call, push a `RetrievalRecord` to `GlobalRetrievalHistory()`.
- `core/rpc/views/memory/api.go` — add `RetrievalReport`, `ScoredChunk` types + `LastRetrieval(sessionID string)` to `MemoryAPI`.
- `core/rpc/views/memory/impl.go` — implement `LastRetrieval`, `EmbeddingProbe`.
- `core/rpc/views/memory/impl_test.go` — ring buffer bounds, concurrency.

### WP03 — `Memory_EmbeddingProbe` RPC
Lets the user type arbitrary text and see which chunks would be retrieved, with scores.

Files:
- `core/rpc/views/memory/api.go` — add `EmbeddingProbe(query string, limit int) ([]ScoredChunk, error)` to `MemoryAPI`.
- `core/rpc/views/memory/impl.go` — implement: embed query → `store.Query` → return sorted `ScoredChunk` slice.
- Tests in `impl_test.go`.

### WP04 — `Memory_ResummarizeChunk` RPC
Per-chunk manual re-run of narrative synthesis. Requires the Promoter from the
narrative layer.

Files:
- `core/rpc/views/memory/api.go` — add `ResummarizeChunk(chunkID string) (Chunk, error)` to `MemoryAPI`.
- `core/rpc/views/memory/impl.go` — look up chunk; if `TurnID != ""` re-enqueue to Promoter; else run extractive fallback inline; return updated chunk.
- `core/rpc/views/memory/api.go` — add `Resummary` field to `Config` (optional `narrative.Promoter` pointer).
- Tests in `impl_test.go`.

### WP05 — `Memory_GetChunkProvenance` + provenance wire types
Full audit chain for a chunk.

Files:
- `core/rpc/views/memory/api.go` — add `ChunkProvenance`, `GetChunkProvenance(id string) (ChunkProvenance, error)` to `MemoryAPI`.
- `core/rpc/views/memory/impl.go` — implement: load chunk, load narrative metrics if available, compute provenance fields.
- Tests in `impl_test.go`.

### WP06 — Bindings + frontend types
Wire all four new RPCs through the Wails bindings layer and the typed frontend client.

Files:
- `core/rpc/bindings.go` — four new `Memory_*` methods.
- `frontend/src/lib/types.ts` — `RetrievalReport`, `ScoredChunk`, `ChunkProvenance`.
- `frontend/src/lib/harnessClient.ts` — `WailsBindingsLike` extensions + `MemoryClient` method signatures + implementation + fake stub.
- `frontend/wailsjs/go/rpc/Bindings.{js,d.ts}` — generated stubs (hand-authored to match Wails codegen format).

### WP07 — Frontend: Retrieval Inspector tab + Embedding Probe
Extends `MemoryView.vue` with two new tabs and new interactive sections.

Files:
- `frontend/src/views/memory/MemoryView.vue` — new `retrieval` tab showing `RetrievalReport`, embedding probe input, loading states, empty states.
- `frontend/src/views/memory/__tests__/MemoryView.spec.ts` — test retrieval tab render + embedding probe.

### WP08 — Frontend: Provenance Drawer
Slide-in drawer for any chunk row in the Chunks tab.

Files:
- `frontend/src/views/memory/ProvenanceDrawer.vue` (new) — receives `ChunkProvenance`, renders all fields.
- `frontend/src/views/memory/MemoryView.vue` — add "Provenance" row action, wire drawer open/close.
- `frontend/src/views/memory/__tests__/ProvenanceDrawer.spec.ts` (new).

## Architecture notes

### `GlobalRetrievalHistory` (WP02)
Process-scoped singleton (same pattern as `GlobalCaptureTracker`). Ring buffer is
bounded: last 200 records per session-ID key, hard cap 10k rows total across all sessions.
Records survive in-memory only (not persisted across restarts — acceptable per NFR-002 which only applies to `retrieval_history` table, and we're using in-memory only for the capstone).

### `EmbeddingProbe` (WP03)
Directly calls `store.Query` with the embedder result. Returns up to `limit` (default 10,
max 50) `ScoredChunk` values sorted by similarity descending. Never filters by session —
always global view (debugging use case).

### `ResummarizeChunk` (WP04)
Rate-limited: 1 call per chunk per 60s (tracked in a `sync.Map[string]time.Time`).
For narrative chunks with `TurnID != ""`: re-enqueues to Promoter job queue (async;
returns current chunk immediately). For raw/legacy chunks: runs `ExtractiveBuilder.BuildTurnFallback`
inline and writes the result as a new chunk (replacing the old one atomically via Delete+Add).

### `GetChunkProvenance` (WP05)
Aggregates data already present across the store + narrative metrics + journal. No new
SQL tables needed. Returns: source turn, hook boundary (from `Source` field), retrieval counts
from `NarrativeMetrics.Retrievals`, citation count, pin count, promotion score, scope path,
embedding info (kind/dims). Empty fields are zero-value safe.
