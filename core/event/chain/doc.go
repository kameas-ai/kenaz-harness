// Package chain provides the cryptographic spine of the event log:
// canonical-JSON encoding, payload hashing, and per-session chain-link
// computation.
//
// PayloadHash uses BLAKE3 over the canonical-JSON encoding of a
// payload (plan §9 planning decision). Per-session chain shape: each
// event's prev_hash references the previous event's payload_hash
// within the same session (plan §5.5 / spec OQ-1 default). The global
// ULID PK provides a sortable cross-session ordering index without
// forcing every chain to share a single hash.
package chain
