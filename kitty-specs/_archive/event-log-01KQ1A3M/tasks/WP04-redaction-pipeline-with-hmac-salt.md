---
work_package_id: "WP04"
title: "Redaction pipeline with HMAC-deterministic placeholders"
dependencies:
  - "WP01"
  - "WP02"
planning_base_branch: "main"
merge_target_branch: "main"
branch_strategy: "Planning artifacts were generated on main; completed changes must merge back into main."
subtasks:
  - "T001"
  - "T002"
  - "T003"
  - "T004"
  - "T005"
  - "T006"
  - "T007"
phase: "Phase 4 - Redaction pipeline"
assignee: ""
agent: ""
shell_pid: ""
history:
  - timestamp: "2026-04-25T00:00:00Z"
    agent: "system"
    action: "Prompt generated via /spec-kitty.tasks"
---

# Work Package Prompt: WP04 – Redaction pipeline with HMAC-deterministic placeholders

## Goal

Implement `core/event/redact/` — the non-bypassable redaction pipeline
(C-004) that runs unconditionally on every payload before persistence.
Built-in credential-pattern matchers (API keys, JWTs, AWS keys, Bearer
tokens) plus operator-marked sensitive-field matchers (JSON-path) feed
into a deterministic HMAC-keyed placeholder generator whose salt is
sourced from secrets-keychain as a `CredentialReference`. Policy refuses
"off"; the pipeline is the single ingress that mutates payload bytes
before append.

## Spec references

- FR-005 — Redaction pipeline (credential-pattern + operator-marked).
- FR-006 — Deterministic redaction outputs (HMAC over server-side salt).
- NFR-002 — Redaction overhead < 1 ms p95.
- NFR-003 — Plaintext credentials in persisted events: zero across the
  audit suite.
- C-004 — Redaction non-bypassable.
- C-005 — SOC 2-readiness evidence.

## Plan references

- §2 (`redact/`) — `pipeline.go`, `matchers/`, `fields.go`, `hmac.go`,
  `policy.go`.
- §4.1 Emit path — redaction is the unconditional step before
  canonicalization and hash.
- §6.2 secrets-keychain integration — HMAC salt as `CredentialReference`,
  resolved into `Secret` zeroized on rotation.
- Risk R1 (false-negative) and R6 (salt rotation) mitigations.

## Cross-mission dependencies

- **secrets-keychain-01KQ1A3M**: HMAC salt resolved as
  `CredentialReference{ keychain: "event-log-redaction-salt" }` into a
  `Secret` (`[]byte`-typed, never `string`); rotation surfaces a
  `redaction.salt-rotated` self-event.

## Subtasks

- T001 — Define `Pipeline` struct and `Apply(payload any) (canonical
  []byte, RedactionSummary, error)` signature; pipeline is constructed
  with policy + salt and held for process lifetime.
- T002 — Implement built-in credential-pattern matchers under
  `core/event/redact/matchers/`: API keys, JWTs (3-segment base64url),
  AWS access keys (`AKIA[0-9A-Z]{16}`), Bearer tokens, Basic auth, PEM
  blocks, generic `password=`/`secret=` query-string marks.
- T003 — Implement structured-field matcher (`fields.go`): JSON-path
  selectors marked sensitive in policy redact in place.
- T004 — Implement `hmac.go`: deterministic placeholder
  `redacted:<hmac_truncated>` from BLAKE3-keyed-MAC over salt + matched
  substring; salt loaded from secrets-keychain via
  `CredentialReference`; held as `Secret`; zeroized on rotation.
- T005 — Implement `policy.go`: load YAML/JSON policy; refuse `enabled:
  false`; emit `redaction.salt-rotated` self-event on salt rotation.
- T006 — Confirm pipeline is the single ingress: add a static-analysis
  test (or build-tag check) that no other path under `core/event/`
  writes to the events table without going through `Pipeline.Apply`.
  Pipeline panic / error aborts append (no plaintext fallback).
- T007 — Property-based tests: random credential-shaped strings injected
  into random payload shapes; assert (a) zero plaintext survives, (b)
  identical input produces identical placeholder, (c)
  `RedactionSummary` lists every matcher fired and field path, (d) "no
  patterns matched" produces a `no-op` summary distinct from
  "redaction skipped" (edge case in spec).

## Acceptance criteria

- Property tests: 10K random credential-shape inputs across 1K random
  payload shapes; zero plaintext credentials survive (NFR-003).
- Determinism: identical input + identical salt produces identical
  output across 100 repetitions.
- Performance: redaction adds < 1 ms p95 to a typical payload (NFR-002),
  measured under `go test -bench`.
- Pipeline cannot be disabled at runtime; `policy.enabled = false` is
  rejected with a typed error at load time (C-004).
- Salt rotation: emits `redaction.salt-rotated` self-event; old payloads
  retain their original placeholders (immutability invariant).
- `go test ./core/event/redact/...` and `go test -race` green.

## Files to create / modify

- `core/event/redact/pipeline.go`
- `core/event/redact/policy.go`
- `core/event/redact/hmac.go`
- `core/event/redact/fields.go`
- `core/event/redact/matchers/api_keys.go`
- `core/event/redact/matchers/jwt.go`
- `core/event/redact/matchers/aws.go`
- `core/event/redact/matchers/bearer.go`
- `core/event/redact/matchers/pem.go`
- `core/event/redact/matchers/generic.go`
- `core/event/redact/pipeline_test.go`
- `core/event/redact/property_test.go`
- `core/event/redact/bench_test.go`

## Definition of done

- All subtasks complete; property + benchmark gates met.
- ADR drafted for redaction non-bypassable design (plan §11; DIRECTIVE_003).
- Security-sensitive code path tests landed in the same commit as
  implementation per charter testing standards.
- `go vet`, `golangci-lint run` clean.
