---
work_package_id: "WP05"
title: "OS keychain backend (default) with cross-platform parity and preflight"
dependencies:
  - "WP02"
  - "WP04"
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
phase: "Phase 3 - OS keychain backend"
assignee: ""
agent: ""
shell_pid: ""
history:
  - timestamp: "2026-04-25T00:00:00Z"
    agent: "system"
    action: "Prompt generated via /spec-kitty.tasks"
---

# Work Package Prompt: WP05 – OS keychain backend with cross-platform parity and preflight

## Goal

Implement the default `oskeychain` backend wrapping `zalando/go-keyring` (per secrets-keychain D1) on macOS Keychain, Linux Secret Service / XDG portal (per secrets-keychain D2), and Windows Credential Manager. Wire `core/trust/config.go` `Preflight` per FR-014 to surface backend availability and anchor algorithm support before first use. This backend is the production default per FR-009.

## Spec references

- FR-008, FR-009 (OS keychain default), FR-014 (preflight)
- NFR-004 (zero private-key disk leakage), NFR-007 (fail-closed on backend unavailability), NFR-008 (cross-platform parity)
- C-001 (only `oskeychain` package imports `zalando/go-keyring`), C-002 (no plaintext keys in config — only entry references)
- Plan §6.2 (library mapping), §6.5 (preflight wired through `core/config/`)

## Plan references

- §1.4 (oskeychain "implemented" at v1.0)
- §6.2 (`zalando/go-keyring` per secrets-keychain D1; Linux fallback chain Secret Service → XDG portal → fail per D2)
- §6.5 (preflight runs after secrets pre-flight, exits non-zero on `severity=error`)

## Subtasks

- **T001** — Create `core/trust/backends/oskeychain/oskeychain.go` implementing `SigningBackend`. `BackendRef.Path` carries the keychain entry name (per C-002 — never the key bytes). Sign: load the private key handle, call `crypto/ed25519.Sign`, zero the buffer with `runtime.KeepAlive` per secrets-keychain D6/D7.
- **T002** — Implement `Health(ctx)`: probe Secret Service (Linux) / Keychain (macOS) / Credential Manager (Windows) for reachability; degrade to `unavailable` (not `degraded`) if the service cannot be reached, so dispatcher fails closed.
- **T003** — Implement Linux fallback chain per secrets-keychain D2: Secret Service first, XDG portal second, explicit failure third — never silent file-backed fallback.
- **T004** — Implement `core/trust/config.go` `Preflight` returning `[]PreflightFinding` for: backend unreachable, anchor public key not parseable, anchor algorithm not in policy allow-list, revocation endpoint unreachable. Each finding carries `severity` (`error` vs `warning`) per plan §6.5.
- **T005** — Add black-box integration tests per platform (macOS / Linux / Windows) covering: install key → sign → verify roundtrip; remove key → sign returns `ErrBackendUnavailable`; backend SDK error → fail closed (DIRECTIVE_036).
- **T006** — Document the macOS first-access prompt and the headless / SSH-only Linux gotchas in `core/trust/backends/oskeychain/doc.go` (R-002 in plan §8 risk register).

## Acceptance criteria

- Only `core/trust/backends/oskeychain/` imports `github.com/zalando/go-keyring`; verified manually until WP12 lands the `depguard` rule (C-001).
- Linux fallback chain matches plan §6.2 (Secret Service → XDG portal → fail) with no silent file fallback.
- Cross-platform CI matrix (macOS / Linux / Windows) runs the integration suite green for NFR-008 evidence.
- Preflight surfaces structured findings; `severity=error` causes a non-zero exit when invoked from `core/config/`.
- An audit suite scan (manual at this WP, automated in WP12) finds no plaintext private bytes in working directories or temp files after a sign operation (NFR-004).

## Files to create/modify

- Create: `core/trust/backends/oskeychain/oskeychain.go`, `core/trust/backends/oskeychain/health.go`, `core/trust/backends/oskeychain/linux_fallback.go`, `core/trust/backends/oskeychain/doc.go`
- Modify: `core/trust/config.go` (was a stub from WP01) — implement `Preflight` and `PreflightFinding` payload
- Tests: `core/trust/backends/oskeychain/oskeychain_test.go`, platform-tagged integration tests (`*_darwin_test.go`, `*_linux_test.go`, `*_windows_test.go`)
- Modify: `go.mod` — add `github.com/zalando/go-keyring` at the latest stable version

## Definition of done

- All six subtasks complete.
- Cross-platform CI matrix is green.
- Preflight returns at least one finding for each scenario in §6.5; tests assert exact `code` and `severity` fields.
- macOS prompt and Linux SSH-only caveats documented.
- PR description references the secrets-keychain mission for library decisions (no re-litigation per plan §6.2).
