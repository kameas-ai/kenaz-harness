---
work_package_id: "WP08"
title: "a2a_peer_allowlist, signature_required, redaction_strictness control kinds"
dependencies:
  - "WP02"
  - "WP03"
  - "WP04"
  - "WP06"
planning_base_branch: "main"
merge_target_branch: "main"
branch_strategy: "Planning artifacts were generated on main; completed changes must merge back into main."
subtasks:
  - "T001"
  - "T002"
  - "T003"
  - "T004"
  - "T005"
phase: "Phase 8 - a2a_peer_allowlist + signature_required + redaction_strictness"
assignee: ""
agent: ""
shell_pid: ""
history:
  - timestamp: "2026-04-25T00:00:00Z"
    agent: "system"
    action: "Prompt generated via /spec-kitty.tasks"
---

# Work Package Prompt: WP08 – a2a_peer_allowlist, signature_required, redaction_strictness control kinds

## Goal

Land three control kinds covering A2A peer trust and platform-wide
posture toggles: `a2a_peer_allowlist` (which peers may be dialed /
accepted), `signature_required` (whether bundles, packs, agent cards
must be signed), and `redaction_strictness` (which redaction tier the
event log applies). Each kind exercises a different
`NarrowingMerge` semantic class from WP04.

## Cross-mission dependencies

- **core/a2a** (acp-orchestration consumer): peer dial and incoming-
  card acceptance call `Evaluate` with `Action.Kind() == "a2a.peer.dial"`
  and `"a2a.card.accept"`.
- **core/bundle** + **a2a-signed-cards-trust-01KQ18P9** (consumer):
  `signature_required` interacts with WP05 — when this kind is ON,
  unsigned artifacts are rejected even if the WP05 default would
  permit them under operator-config. Document the precedence rule.
- **event-log-01KQ1A3M** (consumer): the redaction pipeline reads
  `redaction_strictness` from the active `EffectivePolicy` to set its
  redaction tier.

## Spec references

- FR-004 (control catalog v1 — A2A peer allowlist, signature
  requirements, redaction strictness).
- FR-008 (denial taxonomy — `ReasonNotInAllowlist`,
  `ReasonMissingSignature`, `ReasonWrongSigner`).
- FR-005 (extensibility).
- NFR-006 (control-catalog parity).
- User Story 1 (signature required, redaction strict).

## Plan references

- Plan §2 — `clauses/a2a_peer_allowlist/`,
  `clauses/signature_required/`, `clauses/redaction_strictness/`.
- Plan §6 — consumer rows for A2A, bundle resolver, event log.
- Plan §4 strict-narrowing — `BoolStricterWins` for
  `signature_required`; `TierAtLeast` for `redaction_strictness`;
  `SetIntersect` for `a2a_peer_allowlist`.

## Subtasks

- T001: `a2a_peer_allowlist`: `params: { allow: [peer_id...] }`.
  Lowering matches `Action.Kind() == "a2a.peer.dial"` and
  `"a2a.card.accept"` against `inputs.peer_id`. Narrowing:
  `SetIntersect`. Default fail-closed.
- T002: `signature_required`: `params: { scope:
  [bundle|pack|agent_card], required: bool }`. Lowering returns deny
  on `Action.Kind() == "<scope>.activate"` when `inputs.signed ==
  false` and `params.required == true`. Narrowing:
  `BoolStricterWins` (child may turn ON when parent OFF; child MUST
  NOT turn OFF when parent ON). Document precedence over WP05's
  default-required posture: this kind can only further-tighten, never
  loosen, the boot-time signature requirement.
- T003: `redaction_strictness`: `params: { level: lenient|standard|
  strict }`. Lowering does not gate via Evaluate (it is consumed by
  the event-log redaction pipeline); instead, the kind exposes a
  `Tier()` accessor on the merged clause that the event-log subsystem
  reads from `EffectivePolicy`. Narrowing: `TierAtLeast` (strict ≥
  standard ≥ lenient).
- T004: Per-kind tests covering schema, narrowing matrix (with the
  appropriate semantic class), and Evaluate end-to-end for kinds that
  emit decisions. For `redaction_strictness`, test that the event-log
  reader reads the merged tier and that a child cannot loosen the
  parent.
- T005: Register kinds via `init()`; update catalog test; add
  consumer adapters under each package.

## Acceptance criteria

- A peer not in the allowlist is denied with `ReasonNotInAllowlist`
  on both `a2a.peer.dial` and `a2a.card.accept`.
- An unsigned bundle is denied with `ReasonMissingSignature` when
  `signature_required.required == true`. A child layer attempting to
  toggle `required: false` while parent is `true` is rejected by the
  validator with `team_would_broaden_org` /
  `personal_would_broaden_team`.
- Redaction strictness: parent `standard`, child `lenient` →
  validator rejects. Parent `standard`, child `strict` → effective
  `strict`. The event-log subsystem reads `Tier()` and applies the
  expected redaction level (assert via a black-box test that drives
  the log redaction pipeline through this WP's adapter, per
  DIRECTIVE_036).
- All three kinds appear in `RegisteredKinds()`.

## Files to create/modify

- Create `core/policy/clauses/a2a_peer_allowlist/{kind.go, schema.go,
  lower.go, merge.go, adapter.go}` + `*_test.go`.
- Create `core/policy/clauses/signature_required/{kind.go, schema.go,
  lower.go, merge.go, adapter.go}` + `*_test.go`.
- Create `core/policy/clauses/redaction_strictness/{kind.go,
  schema.go, lower.go, merge.go, accessor.go}` + `*_test.go`.
- Modify `core/policy/registry_test.go`.
- Document precedence between `signature_required` and WP05 in a
  short ADR or in `docs/adr/` per DIRECTIVE_003.

## Definition of done

- Acceptance criteria pass.
- Charter quality gates clean.
- Precedence ADR landed for `signature_required` vs. WP05 default.
- Cross-mission dependencies (`core/a2a`, `event-log-01KQ1A3M`,
  `a2a-signed-cards-trust-01KQ18P9`) documented in PR body.
- Conventional-commit message; commit attributed per DIRECTIVE_029.
