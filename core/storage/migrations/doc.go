// Package migrations is the harness's versioned migration framework.
//
// Every consuming mission registers its DDL with the framework; storage
// owns applying it. Migrations are versioned, ordered, idempotent units
// of schema change with explicit Up and Down functions.
//
// # Version-block reservation
//
// Each owning mission claims a fixed range of version numbers so that
// missions cannot collide. The canonical table lives in blocks.go and
// is reproduced here:
//
//	1-99       storage (this mission)
//	100-199    event-log
//	200-299    secrets-keychain (audit tables)
//	300-399    sessions
//	400-499    scheduler
//	500-599    mcp
//	600-699    a2a + signed-cards-trust
//	700-799    bundle + shared-context-distribution
//	800-899    memory-rag
//	900-999    app-layer
//	1000+      reserved-future
//
// To claim a new range, edit blocks.go and the README in
// kitty-specs/storage-foundations-01KQ1A3K/tasks/. A new range entry must
// be merged before any migration referencing it is registered.
//
// # Apply semantics
//
// Pending migrations are applied in Version ascending order, each inside
// its own write transaction. On success a row is appended to
// harness_migrations with action=applied and the canonicalized content
// hash. On failure the transaction rolls back, no ledger row is written,
// and the apply returns ErrMigrationFailed wrapping the underlying error.
//
// # Rollback semantics
//
// Rollback(ctx, toVersion) is an explicit operator action. It runs the
// Down function for each migration whose Version > toVersion, in reverse
// order, each inside its own transaction. Each successful step appends a
// new row with action=rolled_back. The original applied row is preserved
// — the ledger is append-only.
//
// # Hash and gap checks
//
// At startup the runner verifies, for every applied ledger row, that the
// registered migration's canonical content hash matches the ledger's
// content_hash. Mismatch → ErrLedgerHashMismatch. Gaps in
// [min(applied)+1, max(applied)] → ErrSchemaGap.
//
// # Canonicalization rule for ContentHash
//
// The hash is computed over Migration.UpSource after canonicalization:
//
//   - Line endings normalized to "\n"
//   - "--" line comments stripped (everything from "--" to end of line)
//   - "/* ... */" block comments stripped
//   - Multiple consecutive whitespace runs collapsed to a single space
//   - Leading/trailing whitespace trimmed
//
// Then SHA-256 over the resulting UTF-8 bytes; output is the lowercase
// hex digest. See hash.go for the canonical implementation; consuming
// missions should compute their own ContentHash with the same helper at
// registration time.
package migrations
