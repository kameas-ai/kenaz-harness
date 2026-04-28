---
work_package_id: "WP01"
title: "Core contracts and public types in core/acp"
dependencies: []
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
phase: "Phase 1 - Core skeleton"
assignee: ""
agent: ""
shell_pid: ""
history:
  - timestamp: "2026-04-25T00:00:00Z"
    agent: "system"
    action: "Prompt generated via /spec-kitty.tasks"
---

# Work Package Prompt: WP01 – Core contracts and public types in core/acp

## Goal

Establish the canonical, SDK-agnostic Go types and interfaces in
`core/acp/acp.go` that every downstream WP depends on: `TransportKind`,
`Skill`, `AgentCard`, `PeerProfile`, `Task`, `TaskRole`, `TaskState`,
`Message`, `CardSource`, plus the `PeerRegistry`, `A2AClient`, `A2AServer`,
`SkillDispatcher`, and `Verifier` interfaces. Define the typed error
taxonomy and the package's documented architectural invariants.

This package is the harness's single public seam for the A2A protocol; no
type defined here may import or expose `github.com/a2aproject/a2a-go`.

## Spec references

- FR-001 — A2A v1.0 protocol conformance (drives `AgentCard.ProtocolVersion`).
- FR-002 — Outbound A2A client (drives `A2AClient` interface).
- FR-003 — Inbound A2A server (drives `A2AServer`, `SkillDispatcher`).
- FR-004 — Peer Profile bundle artifact (drives `PeerProfile`).
- FR-005 — Exposed-agent bundle artifact (drives `Skill`, `AgentCard`).
- FR-007 — Local-first transport defaults (drives `TransportKind` constants).
- FR-009 — Task lifecycle management (drives `TaskState`, `TaskRole`, `Task`).
- FR-010 — Mid-Task cancellation (drives `A2AClient.Cancel`).
- FR-015 — SDK isolation behind envelope wrapper (drives package boundary).
- FR-016 — Transport extensibility (drives transport registration seam).
- FR-017 — Multi-peer fan-out support (drives `Dispatch` channel return).
- FR-020 — Verification API consumer slot (drives `Verifier` interface).
- C-001 — Architectural-integrity boundary (no a2a-go in this file).
- C-003 — Bundle-format compatibility (no new top-level config types).

## Plan references

- §2 Architectural Placement — placement of `core/acp/acp.go` and
  package boundary; CI guard rule on a2a-go imports.
- §3 Public API (Illustrative Signatures) — full canonical signature
  set this WP materializes.
- §4 Internal Layering — interfaces this WP defines that subsequent
  WPs implement.

## Subtasks

- T001 — Create `core/acp/` directory layout with `doc.go` documenting
  the architectural invariant (only `core/acp/envelope/` imports
  `a2a-go`; everything else flows through these types) and the
  `core/acp/internal/` shared helpers area.
- T002 — Define `TransportKind` enum with constants `TransportUDS`,
  `TransportLoopback`, `TransportLAN`, `TransportPublic`; define
  `CardSource` enum with `inline`, `well_known`, `manual_url`.
- T003 — Define `Skill` and `AgentCard` value types matching plan §3
  (including `ProtocolVersion`, `Signed`, `EndpointURL`, JSON-Schema
  fields as `json.RawMessage`).
- T004 — Define `PeerProfile` (with `AuthRef *secrets.Reference`
  placeholder type, `CardCacheTTL`, `Transport`, `CardSource`,
  `InlineCard`); define `TaskRole`, `TaskState`, `Task`, `Message`
  per plan §3; document state-machine transitions in comments.
- T005 — Define interfaces: `PeerRegistry` (Load / Lookup / All /
  PreflightAll), `A2AClient` (Dispatch / Cancel), `A2AServer` (Expose
  / Start / Stop), `SkillDispatcher`, `Verifier`. Add `DispatchOption`
  variadic option pattern and `PreflightResult` value type.
- T006 — Define typed error taxonomy in `core/acp/errors.go`:
  `ErrPeerNotFound`, `ErrCredentialResolution`, `ErrPolicyDenied`,
  `ErrSkillNotFound`, `ErrUnsupportedProtocolVersion`,
  `ErrTransportRefused`, `ErrCancelled`, `ErrTaskFailed`,
  `ErrSchemaViolation`. Provide classification helpers
  (`IsTransient(err) bool`, `IsRefusal(err) bool`).
- T007 — Add table-driven unit tests under `core/acp/acp_test.go`
  covering enum round-trips, error classification, and state-machine
  validation (no backwards transitions, terminal-state immutability).

## Acceptance criteria

- `go build ./core/acp/...` succeeds with no `a2a-go` imports.
- `go vet ./core/acp/...` clean.
- `go test ./core/acp/...` passes; coverage on `core/acp/acp.go` and
  `core/acp/errors.go` ≥ 80%.
- `go list -deps ./core/acp | grep a2aproject` returns nothing.
- The state-machine validator rejects every illegal transition listed
  in the data-model doc (e.g., `completed → running`, `cancelled →
  completed`).

## Files to create / modify

- `core/acp/acp.go` — public types and interfaces.
- `core/acp/errors.go` — typed error taxonomy + classification helpers.
- `core/acp/doc.go` — package doc string referencing the
  architectural invariant.
- `core/acp/acp_test.go` — table-driven tests.
- `core/acp/internal/.gitkeep` — placeholder for shared helpers
  package created in later WPs.

## Definition of done

- All subtasks complete; tests green; `go vet` and `golangci-lint run`
  clean for `./core/acp/...`.
- Public types match plan §3 signatures; deviations recorded in commit
  message or ADR per DIRECTIVE_003.
- PR opened against `feat/acp-orchestration-01KQ17ZK` targeting
  `main`, ≥ 1 maintainer approval, squash-merge.
