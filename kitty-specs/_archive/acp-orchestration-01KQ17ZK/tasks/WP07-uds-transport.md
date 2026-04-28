---
work_package_id: "WP07"
title: "UDS transport (default macOS/Linux)"
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
phase: "Phase 7 - UDS transport"
assignee: ""
agent: ""
shell_pid: ""
history:
  - timestamp: "2026-04-25T00:00:00Z"
    agent: "system"
    action: "Prompt generated via /spec-kitty.tasks"
---

# Work Package Prompt: WP07 – UDS transport (default macOS/Linux)

## Goal

Implement `core/acp/transports/uds/` — a Unix domain socket Listener
and Dialer that satisfies the WP02 envelope `Dialer` and `Listener`
interfaces. UDS is the default local transport on macOS/Linux per
the spec; filesystem permissions provide the access-control surface.
On Windows hosts, the transport refuses to construct (WP03 substitutes
`http_loopback` at bundle-load time).

DIRECTIVE_001 contract: this transport ships as its own self-contained
sub-package; adding or modifying it MUST NOT touch any other package
beyond a single registration call.

## Spec references

- FR-007 — Local-first transport defaults (UDS on macOS/Linux).
- FR-016 — Transport extensibility (this WP exemplifies the pattern).
- NFR-005 — Local-first guarantee (no outbound traffic until invoked).
- NFR-006 — Default-transport binding scope (refuses non-local).
- C-001 — Architectural-integrity boundary.
- US5 Acceptance Scenario 3 — non-loopback caller refused at
  transport layer.
- Edge case: Windows host with `transport: uds` → transport package
  reports the platform error to bundle resolver for substitution.

## Plan references

- §2 Architectural Placement — `core/acp/transports/uds/`.
- §4 Internal Layering, "Transports" — `Dial(ctx, endpoint) →
  net.Conn`, `Listen(ctx, addr) → net.Listener`.
- §8 R3 — UDS-vs-localhost-TCP cross-platform divergence.

## Subtasks

- T001 — Implement `Listen(ctx, socketPath) (net.Listener, error)`
  using `net.Listen("unix", path)`. Set socket file permissions to
  `0600` (owner-only). Clean up stale socket files on bind failure.
- T002 — Implement `Dial(ctx, socketPath) (net.Conn, error)` using
  `net.Dial("unix", path)`. Honor `ctx` cancellation.
- T003 — Build-tag platform guard: `transports/uds` compiles only on
  GOOS in {`darwin`, `linux`, `freebsd`}. On Windows the package
  exports a stub that returns `ErrTransportRefused` from constructor;
  the WP03 schema validator detects this at bundle load.
- T004 — Register the transport with the WP02 envelope dialer/
  listener registry via a single `init()` call (FR-016 — additions
  touch nothing else).
- T005 — Tests:
  - Successful round-trip dial+listen on a temp socket.
  - Permission test: socket file mode is `0600`.
  - Stale-socket cleanup test: pre-existing socket file at the path
    is removed before bind retries.
  - Refusal test: an attempt to connect via TCP (not UDS) fails at
    the transport layer (NFR-006; US5 Acceptance 3).

## Acceptance criteria

- `go test ./core/acp/transports/uds/...` passes on darwin and
  linux; on Windows the package's only export refuses to construct.
- Coverage ≥ 80% on darwin/linux.
- Socket file mode is `0600` on every successful listen.
- A grep across `core/` confirms only `core/acp/envelope/` (via the
  Dialer interface) imports anything from `transports/uds/` outside
  the registration `init()`.
- Adding a probe import of `a2a-go` to this package is rejected by
  the WP02 depguard rule.

## Files to create / modify

- `core/acp/transports/uds/uds.go` — listener + dialer
  (build tag: `darwin || linux || freebsd`).
- `core/acp/transports/uds/uds_windows.go` — stub
  (build tag: `windows`).
- `core/acp/transports/uds/uds_test.go`.

## Definition of done

- All subtasks complete; tests green; lint clean.
- DIRECTIVE_001: no other package modified by this WP beyond the
  envelope's transport registry seam.
- PR merged.
