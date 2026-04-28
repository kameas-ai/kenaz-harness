---
work_package_id: "WP14"
title: "Memguard opt-in hardening, CLI, and cross-cutting audit/security tests"
dependencies:
  - "WP08"
  - "WP09"
  - "WP10"
  - "WP11"
  - "WP12"
planning_base_branch: "main"
merge_target_branch: "main"
branch_strategy: "Planning artifacts were generated on main; completed changes must merge back into main."
subtasks:
  - "T001: Implement MemguardSecret behind build tag memguard"
  - "T002: Implement harness secrets status CLI / RPC (FR-017)"
  - "T003: Implement harness secrets invalidate <ref> CLI / RPC (FR-011)"
  - "T004: Cross-cutting audit suite scanning event log, lockfile, RPC payloads, /tmp"
  - "T005: Refuse-inline-plaintext config-load test (FR-015)"
  - "T006: Per-consumer audit attribution test (FR-016, User Story 6)"
  - "T007: ADR for cache TTL default + Linux fallback chain + scrub policy (Open Questions)"
  - "T008: Process-arg / env scrub at startup per Open Question 3"
phase: "Phase 14 - Hardening, CLI, and Audit"
assignee: ""
agent: ""
shell_pid: ""
history:
  - timestamp: "2026-04-25T00:00:00Z"
    agent: "system"
    action: "Prompt generated via /spec-kitty.tasks"
---

# Work Package Prompt: WP14 – Memguard opt-in hardening, CLI, and cross-cutting audit/security tests

## Goal

Close the mission by landing the cross-cutting deliverables: the opt-in `MemguardSecret` for hardened deployments, the operator CLI/RPC surface (`harness secrets status` / `harness secrets invalidate`), the cross-cutting audit suite that proves NFR-003 across the audit matrix, and the ADR that records the resolutions for the three Open Questions. Also lands the process-arg / env scrub at startup per plan §4 / Open Question 3.

## Spec references

- FR-011 (Cache invalidation on rotation): operator-driven invalidation surfaced via CLI.
- FR-015 (Refusal of inline plaintext): config-load-time refusal exercised end-to-end.
- FR-016 (Per-reference scoping): audit attribution surfaced in event-log queries.
- FR-017 (Backend health probe): exposed via `harness secrets status`.
- NFR-003 (Plaintext leakage): zero matches across the audit matrix (event log, configuration, lockfile, RPC payloads, error reports, temp files).
- C-002 (No plaintext in any persisted state).
- C-005 (SOC 2 readiness): resolution events, rotation events, and pre-flight outcomes produce evidence sufficient for SOC 2 audit.
- User Story 4 (Resolved credentials short-lived in memory) — memguard hardens this.
- User Story 6 (Credentials scoped to specific consumers).

## Plan references

- §2 Architectural placement → `core/secrets/secret/memguard_secret.go` (build-tag gated).
- §4 Internal layering → "Process-arg / env scrub" subsection.
- §5 Data model summary → memguard variant noted as opt-in.
- §7 Phasing → v1.0 ships CLI + audit; `MemguardSecret` follows but is in scope for this mission as the hardening lever.
- §10 Test strategy → audit suite; performance benches from prior WPs aggregate here.
- §12 Acceptance mapping → FR-011 (operator-side), FR-015, FR-016, FR-017, NFR-003, C-002, C-005 partially map here.
- §9 Open questions → ADR records the three resolutions.

## Subtasks

- Implement `MemguardSecret` at `core/secrets/secret/memguard_secret.go`, build-tagged `//go:build memguard`. Wraps `memguard.NewBuffer(...)`; `Destroy()` calls `buf.Destroy()`.
- Implement CLI / RPC handlers for `harness secrets status` (calls `Resolver.Health`) and `harness secrets invalidate <ref>` (calls `Resolver.Invalidate`).
- Cross-cutting audit suite (`core/secrets/internal/audittest/`): drive a representative session, then scan the event log file, lockfile fixtures, RPC payload captures, and `t.TempDir()` for any byte pattern matching a known-credential sentinel. Zero matches required.
- End-to-end test for FR-015: load a config file with an inline plaintext credential; confirm load fails with `ErrInlinePlaintext` naming the offending field.
- End-to-end test for FR-016 / User Story 6: configure two consumers with different references; confirm event-log entries distinguish them via `consumer_id`.
- ADR under `docs/adr/`: records the resolutions to the three Open Questions — cache TTL default 60s, Linux fallback chain (Secret Service → XDG portal → explicit-opt-in file with argon2id KEK; no kernel keyctl primary; no silent file fallback), and process-arg / env scrub at startup default-on.
- Implement the process-arg / env scrub at startup per plan §4: walk `os.Args[1:]` at `secrets.New()`, scan for credential-shaped substrings, fail with `ErrInlinePlaintext` if any found; record env-var *names* referenced by the env backend, never values.

## Acceptance criteria

- `MemguardSecret` builds and tests pass under `-tags memguard`; default builds remain unchanged.
- `harness secrets status` returns the per-backend health from `Resolver.Health`.
- `harness secrets invalidate <ref>` evicts and re-resolves on next request, with `Secret.Destroy()` called on the evicted Secret.
- Audit suite asserts zero plaintext credential bytes across event log, lockfile fixtures, RPC payload captures, and `t.TempDir()` (NFR-003).
- Inline-plaintext config rejected at load time with `ErrInlinePlaintext` (FR-015).
- Two-consumer test demonstrates per-consumer audit attribution (FR-016, User Story 6, SC-005 supporting evidence).
- ADR under `docs/adr/` exists, references DIRECTIVE_003, and records Open-Question resolutions.
- Process-arg / env scrub fails fast on credential-shaped argument values; env-var names recorded.
- Charter quality gates pass.

## Files to create / modify

- Create `core/secrets/secret/memguard_secret.go` (build-tagged).
- Create `core/secrets/secret/memguard_secret_test.go`.
- Create `core/secrets/cli/status.go` and `core/secrets/cli/invalidate.go` (or wire into the existing harness CLI surface — defer to project conventions).
- Create `core/secrets/internal/audittest/audit_test.go`.
- Create `core/secrets/scrub/scrub.go` and `core/secrets/scrub/scrub_test.go` (process-arg / env scrub).
- Create `docs/adr/NNNN-secrets-keychain-cache-ttl-and-linux-fallback.md`.
- Update `go.mod` / `go.sum` to add `github.com/awnumar/memguard` (under build tag).

## Definition of done

- All upstream backend WPs (WP08–WP12) merged and exercised by the audit suite.
- FR-011 (operator side), FR-015, FR-016, FR-017 acceptance scenarios traceable to tests in this WP.
- NFR-003 audit matrix: zero plaintext matches confirmed.
- C-005 SOC 2 evidence: resolution + rotation + pre-flight events visible in the event log; ADR captures the policy posture.
- Open Questions 1, 2, 3 resolved with ADR-grade rationale per DIRECTIVE_003.
- Cross-mission dep recorded: `event-log-01KQ1A3M` consumes the audit-test fixtures (event-log mission's redaction pipeline double-checks no value bytes leak; this WP supplies the credential-side fixtures).
- Mission acceptance ready: SC-001 through SC-006 all have measured supporting evidence.
