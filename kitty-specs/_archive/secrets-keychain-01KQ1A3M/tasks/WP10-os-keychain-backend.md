---
work_package_id: "WP10"
title: "OS keychain backend (zalando/go-keyring)"
dependencies:
  - "WP07"
planning_base_branch: "main"
merge_target_branch: "main"
branch_strategy: "Planning artifacts were generated on main; completed changes must merge back into main."
subtasks:
  - "T001: Wrap zalando/go-keyring Set/Get/Delete behind Backend interface"
  - "T002: Map platform errors to FR-014 typed errors"
  - "T003: Detect Linux sandbox (Flatpak /.flatpak-info, Snap SNAP env) and emit warning until WP13 ships portal"
  - "T004: Cold-resolution latency benchmark (NFR-002)"
  - "T005: Platform-gated black-box integration tests (macOS, Linux Secret Service, Windows Credential Manager)"
  - "T006: First-run macOS prompt UX hint per spec edge case"
phase: "Phase 10 - OS Keychain Backend"
assignee: ""
agent: ""
shell_pid: ""
history:
  - timestamp: "2026-04-25T00:00:00Z"
    agent: "system"
    action: "Prompt generated via /spec-kitty.tasks"
---

# Work Package Prompt: WP10 – OS keychain backend (zalando/go-keyring)

## Goal

Ship the default cross-platform OS-keychain backend wrapping `zalando/go-keyring` v0.2.8. Resolves `RefKeychain` references against macOS Keychain, Windows Credential Manager, and Linux Secret Service via D-Bus. This is the charter-default backend — the one operators land on without configuration.

## Spec references

- FR-003 (OS keychain backend, default): cross-platform OS keychain — macOS Keychain, Windows Credential Manager, Linux Secret Service / kernel keyring.
- NFR-002 (Resolution latency, cold OS keychain): under 50 ms p95 on a developer laptop.
- NFR-004 (Cross-platform parity): identical reference shapes resolve identically across macOS, Linux, Windows.
- C-004 (Charter local-first): OS keychain is the default; non-network-required by definition.
- User Story 2 (OS-native secure storage works across macOS, Linux, and Windows).
- Edge case: macOS Keychain prompts user on first access — provide UX hint.

## Plan references

- §2 Architectural placement → `core/secrets/backends/oskeychain/`.
- §4 Internal layering → "Sandbox detection (Linux)" subsection (probes `/.flatpak-info` and `SNAP`).
- §7 Phasing → v1.0 ships `oskeychain` via zalando/go-keyring; XDG portal deferred to WP13.
- §8 Risk register → R1 (macOS first-run prompt), R2 (Windows VirtualLock limits).
- §12 Acceptance mapping → FR-003, NFR-002, NFR-004, C-004 map here.
- Research D1 → `zalando/go-keyring` is the chosen library.

## Subtasks

- Implement `Backend` for OS keychain at `core/secrets/backends/oskeychain/oskeychain.go` wrapping `zalando/go-keyring` Set/Get/Delete.
- Map platform errors to FR-014 typed errors: missing entry → `ErrReferenceNotFound`; permission/denied prompts → `ErrPermissionDenied`; D-Bus / Secret Service unavailable → `ErrBackendUnavailable`.
- Detect Linux sandbox (Flatpak via `/.flatpak-info`, Snap via `SNAP` env) at backend init. v1: log a warning and fall back to direct D-Bus per plan §4. Full XDG portal routing lands in WP13.
- Implement `Health()` probe: D-Bus ping on Linux, Keychain availability on macOS, Credential Manager call on Windows.
- Add a microbenchmark validating cold-resolution p95 < 50 ms (NFR-002) on a developer laptop; document platform variance.
- Platform-gated black-box integration tests using build tags `darwin`, `linux`, `windows`. Tests use real OS keychain calls in CI runners (charter rule: black-box at boundary).
- Implement first-run UX hint: when the macOS Keychain prompts the user, surface a structured pre-flight result indicating "user-authorization-required" so the operator gets a clear cue.

## Acceptance criteria

- `core/secrets/backends/oskeychain/oskeychain.go` compiles on all three platforms with `zalando/go-keyring`.
- Identical reference shape resolves on macOS, Linux Secret Service, Windows Credential Manager (NFR-004).
- Cold-resolution p95 < 50 ms on a developer laptop (NFR-002).
- Linux sandbox detection works on Flatpak/Snap; v1 emits a warning until WP13 lands.
- Platform-gated tests pass on each platform's CI runner.
- macOS first-access prompt path is documented and tested via the pre-flight UX hint.
- Charter quality gates pass.

## Files to create / modify

- Create `core/secrets/backends/oskeychain/oskeychain.go`.
- Create `core/secrets/backends/oskeychain/sandbox_linux.go` (build-tagged sandbox detection).
- Create `core/secrets/backends/oskeychain/oskeychain_test.go` (platform-gated).
- Create `core/secrets/backends/oskeychain/oskeychain_bench_test.go`.
- Update `go.mod` / `go.sum` to add `github.com/zalando/go-keyring`.

## Definition of done

- FR-003, NFR-002, NFR-004, C-004 acceptance scenarios traceable to tests in this WP.
- Resolver routes `keychain:` references through this backend after registration.
- Architectural integrity preserved: only this package imports `zalando/go-keyring` (C-001).
- Risks R1, R2 acknowledged with documented mitigations.
- Handoff: WP13 (XDG portal) replaces the v1 sandbox-warning path with full portal routing.
