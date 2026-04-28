---
work_package_id: "WP09"
title: "A2A v1 Signed Agent Cards envelope conformance and adapter isolation"
dependencies:
  - "WP02"
  - "WP03"
planning_base_branch: "main"
merge_target_branch: "main"
branch_strategy: "Planning artifacts were generated on main; completed changes must merge back into main."
subtasks:
  - "T001"
  - "T002"
  - "T003"
  - "T004"
phase: "Phase 8 - A2A envelope conformance"
assignee: ""
agent: ""
shell_pid: ""
history:
  - timestamp: "2026-04-25T00:00:00Z"
    agent: "system"
    action: "Prompt generated via /spec-kitty.tasks"
---

# Work Package Prompt: WP09 – A2A v1 envelope conformance and adapter isolation

## Goal

Implement the A2A v1.0 Signed Agent Cards envelope adapter under `core/trust/envelope/` (kept inside `core/trust/` so no other `core/` package transitively imports `a2aproject/a2a-go` per DIRECTIVE_001 + acp-orchestration D3). Use the SDK where it exposes signed-card primitives and hand-roll only what the SDK does not yet expose, recording the split in ADR `adr-trust-004-envelope-implementation-source`.

## Spec references

- FR-001, FR-002, FR-012 (uniform verification API)
- C-004 (A2A v1 envelope conformance — no parallel envelope), C-001 (boundary)
- SC-001, SC-002 (verifier rejects tampered cards)
- Plan §5.3 (envelope fields), §6.1 (A2A SDK reuse)

## Cross-mission dependencies

- `acp-orchestration-01KQ17ZK` — A2A core protocol integration; this WP does not import `core/acp/`, but the envelope must be the same shape `core/acp/` will marshal.

## Plan references

- §6.1 (SDK reuse + ADR `adr-trust-004` for what came from SDK vs hand-rolled).
- §5.3 envelope fields: `payload`, `signature`, `algorithm`, `key_id`, `issued_at`, `expires_at`, `chain` (optional), `key_distribution_hint` (optional).
- §1.4 (A2A v1.0 envelope is "implemented" at v1.0).

## Subtasks

- **T001** — Create `core/trust/envelope/` subpackage. Move envelope marshaling code out of the top-level `core/trust/envelope.go` shim (which now becomes a thin re-export). The subpackage is the only location in the entire `core/` tree allowed to import `github.com/a2aproject/a2a-go`.
- **T002** — Implement `Marshal(env Envelope) ([]byte, error)` and `Unmarshal(data []byte) (Envelope, error)` conforming to A2A v1.0 Signed Agent Cards spec. Use `a2a-go` types where exposed; hand-roll the rest. Implement canonical JSON serialization for the `payload` field so signatures verify across implementations (no map-iteration-order surprises).
- **T003** — Implement the verifier-side parsing in `core/trust/verify.go` step 1 (envelope shape check from WP03): use `envelope.Unmarshal`; on shape failure return `RejSignatureInvalid` with detail "envelope shape" so operators can distinguish from a real signature mismatch.
- **T004** — Draft ADR `adr-trust-004-envelope-implementation-source.md` under `docs/adr/` listing every envelope field, marking each as `sdk` or `hand-rolled`, with a "revert plan" for when the SDK catches up. Add black-box tests using golden envelope fixtures (canonical bytes for known-valid signed AgentCards) so SDK upgrades that change the shape are caught at CI.

## Acceptance criteria

- `go list -deps ./core/...` shows `github.com/a2aproject/a2a-go` only in transitive deps of `core/trust/envelope/` — no other `core/` package imports it (verified manually until WP12 lands depguard).
- Canonical JSON serialization: an envelope marshaled and re-marshaled produces byte-identical output (deterministic — User Story 2 tampering detection requires this).
- Tampering test: flipping any byte in the `payload` after marshaling makes verification fail with `RejSignatureInvalid` (User Story 2, SC-002 evidence).
- ADR `adr-trust-004` drafted, linked from PR (DIRECTIVE_003).
- Golden envelope fixtures committed under `core/trust/envelope/testdata/` so future SDK upgrades that drift the wire shape break CI.
- Charter quality gates clean (`gofmt`, `goimports`, `go vet`, `golangci-lint run`).

## Files to create/modify

- Create: `core/trust/envelope/envelope.go`, `core/trust/envelope/canonical.go`, `core/trust/envelope/doc.go`
- Modify: `core/trust/envelope.go` (top-level shim re-exports — public API for callers)
- Modify: `core/trust/verify.go` step 1 (use envelope subpackage)
- Modify: `go.mod` — add `github.com/a2aproject/a2a-go` (only path that imports it)
- Tests: `core/trust/envelope/envelope_test.go`, fixtures under `core/trust/envelope/testdata/`
- Create: `docs/adr/adr-trust-004-envelope-implementation-source.md`

## Definition of done

- All four subtasks complete.
- Boundary check: `go list -deps` confirms only `core/trust/envelope/` imports `a2a-go`.
- Tampering test passes for every envelope field listed in §5.3 (each byte flipped → `RejSignatureInvalid`).
- ADR drafted; PR description references it.
- ≥ 80% coverage on `core/trust/envelope/`.
