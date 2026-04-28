---
work_package_id: "WP08"
title: "HTTP loopback transport (default Windows; opt-in elsewhere)"
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
phase: "Phase 8 - HTTP loopback transport"
assignee: ""
agent: ""
shell_pid: ""
history:
  - timestamp: "2026-04-25T00:00:00Z"
    agent: "system"
    action: "Prompt generated via /spec-kitty.tasks"
---

# Work Package Prompt: WP08 – HTTP loopback transport (default Windows; opt-in elsewhere)

## Goal

Implement `core/acp/transports/http_loopback/` — an HTTP listener
bound to `127.0.0.1` / `::1` only on an ephemeral port, plus a
matching Dialer that refuses non-loopback hosts. Default transport
on Windows hosts; opt-in elsewhere. Refuses any bind or dial against
a non-local address at the transport layer.

DIRECTIVE_001: this transport ships as its own self-contained sub-
package.

## Spec references

- FR-007 — Local-first transport defaults (`http_loopback` on
  Windows).
- FR-016 — Transport extensibility.
- NFR-005 — Local-first guarantee.
- NFR-006 — Default-transport binding scope (refuses non-local).
- US5 Acceptance Scenario 3 — non-loopback caller refused at
  transport layer.

## Plan references

- §2 Architectural Placement — `core/acp/transports/http_loopback/`.
- §4 Internal Layering, "Transports" — listener bound to 127.0.0.1
  / ::1, dialer refuses non-loopback.
- §8 R3 — Windows automatic substitution.

## Subtasks

- T001 — Implement `Listen(ctx, addrSpec) (net.Listener, error)`
  via `net.Listen("tcp", "127.0.0.1:0")` (or `[::1]:0` when IPv6
  configured). Refuse explicit non-loopback bind addresses with
  `ErrTransportRefused`. The chosen ephemeral port is exposed via
  the listener's `Addr()` so the harness can include it in the
  generated AgentCard `endpoint_url`.
- T002 — Implement `Dial(ctx, endpointURL) (net.Conn, error)` over
  HTTP/1.1 + HTTP/2 to a parsed loopback URL; reject any URL whose
  resolved host is not loopback (`127.0.0.0/8`, `::1`,
  `localhost`-only) with `ErrTransportRefused`.
- T003 — Wrap with the WP02 envelope's HTTP transport seam so the
  envelope can run JSON-RPC 2.0 over this loopback connection.
- T004 — Register the transport via a single `init()` call (FR-016).
- T005 — Tests:
  - Successful round-trip dial+listen against ephemeral 127.0.0.1.
  - Refusal: bind attempt against `0.0.0.0` returns
    `ErrTransportRefused`.
  - Refusal: dial attempt against an external IP (e.g.,
    `93.184.216.34` aka example.com) returns `ErrTransportRefused`
    without touching the network (verified by intercepting at the
    URL-validation step).
  - Refusal: dial attempt against an external hostname that
    resolves off-loopback returns `ErrTransportRefused`.

## Acceptance criteria

- `go test ./core/acp/transports/http_loopback/...` passes on all
  GOOS targets; coverage ≥ 80%.
- Listener never binds to a non-loopback address (NFR-006).
- Dialer never establishes a TCP connection to a non-loopback host
  (NFR-006; US5 Acceptance 3).
- A grep across `core/` confirms only the envelope imports this
  package outside the registration `init()`.

## Files to create / modify

- `core/acp/transports/http_loopback/http_loopback.go`
- `core/acp/transports/http_loopback/http_loopback_test.go`
- `core/acp/transports/http_loopback/loopback_check.go` — host
  classification helper (loopback vs not).

## Definition of done

- All subtasks complete; tests green; lint clean.
- DIRECTIVE_001: no other package modified beyond the envelope
  registry seam.
- PR merged.
