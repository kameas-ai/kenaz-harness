---
work_package_id: "WP03"
title: "Secret type ([]byte-only) with Use/Destroy and zeroize"
dependencies:
  - "WP02"
planning_base_branch: "main"
merge_target_branch: "main"
branch_strategy: "Planning artifacts were generated on main; completed changes must merge back into main."
subtasks:
  - "T001: Define Secret interface (Use, Destroy, ReferenceID)"
  - "T002: Implement StdlibSecret backed by []byte"
  - "T003: Implement explicit zero loop + runtime.KeepAlive on Destroy"
  - "T004: Enforce single-use via Use(closure) pattern"
  - "T005: Best-effort heap-scan advisory test confirming zeroization"
  - "T006: Unit tests for Use, Destroy, double-Destroy idempotency, post-Destroy access errors"
phase: "Phase 3 - Secret Lifecycle"
assignee: ""
agent: ""
shell_pid: ""
history:
  - timestamp: "2026-04-25T00:00:00Z"
    agent: "system"
    action: "Prompt generated via /spec-kitty.tasks"
---

# Work Package Prompt: WP03 – Secret type ([]byte-only) with Use/Destroy and zeroize

## Goal

Implement the `Secret` interface and the baseline `StdlibSecret` implementation that backs every successful resolution. Secrets are `[]byte`-only by construction (per D7), exposed only through `Use(func([]byte) error)`, and zeroized on `Destroy()` with `runtime.KeepAlive` to defeat compiler elision. This is the single in-process boundary between resolved bytes and consumer code.

## Spec references

- FR-013 (Zeroize after use): documented pattern for receiving a credential, using it, and explicitly zeroing the byte slice.
- NFR-003 (Plaintext leakage): zero matches across the audit matrix; the Secret type's discipline is the primary control.
- Key Entities: Secret (`bytes` `[]byte`, `acquired_at`, `consumer_id`, `reference_id`; methods `Use`, `Destroy`).
- User Story 4 (Resolved credentials are short-lived in process memory).

## Plan references

- §3 Public API → `Secret` interface sketch (Use, Destroy, ReferenceID).
- §4 Internal layering → "Zeroize on Secret.Destroy" subsection (D6, D7, FR-013).
- §5 Data model summary → Secret row (`[]byte`, scoped to consumer call, lint-enforced).
- §7 Phasing → v1.0 ships `StdlibSecret` baseline; `MemguardSecret` is opt-in (lands in WP14).
- §12 Acceptance mapping → FR-013 maps here.

## Subtasks

- Define `Secret` interface with `Use(fn func(value []byte) error) error`, `Destroy()`, `ReferenceID() string`.
- Implement `StdlibSecret` struct holding `[]byte`, `acquired_at`, `consumer_id`, `reference_id`.
- Implement `Use`: borrows the slice for the duration of `fn`; returns `fn`'s error wrapped if needed; rejects post-Destroy use with typed error.
- Implement `Destroy`: explicit zero loop (`for i := range b { b[i] = 0 }`) followed by `runtime.KeepAlive(b)`; idempotent (second call is a no-op).
- Document the caller pattern (`defer secret.Destroy(); secret.Use(fn)`) in the package doc.
- Add a best-effort heap-scan advisory test: write a known sentinel into a Secret, call Destroy, then scan the heap (where supported) and confirm the sentinel is absent. Document as advisory per plan §4.
- Unit tests covering Use, Destroy, idempotent Destroy, post-Destroy access errors, zeroization correctness on Use closure return.

## Acceptance criteria

- `core/secrets/secret/secret.go` exposes the `Secret` interface and `StdlibSecret` impl.
- The interface and impl are `[]byte`-only — no `string` field, no `string` parameter, no `string` return on credential-bearing methods. (Lint enforcement lands in WP04.)
- `Destroy()` is verified to zero the buffer; advisory heap-scan test confirms zeroization on supported platforms.
- Double-Destroy is safe and idempotent; post-Destroy `Use` returns a typed error.
- Tests achieve ≥80% line coverage on `core/secrets/secret/`.
- Charter quality gates pass.

## Files to create / modify

- Create `core/secrets/secret/secret.go`.
- Create `core/secrets/secret/secret_test.go`.

## Definition of done

- WP02's `Backend` interface can return `Secret` values produced by `StdlibSecret`.
- FR-013 acceptance scenarios traceable to tests in this WP.
- Architectural integrity preserved (no SDK imports).
- Handoff: stable Secret API for cache (WP05), preflight (WP06), and every backend WP (WP08–WP13).
- `MemguardSecret` and memguard build tag are explicitly out of scope for this WP — see WP14.
