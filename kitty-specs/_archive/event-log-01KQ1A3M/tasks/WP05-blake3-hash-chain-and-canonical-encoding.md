---
work_package_id: "WP05"
title: "BLAKE3 per-session hash chain and canonical payload encoding"
dependencies:
  - "WP01"
  - "WP03"
planning_base_branch: "main"
merge_target_branch: "main"
branch_strategy: "Planning artifacts were generated on main; completed changes must merge back into main."
subtasks:
  - "T001"
  - "T002"
  - "T003"
  - "T004"
  - "T005"
phase: "Phase 5 - Hash chain"
assignee: ""
agent: ""
shell_pid: ""
history:
  - timestamp: "2026-04-25T00:00:00Z"
    agent: "system"
    action: "Prompt generated via /spec-kitty.tasks"
---

# Work Package Prompt: WP05 – BLAKE3 per-session hash chain and canonical payload encoding

## Goal

Implement `core/event/chain/` — the cryptographic spine of the audit
story. Provide a stable canonical-JSON encoder so payload hashes are
reproducible across processes, a BLAKE3 hash function over the canonical
form, and per-session chain-link computation (`prev_hash`) used by the
append path (WP06) and the verifier (WP08).

## Spec references

- FR-004 — Hash-chain integrity.
- NFR-005 — Single-byte tampering detected 100 % by chain verification.
- C-005 — SOC 2-readiness; cryptographic guarantee complementing
  append-only-by-API.

## Plan references

- §2 (`chain/`) — `hash.go`, `verify.go`, `canonical.go`.
- §4.1 Emit path step 4–6 — canonicalize, hash, link prev_hash, write
  under txn.
- §5.5 Per-session chain shape — chain links scoped per session;
  ULID PK provides global ordering across sessions.
- §9 Planning decision — BLAKE3 over canonical-JSON; ADR commitment.

## Subtasks

- T001 — Implement `canonical.go`: stable canonical JSON encoder
  (sorted object keys, fixed number formatting, UTF-8 bytes); document
  whether it conforms to RFC 8785 JCS or a documented superset (note
  plan §9 open item).
- T002 — Implement `hash.go`: `PayloadHash(canonical []byte) [32]byte`
  using BLAKE3; constant-time helpers; unit tests against known-vectors
  for the BLAKE3 library version pinned.
- T003 — Implement chain-link helper: `LinkHash(prevHash [32]byte,
  payloadHash [32]byte) [32]byte` (or use `prev_hash` as-is per the
  schema in §5; verify alignment with §5 spec — `prev_hash` field
  references the previous event's `payload_hash`).
- T004 — Round-trip determinism tests: encode the same payload across
  multiple processes / Go versions; assert byte-equality of canonical
  output and `payload_hash`.
- T005 — Adversarial tests: feed payloads with reordered keys, varying
  whitespace, equivalent number formats — assert canonical output is
  identical (defends NFR-005 detection guarantee).

## Acceptance criteria

- Canonical encoder: 100 % of randomized key-permutation pairs produce
  identical bytes.
- BLAKE3 implementation matches published test vectors.
- Round-trip tests pass under `go test -race`.
- ADR drafted for "BLAKE3 + canonical-JSON for payload_hash" decision
  (plan §11; DIRECTIVE_003).
- No allocations on the hot path of `PayloadHash` beyond the BLAKE3
  library's own buffer (verified via benchmark with `-benchmem`).

## Files to create / modify

- `core/event/chain/canonical.go`
- `core/event/chain/canonical_test.go`
- `core/event/chain/hash.go`
- `core/event/chain/hash_test.go`
- `core/event/chain/bench_test.go`

## Definition of done

- All subtasks complete; tests + benchmarks green.
- ADR drafted and merged alongside this WP.
- `go vet`, `golangci-lint run` clean.
