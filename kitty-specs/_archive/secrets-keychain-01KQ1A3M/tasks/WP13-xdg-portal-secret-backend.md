---
work_package_id: "WP13"
title: "XDG portal Secret backend (Linux Flatpak/Snap, deferred to v1.x)"
dependencies:
  - "WP10"
planning_base_branch: "main"
merge_target_branch: "main"
branch_strategy: "Planning artifacts were generated on main; completed changes must merge back into main."
subtasks:
  - "T001: Implement org.freedesktop.portal.Secret client via godbus/dbus"
  - "T002: Replace OS keychain Linux sandbox warning with active portal routing"
  - "T003: Encrypt local credential store with portal-issued master secret"
  - "T004: Map portal errors to FR-014 typed errors"
  - "T005: Black-box tests in a Flatpak/Snap simulated sandbox"
  - "T006: Backend gated behind v1.x release flag if Flatpak/Snap distribution lands"
phase: "Phase 13 - XDG Portal (deferred)"
assignee: ""
agent: ""
shell_pid: ""
history:
  - timestamp: "2026-04-25T00:00:00Z"
    agent: "system"
    action: "Prompt generated via /spec-kitty.tasks"
---

# Work Package Prompt: WP13 – XDG portal Secret backend (Linux Flatpak/Snap, deferred to v1.x)

## Goal

Ship the `org.freedesktop.portal.Secret` backend that replaces the v1 sandbox-warning fallback on Linux Flatpak / Snap deployments. The portal returns a per-app master secret usable to encrypt a local store. This WP is gated to the v1.x release line, conditional on Flatpak/Snap distribution actually landing in the project's release plan (research Open Question 1).

## Spec references

- FR-003 (OS keychain backend, default): cross-platform parity, including Linux sandboxed environments.
- NFR-004 (Cross-platform parity).
- C-006 (Fail-closed): no silent fallback to a less-secure backend.
- Edge case: "The Linux secret-storage daemon is not running... fall back to kernel keyring with a recorded warning, or fail closed if neither is available." (Plan adopts D2: portal first in sandboxed builds, then explicit-opt-in file backend.)

## Plan references

- §2 Architectural placement → `core/secrets/backends/xdgportal/`.
- §4 Internal layering → "Sandbox detection (Linux)" subsection.
- §7 Phasing → v1.x (`xdgportal` follow-up); depends on whether v1 ships Flatpak/Snap targets.
- §12 Acceptance mapping → FR-003 (Linux sandboxed coverage), NFR-004 map here.
- Research D2 → portal is the second tier in the Linux fallback chain.

## Subtasks

- Implement `Backend` for XDG portal Secret at `core/secrets/backends/xdgportal/xdgportal.go` using `godbus/dbus` to call `org.freedesktop.portal.Secret`.
- Wire the backend into Linux sandbox detection from WP10: when `/.flatpak-info` or `SNAP` is present, route through the portal instead of direct D-Bus.
- Use the portal-issued master secret to encrypt the local credential store; persist the store in the per-app sandbox dir.
- Map portal errors (timeout, permission denied, no-portal) to FR-014 sentinel errors.
- Black-box integration tests in a simulated Flatpak/Snap sandbox (mock the D-Bus surface or use containerized fixtures).
- Gate the backend behind a v1.x release flag — this WP only ships when Flatpak/Snap distribution lands on the release plan; otherwise the WP remains in `kitty-specs` as a planned-but-not-built artifact.

## Acceptance criteria

- `core/secrets/backends/xdgportal/xdgportal.go` compiles on Linux when the v1.x build flag is set.
- Linux sandbox detection routes through this backend automatically when `/.flatpak-info` or `SNAP` is present.
- Portal errors map to typed FR-014 errors.
- Black-box tests cover ok, no-portal, permission-denied, and the local-store encryption round-trip.
- Charter quality gates pass.

## Files to create / modify

- Create `core/secrets/backends/xdgportal/xdgportal.go` (Linux build tag).
- Create `core/secrets/backends/xdgportal/store.go` (local encrypted store helpers).
- Create `core/secrets/backends/xdgportal/xdgportal_test.go`.
- Update `core/secrets/backends/oskeychain/sandbox_linux.go` to dispatch to the portal backend when sandboxed.
- Update `go.mod` / `go.sum` to add `github.com/godbus/dbus/v5`.

## Definition of done

- FR-003 (Linux sandboxed) and NFR-004 acceptance scenarios traceable to tests in this WP, contingent on the v1.x release flag.
- Resolver and OS-keychain backend cleanly hand off to the portal backend in sandboxed Linux deployments.
- Architectural integrity preserved: only this package imports the D-Bus client (C-001).
- Open Question 1 (research) resolved at the time this WP is scheduled: Flatpak/Snap distribution decision recorded in the release plan.
- Defer notice: if Flatpak/Snap distribution does not land in v1, this WP remains queued as a v1.x artifact with no code merged.
