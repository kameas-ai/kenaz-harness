---
work_package_id: "WP12"
title: "Depguard boundary rule, cross-cutting integration tests, and remaining ADRs"
dependencies:
  - "WP05"
  - "WP06"
  - "WP07"
  - "WP08"
  - "WP09"
  - "WP10"
  - "WP11"
planning_base_branch: "main"
merge_target_branch: "main"
branch_strategy: "Planning artifacts were generated on main; completed changes must merge back into main."
subtasks:
  - "T001"
  - "T002"
  - "T003"
  - "T004"
  - "T005"
phase: "Phase 10 - Cross-cutting integration"
assignee: ""
agent: ""
shell_pid: ""
history:
  - timestamp: "2026-04-25T00:00:00Z"
    agent: "system"
    action: "Prompt generated via /spec-kitty.tasks"
---

# Work Package Prompt: WP12 – Depguard rule, cross-cutting integration tests, and remaining ADRs

## Goal

Lock down the architectural boundary with a `golangci-lint` `depguard` rule (R-009 mitigation), land the cross-cutting integration tests that exercise SC-001 through SC-008 end to end (two-harness handshake, tampering rejection, rotation, revocation, audit replay, no-plaintext-keys audit), and draft the remaining ADRs from plan §8 (default algorithm `adr-trust-001`). This is the mission's gate-keeper WP — when it lands, the trust primitive is ready for the consumer missions (`shared-context-distribution`, `bundle-format-resolver`, `policy-engine`).

## Spec references

- FR-002, FR-005, FR-006, FR-011, FR-014
- NFR-001, NFR-003, NFR-004, NFR-005, NFR-008
- C-001 (boundary lint), C-003 (append-only), C-004 (A2A envelope), C-005 (SOC 2)
- SC-001, SC-002, SC-003, SC-004, SC-005, SC-006, SC-007, SC-008
- Plan §8 R-009 (depguard rule), §10 charter re-check.

## Cross-mission dependencies

- `secrets-keychain-01KQ1A3M` — this WP's audit-suite test for NFR-004 may share fixtures.
- `event-log-01KQ1A3M` — replay test depends on the public event-log query API.
- `acp-orchestration-01KQ17ZK` — two-harness handshake test (SC-001) consumes the A2A core protocol surface; if the API is not yet stable, gate the test behind a build tag and document the deferral.

## Plan references

- §8 Risk register R-009: explicit `golangci-lint` `depguard` rule that `core/trust/` (top level) may not import `aws-sdk-go-v2`, `go-piv`, `zalando/go-keyring`, `miekg/pkcs11`, or `a2aproject/a2a-go`. Backends are the only allowed importers.
- §10 charter re-check (PASS items to verify continue to hold).
- §11 outputs (`quickstart.md`, `data-model.md`, `contracts/`).

## Subtasks

- **T001** — Add or extend `.golangci.yml` `depguard` configuration: `core/trust/` (top level, excluding `backends/` and `envelope/`) may not import any of `aws-sdk-go-v2/...`, `go-piv/piv-go`, `zalando/go-keyring`, `miekg/pkcs11`, `a2aproject/a2a-go`. Add a CI step that fails on any violation. Add a focused negative test by intentionally introducing an import in a non-merged commit to confirm the rule fires (then revert).
- **T002** — Add cross-cutting integration test suite under `core/trust/integration/` covering: (a) SC-001 two-harness handshake with a shared org anchor; (b) SC-002 tampered card rejected before any Skill invocation; (c) SC-003 24-hour rotation simulation with peers on both keys; (d) SC-004 5-min revocation propagation budget; (e) SC-006 offline replay reproduces every accept/reject decision from the event log alone (User Story 5 independent test).
- **T003** — Add the security-audit suite for SC-005 / NFR-004: scans on-disk state, process command-line args, and the event log after a sign operation; asserts no private-key bytes appear anywhere outside the configured backend boundary. Uses fixtures from the `software` backend (test-only) to know what private bytes to look for.
- **T004** — Draft `adr-trust-001-default-algorithm.md` under `docs/adr/` recording the Open Question 2 resolution (Ed25519-only at v1.0; ECDSA-P256 + RSA-PSS opt-in in v1.x). Verify ADRs `adr-trust-002` (WP10), `adr-trust-003` (WP08), `adr-trust-004` (WP09) are linked from the mission's `plan.md` "outputs" section.
- **T005** — Author `kitty-specs/a2a-signed-cards-trust-01KQ18P9/quickstart.md` (per plan §11) showing the SC-001 walkthrough: configure org anchor → sign a card → verify on a peer in five minutes from a clean clone. Add `data-model.md` with the §5.1 SQL DDL exactly as implemented, and the `contracts/` files (`trust.go.contract`, `backend.go.contract`) snapshotting the public interfaces from WP01/WP02 for downstream consumer missions.

## Acceptance criteria

- `golangci-lint run` fails fast on any new top-level `core/trust/` import of a backend SDK or the A2A SDK (R-009 evidence).
- Integration test suite passes on the cross-platform CI matrix (NFR-008): macOS, Linux, Windows.
- Audit suite scan finds zero plaintext private-key bytes anywhere outside the backend boundary across the platform matrix (NFR-004, SC-005 evidence in PR description).
- Offline-replay test reconstructs every accept/reject decision from the event log alone (SC-006).
- `adr-trust-001` drafted and linked; all four planning-phase ADRs (`-001`/`-002`/`-003`/`-004`) referenced from plan §11 outputs and from the PR.
- `quickstart.md`, `data-model.md`, and `contracts/` artifacts land per plan §11.
- Charter re-check: `gofmt`, `goimports`, `go vet`, `golangci-lint run`, `go test ./... -race` all clean.

## Files to create/modify

- Modify: `.golangci.yml` — add `depguard` rule
- Create: `core/trust/integration/` test suite (one file per SC: `sc001_handshake_test.go`, `sc002_tamper_test.go`, `sc003_rotation_test.go`, `sc004_revocation_test.go`, `sc005_no_plaintext_test.go`, `sc006_replay_test.go`)
- Create: `docs/adr/adr-trust-001-default-algorithm.md`
- Create: `kitty-specs/a2a-signed-cards-trust-01KQ18P9/quickstart.md`
- Create: `kitty-specs/a2a-signed-cards-trust-01KQ18P9/data-model.md`
- Create: `kitty-specs/a2a-signed-cards-trust-01KQ18P9/contracts/trust.go.contract`
- Create: `kitty-specs/a2a-signed-cards-trust-01KQ18P9/contracts/backend.go.contract`

## Definition of done

- All five subtasks complete.
- CI matrix is green across macOS / Linux / Windows for both tag-off and tag-on combinations.
- Charter re-check from plan §10 still PASS on every gate.
- All four planning-phase ADRs drafted and linked.
- Mission gate: every spec FR/NFR/C/SC has at least one test asserting it; PR description includes a coverage matrix mapping spec id → test file.
- PR description marks the mission ready for `/spec-kitty.merge` per the canonical workflow.
