---
work_package_id: "WP03"
title: "Bundle artifact kinds: a2a_peer and expose_over_a2a"
dependencies:
  - "WP01"
  - "bundle-format-resolver:WP-artifact-kind-handler"
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
phase: "Phase 3 - Bundle artifact kinds"
assignee: ""
agent: ""
shell_pid: ""
history:
  - timestamp: "2026-04-25T00:00:00Z"
    agent: "system"
    action: "Prompt generated via /spec-kitty.tasks"
---

# Work Package Prompt: WP03 – Bundle artifact kinds: a2a_peer and expose_over_a2a

## Goal

Register two `ArtifactKindHandler`s with the bundle resolver:
`a2a_peer` (declares an outbound A2A peer) and an `expose_over_a2a`
extension to the existing agent-definition artifact (declares a local
agent's Skills as A2A endpoints). Implement YAML schemas, validation
rules, and parse → validate → activate handlers per the bundle
resolver's contract.

## Spec references

- FR-004 — Peer Profile bundle artifact (`a2a_peer`).
- FR-005 — Exposed-agent bundle artifact (`expose_over_a2a`).
- FR-007 — Local-first transport defaults (validation rejects
  `http_public` without `auth`).
- FR-018 — Pre-flight peer resolution (bundle-load-time validation
  surfaces failures as actionable startup errors).
- C-003 — Bundle-format compatibility (no new top-level config
  surface).
- C-004 — No inline plaintext credentials.
- C-005 — Public exposure requires escalation.
- US1 Acceptance Scenario 3 — credential reference missing, startup
  error.
- US5 Acceptance Scenario 2 — `http_public` without `auth_ref`,
  bundle-load refusal.

## Plan references

- §5.1 Bundle artifacts — full YAML schema, validation rules.
- §6.3 bundle-format-resolver integration — `Parse / Validate /
  Activate` handler signatures.
- §2 Architectural Placement — handlers live under `core/acp/peers/`
  for peers and the existing agent-definition handler is extended for
  exposure.

## Subtasks

- T001 — Define Go structs for the on-disk YAML shape of
  `a2a_peer` and the `expose_over_a2a` extension under
  `core/acp/peers/schema.go`; document field requirements and the
  Windows `uds → http_loopback` substitution rule.
- T002 — Implement `Parse(bytes) → PeerProfile` and `Parse(bytes) →
  ExposedAgentSpec` handlers; reject unknown fields strictly so
  schema drift surfaces early.
- T003 — Implement `Validate(profile, ManifestCtx) → []error`: enforce
  unique `peer_id`, transport enum membership, `http_public` requires
  `auth` (C-005), inline cards require `name` + `endpoint_url` + ≥ 1
  Skill, `card_cache_ttl_s` non-negative, no plaintext credentials in
  any field (C-004).
- T004 — Implement `Activate` for both kinds: peers register with the
  WP04 `PeerRegistry`; exposed agents register with the WP06
  `SkillRouter`. Activation order follows
  `ResolvedGraph.activation_order`; collisions surface via the
  resolver's conflict-detection path (bundle FR-009).
- T005 — Wire the two handlers into the bundle resolver's
  `ArtifactKindHandler` registry at process start (via the seam the
  bundle-format-resolver mission exposes).
- T006 — Black-box tests: a fixture bundle declares peers across all
  four transport kinds; a second fixture declares an exposed agent
  with two Skills; tests confirm successful resolution, then
  parameterized failure tests for each rejection rule (missing
  `auth_ref` + `http_public`, duplicate `peer_id`, plaintext key in
  YAML, unknown transport).

## Acceptance criteria

- `go test ./core/acp/peers/...` passes; coverage ≥ 80%.
- A bundle with `transport: http_public` and no `auth` produces a
  bundle-load error citing the peer id, before any A2A call (US5
  Acceptance 2; SC-007).
- A bundle with a plaintext credential anywhere in the peer YAML
  produces a bundle-load error.
- A Windows host loading a `transport: uds` peer produces a
  configuration warning AND substitutes `http_loopback`; the bundle
  loads successfully.
- Activation order is deterministic across repeated loads of the same
  resolved graph (test asserts identical registration sequence).

## Files to create / modify

- `core/acp/peers/schema.go` — on-disk YAML structs.
- `core/acp/peers/handlers.go` — `Parse / Validate / Activate` for
  `a2a_peer`.
- `core/acp/peers/exposed_handler.go` — extension for
  agent-definition artifacts.
- `core/acp/peers/handlers_test.go` — black-box fixture tests.
- `core/acp/peers/testdata/` — fixture bundles for success and
  failure cases.

## Definition of done

- All subtasks complete; tests green; lint clean.
- Cross-mission dependency on `bundle-format-resolver`'s
  `ArtifactKindHandler` seam documented in PR.
- Successfully resolves a fixture bundle end-to-end into populated
  `PeerRegistry` and `SkillRouter` registrations (mocked downstream
  registries acceptable here; full wiring lives in WP04 / WP06).
- PR merged.
