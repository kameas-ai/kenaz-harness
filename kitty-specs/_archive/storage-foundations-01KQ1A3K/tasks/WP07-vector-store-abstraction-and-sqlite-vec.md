---
work_package_id: "WP07"
title: "VectorStore abstraction and sqlite-vec default backend"
dependencies:
  - "WP01"
  - "WP03"
planning_base_branch: "feat/storage-foundations-01KQ1A3K"
merge_target_branch: "main"
branch_strategy: "Planning artifacts were generated on feat/storage-foundations-01KQ1A3K; completed changes must merge back into main via squash merge per charter Branch Strategy."
subtasks:
  - "T001: define VectorStore, Collection, CollectionSpec, Embedding, Match, Filter, Metric, Quantization"
  - "T002: pin sqlite-vec extension version + load it into the libSQL connection at Open"
  - "T003: implement sqlite-vec backend Insert/BatchInsert/Search/Delete/Reindex/Stats/Close"
  - "T004: define Reindex contract (rebuild index from authoritative source via consumer-supplied iterator)"
  - "T005: contract-test suite reusable across backends"
  - "T006: 100k-vector NFR-003 latency bench"
phase: "Phase 7 - Vector store abstraction + sqlite-vec"
assignee: ""
agent: ""
shell_pid: ""
history:
  - timestamp: "2026-04-25T00:00:00Z"
    agent: "system"
    action: "Prompt generated via /spec-kitty.tasks"
---

# Work Package Prompt: WP07 – VectorStore abstraction and sqlite-vec default backend

## Goal

Define the `VectorStore` abstraction (`OpenCollection`, `Insert`, `BatchInsert`, `Search`, `Delete`, `Reindex`, `Stats`, `Close`) and ship the default sqlite-vec backend that loads as an extension into the same libSQL connection. Provide a backend-neutral contract test that all future backends (LanceDB in WP08, chromem-go in WP09) must pass. Hit NFR-003 (k=10 over 100k vectors under 100 ms p95).

## Spec references

- FR-005 (vector store abstraction)
- FR-006 (sqlite-vec default backend)
- FR-013 (vector reindex)
- NFR-003 (vector k-NN latency)
- C-001 (architectural integrity — vector backends behind interface)
- C-006 (vector backend extensibility — adding a backend touches only its directory)
- US 2 acceptance scenarios 1-2

## Plan references

- §3 Public API VectorStore / Collection / CollectionSpec
- §4 Internal Layering (vector subpackage layout)
- §7 v1.0 ships sqlite-vec
- §8 Risk Register R2 (sqlite-vec pre-v1; pin + Reindex from day one)
- Research D2, D3

## Subtasks

1. In `core/storage/vector/vector.go`, define interfaces and types per plan §3: `VectorStore`, `Collection`, `CollectionSpec`, `Embedding{ID, Vector, Metadata}`, `QueryVector`, `Match{ID, Score, Metadata}`, `Filter`, `Metric` (cosine, l2, dot), `Quantization` (none, binary_int8), `BackendKind`, `CollectionStats`.
2. Pin sqlite-vec extension version (per research note "pin extension version via vendored binary") and load into the libSQL connection at Open (extension loading enabled before pragma application or right after). Document the pinned version in `core/storage/vector/sqlitevec/doc.go`.
3. In `core/storage/vector/sqlitevec/`, implement the `Collection` interface using sqlite-vec virtual tables (`vec0` for sqlite-vec). Naming: `vec_<collection_name>` per plan §5.2 prefix convention. Insert / BatchInsert (write tx via core/storage WriteTx); Search uses sqlite-vec MATCH / k-NN syntax; Delete; Reindex (rebuilds the virtual table from a consumer-provided iterator over authoritative embeddings); Stats reads row count + dimension.
4. Wire `core/storage.DB.VectorStore()` to return the configured backend (defaults to sqlite-vec). Backend selection driven by `cfg.VectorBackend BackendKind`.
5. Define a backend-neutral contract test suite in `core/storage/vector/contract_test.go`: same test bodies parameterized over backend factory; covers insert + k-NN with deterministic ordering on identical input (US 2 acceptance scenario 1), reindex round-trip, delete, dimension-mismatch error, metric semantics. WP08/WP09 reuse this suite.
6. Bench: generate 100k random embeddings (text-embedding-3-large dim 3072 is the realistic target; bench at the chosen production dim or document the chosen dim) into a sqlite-vec collection; measure k=10 search latency p95 across 1k queries. Gate at < 100 ms p95 per NFR-003. Allow `-short` skipping in CI.
7. Emit `vector_collection_opened` and `vector_reindex_completed` events through the EventSink.

## Acceptance criteria

- `db.VectorStore().OpenCollection(spec)` returns a working sqlite-vec-backed collection in the same DB file.
- Insert + Search returns expected nearest neighbors against a known small corpus with deterministic ordering on identical input.
- Reindex rebuilds the virtual table from a consumer-supplied iterator without loss.
- Backend-neutral contract test suite passes for sqlite-vec.
- NFR-003 bench passes on the CI baseline runner.
- No package outside `core/storage/vector/...` imports sqlite-vec.

## Files to create/modify

- Create: `core/storage/vector/vector.go`, `vector_test.go`, `contract_test.go`
- Create: `core/storage/vector/sqlitevec/{store.go,collection.go,doc.go,store_test.go,bench_test.go}`
- Modify: `core/storage/storage.go` (VectorStore accessor on DB)
- Modify: `core/storage/db/conn.go` (extension loader hook)
- Modify: `core/storage/eventkinds.go` (vector_collection_opened, vector_reindex_completed already declared in WP05)

## Definition of done

- sqlite-vec backend is the default; swappable via `cfg.VectorBackend`.
- Contract test suite green and reusable.
- NFR-003 bench gate green.
- Reindex contract validated end-to-end.
