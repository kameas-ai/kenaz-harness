---
work_package_id: "WP04"
title: "Software backend (test-only) and Ed25519 detached signing"
dependencies:
  - "WP02"
planning_base_branch: "main"
merge_target_branch: "main"
branch_strategy: "Planning artifacts were generated on main; completed changes must merge back into main."
subtasks:
  - "T001"
  - "T002"
  - "T003"
  - "T004"
phase: "Phase 4 - Software backend and Ed25519"
assignee: ""
agent: ""
shell_pid: ""
history:
  - timestamp: "2026-04-25T00:00:00Z"
    agent: "system"
    action: "Prompt generated via /spec-kitty.tasks"
---

# Work Package Prompt: WP04 – Software backend (test-only) and Ed25519 detached signing

## Goal

Implement the in-memory `software` backend used solely for tests, gated behind a build tag so it never compiles into release binaries (per plan §1.4 and §6.2). Provide the canonical Ed25519 sign / verify path using stdlib `crypto/ed25519`. This is the first concrete `SigningBackend` implementation and validates the WP02 contract end to end.

## Spec references

- FR-001, FR-004 (Ed25519 minimum), FR-008 (backend abstraction)
- NFR-002 (signing latency < 20 ms p95 with software backend)
- C-002 (no plaintext keys persisted — software backend is test-only and explicit about that)
- Plan §1.4 (status table — software is "test-only build tag"), §6.2 (backend → library mapping)

## Plan references

- §6.2 entry: `software` backend uses stdlib `crypto/ed25519`; gated behind a build tag, never compiled into release binaries.
- §4.3 (sign dispatcher contract this backend must satisfy).

## Subtasks

- **T001** — Create `core/trust/backends/software/software.go` with `//go:build test_software_backend` tag. Implement the `SigningBackend` interface using `crypto/ed25519` for sign and verify, an in-memory `map[string]ed25519.PrivateKey` keyed by `BackendRef.Path`, and `Health` always returning `ok` (test-only).
- **T002** — Implement key generation helper `GenerateAndStore(ref string)` (test-only) returning the public key for installation as a trust anchor; explicit zeroing of the private key on `Close()` per security hygiene even in test fixtures.
- **T003** — Add a guard test (`software_buildtag_test.go`) without the build tag that asserts the package compiles to a stub — i.e., calling `Register` with kind `"software"` returns `ErrBackendNotAvailable` so production binaries cannot accidentally exercise the in-memory key path.
- **T004** — Add latency benchmark `BenchmarkSoftwareSign` capturing software-backend sign p95 against NFR-002 (< 20 ms p95).

## Acceptance criteria

- `go build ./...` (no tags) does not compile any software-backend code path that holds private bytes.
- `go test -tags test_software_backend ./core/trust/...` exercises the backend end to end (sign + verify roundtrip via the `algo/ed25519` registry from WP02).
- Build-tag guard test confirms accidental enablement is impossible without the explicit tag.
- Benchmark records p95 < 20 ms locally (NFR-002 evidence in PR description).
- Lint and vet clean under both tag-on and tag-off builds.

## Files to create/modify

- Create: `core/trust/backends/software/software.go` (build-tagged), `core/trust/backends/software/stub.go` (no-tag stub), `core/trust/backends/software/doc.go`
- Tests: `core/trust/backends/software/software_test.go` (tag-on), `core/trust/backends/software/software_buildtag_test.go` (tag-off)
- Benchmark: `core/trust/backends/software/software_bench_test.go`

## Definition of done

- All four subtasks complete.
- Both tag-on and tag-off CI build matrices succeed.
- Software backend is the reference implementation other backends (oskeychain, awskms) test against for envelope-shape parity.
- PR description includes NFR-002 benchmark evidence.
- Inline doc comments explicitly note that the software backend is for tests only and must never be registered in production code paths.
