---
work_package_id: "WP12"
title: "Decision log emission to event-log and OverrideToken surface"
dependencies:
  - "WP01"
  - "WP04"
  - "WP05"
planning_base_branch: "main"
merge_target_branch: "main"
branch_strategy: "Planning artifacts were generated on main; completed changes must merge back into main."
subtasks:
  - "T001"
  - "T002"
  - "T003"
  - "T004"
  - "T005"
phase: "Phase 12 - Decision log emission + override-token surface"
assignee: ""
agent: ""
shell_pid: ""
history:
  - timestamp: "2026-04-25T00:00:00Z"
    agent: "system"
    action: "Prompt generated via /spec-kitty.tasks"
---

# Work Package Prompt: WP12 – Decision log emission to event-log and OverrideToken surface

## Goal

Wire every policy evaluation, validator finding, layer transition,
and override usage through the append-only `event-log-01KQ1A3M` as
typed `PolicyEvent`s. Build the constrained, audited `OverrideToken`
surface (FR-014) that allows one-shot operator overrides which can
**only further-narrow** — never loosen — org or team policy.

## Cross-mission dependencies

- **event-log-01KQ1A3M** (consumer): the engine appends events
  through the event-log API. Hash chain, redaction, retention are
  inherited from that mission. The OPA decision-log JSON shape MUST
  pass through the event-log redaction pipeline (plan §8 R7) — never
  written directly to disk.

## Spec references

- FR-009 (policy-decision events with policy id, clause id, inputs,
  outcome, reason).
- FR-014 (operator-override surface — constrained, audited).
- NFR-003 (audit completeness — 100 % of evaluations emit events).
- C-003 (append-only event log).
- C-005 (SOC 2 readiness).
- User Story 4 (every decision auditable and reproducible).
- User Story 7 (denials carry policy id, clause id, evaluated input,
  remediation hint).
- Edge case "per-session override vs. org policy": override cannot
  bypass org policy.

## Plan references

- Plan §2 — `core/policy/decisionlog/`, `core/policy/override/`.
- Plan §5 — `PolicyEvent` kinds (eight kinds; all listed in
  data-model.md).
- Plan §8 R3 (audit completeness — sampling forbidden), R5 (override
  bypass attempts), R7 (OPA decision-log redaction).
- Plan §6 — event-log integration row.

## Subtasks

- T001: Create `core/policy/decisionlog/emitter.go` exposing
  `EmitDecision(Decision)`, `EmitFinding(Finding)`,
  `EmitLayerTransition(prev, next EffectivePolicy)`,
  `EmitUnavailable(reasonCode, posture)`, `EmitOverrideUsed(token,
  decision)`. Each function constructs the typed `PolicyEvent`,
  routes its `inputs_summary` through the event-log redaction
  pipeline, and appends to the event-log. NO direct disk I/O.
- T002: Wire `engine.Evaluate` to emit `policy_evaluation_allowed` /
  `policy_evaluation_denied` for every call (NFR-003 = 100 %). Wire
  `engine.Reload` to emit `policy_loaded` and
  `policy_layer_transitioned` on success and `policy_validator_finding`
  for each finding from WP04. Wire fail-closed / fail-open paths
  (WP10 cost_ceiling and any other clause that signals unavailable
  inputs) to emit the matching `policy_unavailable_*` events.
- T003: Build `core/policy/override/token.go` with `IssueToken(req
  OverrideRequest) (OverrideToken, error)` and `Consume(tokenID)
  (OverrideToken, error)`. The issuer rejects any override request
  that would loosen org or team policy (it dry-runs the would-be
  Decision against the merged org+team layer; if that layer alone
  denies, the token is refused with a typed error). Tokens are
  short-lived (configurable, default 60 s) and single-use.
- T004: Tests:
  - Every `Evaluate` call emits exactly one event (allowed or denied);
    no sampling. Spec User Story 4 acceptance scenarios 1 + 2 pass.
  - User Story 7 acceptance scenario: a denial event includes
    policy id, clause id, evaluated input summary, and a remediation
    hint string from the matching control kind's adapter.
  - Layer transition emits `policy_layer_transitioned` with the prior
    + new `effective_id` and content hashes.
  - Override that would further-narrow a personal layer denial: token
    issues, consumes once, emits `policy_override_used` event;
    second consume attempt fails (`token_already_consumed`).
  - Override that would loosen org policy (the spec edge case): token
    issuance refused with typed `override_would_loosen_org` error.
- T005: Use a real on-disk event log under a temp dir for the
  audit/replay tests (charter testing standard — no mocking the event
  log when asserting audit behavior).

## Acceptance criteria

- 100 % of evaluations produce one `policy_evaluation_*` event in the
  event log; tested with a randomized hot loop of 10k evaluations
  asserting equal counts.
- Every `PolicyEvent` carries the fields required by FR-009: policy
  id, clause id, inputs summary, outcome, reason code,
  `evaluated_at`, `decision_id`. Inputs summary passes through the
  event-log redaction pipeline (assert by feeding a known-sensitive
  input pattern and verifying the redacted shape).
- Override tokens that would loosen org or team policy are rejected
  at issuance time. Tokens that further-narrow are issued, consumable
  exactly once, and emit `policy_override_used`.
- Hot-path emission cost is small enough that NFR-001 (sub-1 ms p99)
  still holds with emission enabled (re-run the WP01 benchmark).

## Files to create/modify

- Create `core/policy/decisionlog/{emitter.go, redact.go}` + tests
  using a real on-disk event log per charter.
- Create `core/policy/override/{token.go, issuer.go}` + tests.
- Modify `core/policy/engine/engine.go` to call the emitter on every
  Evaluate / Reload path.
- Modify per-kind adapters (WPs 06–11) to expose a `RemediationHint()`
  string used by `EmitDecision`.

## Definition of done

- Acceptance criteria pass.
- Charter quality gates clean.
- Tests use a real on-disk event log under a temp dir (charter
  Testing Standards).
- NFR-001 benchmark with emission enabled passes the 1 ms p99 budget.
- Cross-mission dependency on `event-log-01KQ1A3M` documented in PR
  body.
- Conventional-commit message; commit attributed per DIRECTIVE_029.
