---
work_package_id: "WP12"
title: "YubiKey PIV backend (go-piv/piv-go, pure Go)"
dependencies:
  - "WP07"
planning_base_branch: "main"
merge_target_branch: "main"
branch_strategy: "Planning artifacts were generated on main; completed changes must merge back into main."
subtasks:
  - "T001: Implement YubiKey PIV Backend wrapping go-piv/piv-go v2.6.0"
  - "T002: Map PC/SC errors to FR-014 typed errors"
  - "T003: Implement Health() probe (PC/SC reader presence)"
  - "T004: Black-box tests with software-emulated PIV applet (piv-go test fixtures)"
  - "T005: Document required pcsclite version per platform (R5)"
  - "T006: Backend opt-in only; never default"
phase: "Phase 12 - YubiKey PIV"
assignee: ""
agent: ""
shell_pid: ""
history:
  - timestamp: "2026-04-25T00:00:00Z"
    agent: "system"
    action: "Prompt generated via /spec-kitty.tasks"
---

# Work Package Prompt: WP12 – YubiKey PIV backend (go-piv/piv-go, pure Go)

## Goal

Ship the hardware-token slot using `go-piv/piv-go` v2.6.0. Pure-Go (no CGo) over OS PC/SC — macOS / Windows built-in PC/SC; Linux libpcsclite. This is the regulated-deployment backend that handles PIV slots; PKCS#11 is deferred to v2.

## Spec references

- FR-008 (HSM backend, optional, enterprise): hardware-token slot for regulated operators. (PIV is the v1 implementation; PKCS#11 deferred per research D5.)
- FR-014 (Error taxonomy).
- FR-017 (Backend health probe): PC/SC reader presence.
- C-006 (Fail-closed).

## Plan references

- §2 Architectural placement → `core/secrets/backends/yubikey/`.
- §7 Phasing → v1.0 ships `yubikey` via go-piv/piv-go v2.6.0.
- §8 Risk register → R5 (Linux libpcsclite version skew).
- §12 Acceptance mapping → FR-008 partially maps here (PIV slot only; PKCS#11 deferred).
- Research D5 → PIV is universal; PKCS#11 deferred past v1.

## Subtasks

- Implement `Backend` for YubiKey PIV at `core/secrets/backends/yubikey/yubikey.go` wrapping `go-piv/piv-go` v2.6.0.
- Resolve `RefYubikeyPIV` references where the locator is a slot identifier.
- Map PC/SC errors to FR-014 sentinel errors: card-absent → `ErrBackendUnavailable`; PIN-locked → `ErrPermissionDenied`; missing slot → `ErrReferenceNotFound`.
- Implement `Health()` probe checking PC/SC reader presence; emits `degraded` if reader absent.
- Backend is opt-in only — never default. Document the opt-in flow in the package doc.
- Document required `pcsclite` version per platform (Linux) per R5; verify on macOS (built-in) and Windows (built-in WinSCard).
- Black-box integration tests with software-emulated PIV applet (go-piv/piv-go ships fixtures); cover ok, card-absent, PIN-locked, missing-slot.

## Acceptance criteria

- `core/secrets/backends/yubikey/yubikey.go` compiles on macOS, Linux (with pcsclite available), and Windows.
- Backend registered for `RefYubikeyPIV` only after explicit operator opt-in.
- Software-emulated tests pass; covered behaviors are ok / card-absent / PIN-locked / missing-slot.
- Health probe correctly reports `degraded` when no reader present.
- Charter quality gates pass.

## Files to create / modify

- Create `core/secrets/backends/yubikey/yubikey.go`.
- Create `core/secrets/backends/yubikey/yubikey_test.go`.
- Update `go.mod` / `go.sum` to add `github.com/go-piv/piv-go/v2`.

## Definition of done

- FR-008 (PIV portion) and FR-017 acceptance scenarios traceable to tests in this WP.
- Resolver routes `yubikey_piv:` references through this backend after registration.
- Architectural integrity preserved: only this package imports `go-piv/piv-go` (C-001).
- Risk R5 acknowledged; pcsclite version documented.
- Handoff: PKCS#11 / general HSM backend is deferred to v2; this backend covers the v1 hardware-token slot.
