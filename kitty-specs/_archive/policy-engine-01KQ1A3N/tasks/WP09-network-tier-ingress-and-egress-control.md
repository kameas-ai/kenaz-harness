---
work_package_id: "WP09"
title: "network_tier control kind for ingress and egress enforcement"
dependencies:
  - "WP02"
  - "WP03"
  - "WP04"
  - "WP07"
  - "WP08"
planning_base_branch: "main"
merge_target_branch: "main"
branch_strategy: "Planning artifacts were generated on main; completed changes must merge back into main."
subtasks:
  - "T001"
  - "T002"
  - "T003"
  - "T004"
phase: "Phase 9 - network_tier ingress + egress enforcement"
assignee: ""
agent: ""
shell_pid: ""
history:
  - timestamp: "2026-04-25T00:00:00Z"
    agent: "system"
    action: "Prompt generated via /spec-kitty.tasks"
---

# Work Package Prompt: WP09 – network_tier control kind for ingress and egress enforcement

## Goal

Land `network_tier` as a control kind that enforces both **ingress**
(harness-exposed A2A and MCP listeners) and **egress** (harness-
initiated outbound calls to providers, MCP servers, A2A peers, bundle
sources) against the operator's permitted network tier:
`loopback < lan < wan`. Resolves spec open question OQ3 — both
directions are enforced (per plan §9 proposal).

## Cross-mission dependencies

- **core/a2a** (consumer): listener bind + dial paths consult
  `Evaluate` with `Action.Kind() == "a2a.listen.bind"` and
  `"a2a.peer.dial"`.
- **core/mcp** (consumer): server listener bind + outbound connect
  paths consult `Evaluate` with `Action.Kind() == "mcp.listen.bind"`
  and `"mcp.server.connect"`.
- **core/bundle** (consumer): outbound bundle fetch consults
  `Evaluate` with `Action.Kind() == "bundle.fetch"`.

## Spec references

- FR-004 (control catalog v1 — network exposure tiers).
- FR-013 (network-exposure tiering for A2A, MCP, outbound).
- FR-008 (denial taxonomy — `ReasonNetworkTierNotPermitted`).
- User Story 1 (no public WAN exposure).
- Edge case (overly broad `*` allowlists flagged as warning).
- Spec OQ3 → plan §9 proposal: enforce both ingress and egress.

## Plan references

- Plan §2 — `clauses/network_tier/`.
- Plan §4 strict-narrowing — `TierAtLeast` (loopback ≥ lan ≥ wan in
  strictness order; smaller tier is stricter).
- Plan §6 — consumer rows for A2A, MCP, bundle resolver.
- Plan §9 OQ3 proposal (both directions).

## Subtasks

- T001: Define schema: `params: { ingress: { listeners:
  [a2a|mcp], max_tier: loopback|lan|wan }, egress: { destinations:
  [provider|mcp_server|a2a_peer|bundle_source], max_tier: loopback|
  lan|wan } }`. Each direction may declare independent tiers.
- T002: Lowering: emit Rego matching ingress action kinds
  (`a2a.listen.bind`, `mcp.listen.bind`) and egress kinds
  (`a2a.peer.dial`, `mcp.server.connect`, `bundle.fetch`,
  `llm.provider.activate`'s outbound shape) against the tier in
  `inputs.tier`. Deny if `inputs.tier` exceeds the declared
  `max_tier`. The tier ordering helper lives under
  `core/policy/layer/semantics/tiers.go` (already created in WP04).
- T003: Narrowing: child's `max_tier` per direction MUST be ≤ parent's
  (stricter). A child that loosens (e.g., parent `lan`, child `wan`)
  is rejected with `team_would_broaden_org` /
  `personal_would_broaden_team`.
- T004: Tests:
  - Spec edge case: bind A2A listener with tier `wan` while policy
    permits up to `lan` → `Deny` with
    `ReasonNetworkTierNotPermitted`.
  - Egress: dial a peer over WAN while egress tier is `lan` → `Deny`.
  - Narrowing matrix on tiers (loopback / lan / wan with parent /
    child / silence permutations).
  - Black-box: a fake A2A listener subsystem queries Evaluate before
    binding and behaves correctly under ingress and egress denials.
  - Wildcard warning: a clause with `destinations: ["*"]` is allowed
    but emits a `wildcard_warning` finding (per WP04 finding kinds).

## Acceptance criteria

- Ingress and egress enforcement both work; the control kind is
  registered.
- Narrowing matrix is exhaustive across the three tiers and both
  directions.
- Spec OQ3 resolution is documented in an ADR per DIRECTIVE_003 (both
  directions enforced).
- Wildcard destinations produce a warning finding.
- Lowering output is byte-stable.

## Files to create/modify

- Create `core/policy/clauses/network_tier/{kind.go, schema.go,
  lower.go, merge.go, adapter.go}` + `*_test.go`.
- Create `docs/adr/<n>-network-tier-both-directions.md` resolving OQ3.
- Modify `core/policy/registry_test.go`.

## Definition of done

- Acceptance criteria pass.
- Charter quality gates clean.
- ADR for OQ3 resolution landed.
- Cross-mission dependencies documented in PR body.
- Conventional-commit message; commit attributed per DIRECTIVE_029.
