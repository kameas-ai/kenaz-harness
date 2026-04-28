---
work_package_id: "WP02"
title: "Envelope SDK wrapper around a2a-go and import-guard CI rule"
dependencies:
  - "WP01"
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
phase: "Phase 2 - Envelope wrapper"
assignee: ""
agent: ""
shell_pid: ""
history:
  - timestamp: "2026-04-25T00:00:00Z"
    agent: "system"
    action: "Prompt generated via /spec-kitty.tasks"
---

# Work Package Prompt: WP02 – Envelope SDK wrapper around a2a-go and import-guard CI rule

## Goal

Implement `core/acp/envelope/` — the SOLE Go package permitted to import
`github.com/a2aproject/a2a-go`. Translate between `core/acp.*` types and
SDK request/response shapes; expose envelope-level operations
(`Dispatch`, `FetchCard`, `CancelTask`, `AcceptTask`, `Respond`) used by
the client and server packages. Pin the SDK in `go.mod` and add a
golangci-lint `depguard` rule that blocks `a2a-go` imports anywhere
outside `core/acp/envelope/`.

## Spec references

- FR-001 — A2A v1.0 protocol conformance (envelope speaks the SDK).
- FR-015 — SDK isolation behind envelope wrapper (the headline rule
  this WP enforces).
- FR-016 — Transport extensibility (envelope is transport-neutral; it
  consumes `net.Conn` / dialer abstractions).
- C-001 — Architectural-integrity boundary.
- NFR-010 — SDK upgrade blast radius.
- US6 Acceptance Scenarios 1, 2.
- SC-006 — Non-breaking SDK upgrade contained to envelope.

## Plan references

- §2 Architectural Placement — `core/acp/envelope/` as sole importer.
- §4 Internal Layering — envelope responsibilities, no SDK shapes
  leak past it.
- §8 R1 — SDK API churn; envelope is the seam.
- §9 Q2 — direct module dependency, pinned minor version, CI gate
  for major bumps.

## Subtasks

- T001 — Add `github.com/a2aproject/a2a-go` to `go.mod` pinned to a
  specific minor version; run `go mod tidy`; document the pin choice
  and upgrade procedure in `core/acp/envelope/UPGRADE.md`.
- T002 — Define envelope-internal converters: `toSDKCard`,
  `fromSDKCard`, `toSDKTask`, `fromSDKTask`, `toSDKMessage`,
  `fromSDKMessage`. SDK types live only in unexported helpers; every
  exported envelope function takes/returns `core/acp.*` types.
- T003 — Implement `Envelope.Dispatch(ctx, peer, task, body) (<-chan
  Message, error)`: builds an SDK request, hands off to a transport
  Dialer (interface defined here, implemented per-transport in later
  WPs), pumps SDK stream events onto a `core/acp.Message` channel.
- T004 — Implement `Envelope.FetchCard(ctx, peer) (AgentCard, error)`,
  `Envelope.CancelTask(ctx, a2aTaskID) error`, `Envelope.AcceptTask`
  (server-side handler that the server WP wires its skill router into),
  `Envelope.Respond` for inbound responses.
- T005 — Add `golangci-lint` `depguard` configuration (or equivalent
  static-import linter) under `.golangci.yml` that denies imports of
  `github.com/a2aproject/a2a-go` from any package outside
  `core/acp/envelope/...`. Add a CI workflow step that runs the lint.
- T006 — Tests using the SDK's in-process test server (per plan §7
  testing approach): round-trip a fixture Task through the envelope
  and assert the converted `core/acp.Message` stream matches an
  expected golden sequence; assert SDK error structures map onto the
  WP01 typed error taxonomy.

## Acceptance criteria

- `go test ./core/acp/envelope/...` passes; coverage ≥ 80%.
- A test (or `go list -deps`) confirms `core/acp/envelope` is the
  only `core/` importer of `a2aproject/a2a-go`.
- `golangci-lint run` fails when a probe import of `a2aproject/a2a-go`
  is added to any non-envelope package; passes when removed.
- Envelope public surface uses zero `a2a-go` types in signatures;
  verified by a structural assertion test.
- Upgrade procedure document committed under
  `core/acp/envelope/UPGRADE.md`.

## Files to create / modify

- `go.mod`, `go.sum` (pin a2a-go).
- `core/acp/envelope/envelope.go` — public envelope operations.
- `core/acp/envelope/convert.go` — type converters.
- `core/acp/envelope/dialer.go` — transport Dialer interface.
- `core/acp/envelope/envelope_test.go` — round-trip tests.
- `core/acp/envelope/UPGRADE.md` — upgrade procedure / ADR pointer.
- `.golangci.yml` — depguard rule.
- `.github/workflows/*.yml` (or equivalent) — CI lint step.

## Definition of done

- All subtasks complete; tests green; lint clean.
- Static import analysis confirms FR-015 / C-001 invariant.
- Cross-mission: no dependency on other missions for this WP.
- PR merged to `feat/acp-orchestration-01KQ17ZK` with ≥ 1 maintainer
  review.
