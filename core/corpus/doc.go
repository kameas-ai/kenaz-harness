// Package corpus implements bulk-ingestible knowledge corpora for the
// agent-kernel-graph mission (WP10/WP11; FR-020/FR-021/FR-056).
//
// A corpus is a named pool of source files (markdown, code, plain text)
// that are walked, hashed, chunked, embedded, and stored under a scope
// (global / project / session). Retrieval is cosine top-K over the
// per-corpus on-disk vector store at <DataDir>/corpora/<id>/vectors.gob.
//
// Design notes:
//
//   - Atomic per-file commit (NFR-006). Ingestion writes corpus_files +
//     corpus_chunks + the in-memory vector index entries inside one
//     storage.WriteTx per file. A crash mid-file leaves no partial rows
//     and no orphaned vectors. The on-disk vector store is rewritten
//     after each successful file commit so a crash between files leaves
//     the on-disk store consistent with the SQL state at most one file
//     behind — the next ingest re-derives that file from its hash.
//
//   - Hash-check skip (FR-028 from the prior spec / WP10-T7). On
//     re-ingest, the walker hashes each file; matching (path, sha256)
//     in corpus_files short-circuits without re-embedding. Missing files
//     are dropped lazily by DeleteCorpus / explicit prune.
//
//   - Embedding interface. The package is decoupled from a specific
//     embedder via the Embedder interface (mirrors core/memory.Embedder
//     to allow eventual consolidation but does not depend on it — the
//     ingest path is thus testable with a 32-dim deterministic stub).
//
//   - Token-budget cap on retrieval (WP11-T6). Default 16 KiB output;
//     spillover dropped with a one-line warning per the spec.
//
//   - Privacy. Chunk text is content under <DataDir>/corpora/ — never
//     logged at default verbosity. Telemetry verbose-attributes toggle
//     gates anything richer than the count of chunks returned.
//
// DIRECTIVE_001: this package imports stdlib only. Reverse direction —
// session.migrations consumes the corpus migration via owned SQL — stays
// clean. The RPC view at core/rpc/views/corpus/ wires this package to
// the Wails surface.
package corpus
