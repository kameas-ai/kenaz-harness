---
work_package_id: "WP09"
title: "HTTP LAN transport (opt-in)"
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
phase: "Phase 9 - HTTP LAN transport"
assignee: ""
agent: ""
shell_pid: ""
history:
  - timestamp: "2026-04-25T00:00:00Z"
    agent: "system"
    action: "Prompt generated via /spec-kitty.tasks"
---

# Work Package Prompt: WP09 – HTTP LAN transport (opt-in)

## Goal

Implement `core/acp/transports/http_lan/` — an HTTP listener bound
to a non-loopback local network interface (RFC1918 / IPv6 ULA), plus
a matching Dialer that refuses public Internet hosts but allows LAN
ranges. Strictly opt-in — bundle authors must explicitly set
`transport: http_lan`; defaults never select it.

DIRECTIVE_001: this transport ships as its own self-contained sub-
package.

## Spec references

- FR-007 — Local-first transport defaults (LAN requires explicit
  configuration).
- FR-016 — Transport extensibility.
- NFR-005 — Local-first guarantee (no traffic when no peer
  configured).
- C-006 — Unsigned cards over loopback/LAN only in v1.

## Plan references

- §2 Architectural Placement — `core/acp/transports/http_lan/`.
- §4 Internal Layering, "Transports" — bind/dial scope is LAN.
- §7 v1.0 scope — LAN included; public exposure is opt-in only.

## Subtasks

- T001 — Implement `Listen(ctx, bindAddr) (net.Listener, error)`
  that accepts only RFC1918 IPv4 (`10/8`, `172.16/12`,
  `192.168/16`) or IPv6 ULA (`fc00::/7`) bind addresses; reject
  loopback (delegated to WP08), public, and `0.0.0.0` /`::` with
  `ErrTransportRefused`.
- T002 — Implement `Dial(ctx, endpointURL) (net.Conn, error)` that
  resolves the URL, classifies the resolved address, and refuses
  with `ErrTransportRefused` if the resolved host is public. Allow
  resolved addresses in RFC1918 / ULA ranges.
- T003 — Wrap with envelope HTTP transport seam (same JSON-RPC 2.0
  pipeline as WP08 loopback).
- T004 — Register the transport via a single `init()` (FR-016).
- T005 — Tests:
  - Successful round-trip on a fixture LAN address (e.g., bind to
    a test interface or use a fake resolver).
  - Refusal: bind to `8.8.8.8` returns `ErrTransportRefused`.
  - Refusal: dial against a hostname resolving to a public IP
    returns `ErrTransportRefused`.
  - Allow: dial against a hostname resolving to `192.168.1.5`
    succeeds (against a fake resolver).
  - Default-not-selected test: an unspecified transport never
    falls back to `http_lan`.

## Acceptance criteria

- `go test ./core/acp/transports/http_lan/...` passes; coverage
  ≥ 80%.
- Bind/dial classification is correct across the IPv4/IPv6 range
  test matrix.
- A bundle that does not explicitly request `http_lan` never
  selects this transport (verified by a default-resolution test
  in WP03 / WP04 dependents).
- A grep across `core/` confirms only the envelope imports this
  package outside the registration `init()`.

## Files to create / modify

- `core/acp/transports/http_lan/http_lan.go`
- `core/acp/transports/http_lan/lan_check.go` — RFC1918 / ULA
  classification.
- `core/acp/transports/http_lan/http_lan_test.go`

## Definition of done

- All subtasks complete; tests green; lint clean.
- DIRECTIVE_001: no other package modified beyond the envelope
  registry seam.
- PR merged.
