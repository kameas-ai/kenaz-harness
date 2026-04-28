---
work_package_id: "WP10"
title: "HTTP public (WAN) transport — escalated, auth-gated"
dependencies:
  - "WP01"
  - "WP02"
  - "WP04"
  - "secrets-keychain:WP-resolve-and-zeroize"
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
phase: "Phase 10 - HTTP public transport"
assignee: ""
agent: ""
shell_pid: ""
history:
  - timestamp: "2026-04-25T00:00:00Z"
    agent: "system"
    action: "Prompt generated via /spec-kitty.tasks"
---

# Work Package Prompt: WP10 – HTTP public (WAN) transport — escalated, auth-gated

## Goal

Implement `core/acp/transports/http_public/` — the WAN-bound HTTP
Dialer (and listener stub) used to reach A2A peers over the public
Internet. The transport REFUSES to construct without:
1. A resolved `auth_ref` (C-005),
2. A non-no-op `Verifier` bound (C-006 — unsigned cards prohibited
   over public transport).

Signed-card verification is delivered by the
`a2a-signed-cards-trust-01KQ18P9` follow-up; this WP ships the
transport with the C-006 guardrail in place so the harness cannot
ship a v1 footgun even if an operator wires `http_public` without
the trust mission.

DIRECTIVE_001: own sub-package.

## Spec references

- FR-007 — Local-first transport defaults.
- FR-016 — Transport extensibility.
- FR-020 — Verification API consumer slot (transport refuses no-op
  Verifier).
- C-005 — Public exposure requires escalation.
- C-006 — Unsigned cards over loopback/LAN only in v1; public
  exposure with unsigned cards is prohibited.
- US5 Acceptance Scenario 2 — peer with `transport: http_public`
  but no `auth_ref` rejected at bundle load.
- SC-007 — Peer profile rejection 100% across the configuration
  matrix.

## Plan references

- §2 Architectural Placement — `core/acp/transports/http_public/`.
- §4 Internal Layering, "Transports" — refuses construction without
  resolved `auth_ref`; refuses to dial/listen if Verifier is the
  no-op variant.
- §7 v1.0 scope — public listener still ships with refusal
  guardrail; full authorization flow lands in v1.x.
- §8 R8 — public transport ships before signed-card trust mission;
  guardrail enforces C-006.

## Subtasks

- T001 — Implement `New(cfg) (Transport, error)` constructor that
  REFUSES with `ErrCredentialResolution` if `cfg.AuthRef` did not
  resolve to a usable `secrets.Secret`, and refuses with
  `ErrTransportRefused` if the bound `Verifier` is the
  `UnsignedAcceptVerifier` (detected by interface assertion or
  explicit marker).
- T002 — Implement `Dial(ctx, endpointURL) (net.Conn, error)` for
  HTTPS endpoints. TLS verification is on by default; minimum TLS
  1.2 (1.3 preferred). Plain HTTP is refused.
- T003 — Implement listener path (limited v1 scope per plan §7;
  v1.x extends): refuses to bind without `auth_ref` AND non-no-op
  Verifier. v1 may ship listener as `ErrTransportRefused` stub if
  the upstream policy-engine charter-approval flow is not yet in
  place; document the limitation in package doc.
- T004 — Inject the resolved credential (via WP05's secrets path)
  into the wire request as the configured auth header / scheme,
  then zeroize the in-memory `Secret` immediately after the request
  is built (defense in depth; FR-006).
- T005 — Register the transport via a single `init()` (FR-016).
- T006 — Tests:
  - Refusal: construct with no `auth_ref` → `ErrCredentialResolution`.
  - Refusal: construct with no-op `Verifier` →
    `ErrTransportRefused` (C-006 guardrail).
  - Refusal: dial against a plain `http://` URL → refused.
  - Success: dial against a fixture HTTPS endpoint with a resolved
    auth ref returns a working connection (test uses a self-signed
    fixture cert pinned in the test pool).
  - Refusal at bundle load: a bundle declaring
    `transport: http_public` without `auth` produces a load-time
    error 100% of the time (SC-007).

## Acceptance criteria

- `go test ./core/acp/transports/http_public/...` passes; coverage
  ≥ 80%.
- All four refusal paths (no `auth_ref`, no-op Verifier, plain HTTP,
  bundle-load missing-auth) trigger reliably across the
  configuration matrix; tests parameterized over the matrix
  (SC-007).
- TLS minimum version enforced; verified by a test against a TLS
  1.0 fixture server.
- A grep across `core/` confirms only the envelope imports this
  package outside the registration `init()`.

## Files to create / modify

- `core/acp/transports/http_public/http_public.go`
- `core/acp/transports/http_public/refusal.go` — guardrail logic.
- `core/acp/transports/http_public/http_public_test.go`
- `core/acp/transports/http_public/testdata/` — TLS fixture certs.

## Definition of done

- All subtasks complete; tests green; lint clean.
- DIRECTIVE_001: no other package modified beyond the envelope
  registry seam.
- C-006 guardrail compiles and runs without the
  `a2a-signed-cards-trust` mission landed; once that mission lands,
  binding the real `Verifier` removes the refusal.
- PR merged.
