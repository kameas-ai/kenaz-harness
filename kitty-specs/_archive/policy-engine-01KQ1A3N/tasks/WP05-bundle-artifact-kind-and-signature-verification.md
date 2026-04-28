---
work_package_id: "WP05"
title: "Bundle artifact-kind handler and signature verification"
dependencies:
  - "WP01"
  - "WP02"
  - "WP04"
planning_base_branch: "main"
merge_target_branch: "main"
branch_strategy: "Planning artifacts were generated on main; completed changes must merge back into main."
subtasks:
  - "T001"
  - "T002"
  - "T003"
  - "T004"
phase: "Phase 5 - Bundle integration + signature verification"
assignee: ""
agent: ""
shell_pid: ""
history:
  - timestamp: "2026-04-25T00:00:00Z"
    agent: "system"
    action: "Prompt generated via /spec-kitty.tasks"
---

# Work Package Prompt: WP05 – Bundle artifact-kind handler and signature verification

## Goal

Register `policy` as an artifact kind with the
`bundle-format-resolver-01KQ1A3J` mission's resolver, plumb signed
policy artifacts through the `a2a-signed-cards-trust-01KQ18P9`
verification surface, and route verified artifacts into the layer
merge + engine reload pipeline. This is the only seam in the policy
engine that touches bundle and trust subsystems — every other consumer
talks to `policy.Engine` only.

## Cross-mission dependencies

- **bundle-format-resolver-01KQ1A3J**: must expose an artifact-kind
  registration API the policy package calls. If that mission's API is
  not stable yet, this WP defines the consumer-side adapter and adds a
  TODO with the artifact-kind registration call gated behind a build
  tag or interface seam — surfacing the dependency loudly rather than
  silently bypassing it.
- **a2a-signed-cards-trust-01KQ18P9**: must expose a signature-
  verification surface (`Verify(envelope, trustAnchors)
  ([]Signer, error)`). Same fallback approach if the API is not yet
  stable.

## Spec references

- FR-002 (policy as a registered artifact kind in bundles).
- FR-003 (required signing for policy artifacts; default-deny on
  unsigned / wrong-signer).
- C-002 (policies distribute as bundles — no parallel channel).
- Edge case: "policy bundle signed under a trust anchor not yet trusted
  by the operator → rejected, not silently absent."

## Plan references

- Plan §2 — `core/policy/bundle/` is the only package that imports
  `core/bundle` for kind registration.
- Plan §4 step 1–2 (bundle resolver fetches → policy bundle handler
  activates → signature verified by a2a-signed-cards-trust → unsigned
  / wrong-signer rejected).
- Plan §6 integration table (bundle-format-resolver,
  a2a-signed-cards-trust rows).
- Plan §7 v1.0 — registered handler at v1.

## Subtasks

- T001: Create `core/policy/bundle/handler.go` implementing the bundle
  resolver's artifact-kind contract for `kind: policy`. The handler
  parses YAML, runs the WP02 schema validator, resolves clause kinds
  via the registry, and produces a `PolicyArtifact` ready for layering.
- T002: Plumb signature verification through
  `a2a-signed-cards-trust`'s public surface. Default posture: reject
  any artifact whose signature does not verify against an operator-
  trusted anchor. Emit `policy_loaded` PolicyEvent on success (event
  emission lands in WP12; until then, log via the WP01 placeholder).
- T003: On the harness startup boot path, register the handler with
  the bundle resolver at the correct phase per plan §6 bootstrap order
  (after operator config + event log + bundle resolver init, before
  consumer subsystems). Surface a typed startup error if registration
  fails.
- T004: Black-box integration test: a temp directory holds a signed
  policy bundle (using a test trust anchor); the resolver fetches it,
  the handler verifies the signature, the engine reloads, and a
  subsequent `Evaluate` reflects the loaded policy. Negative cases:
  unsigned bundle → reject with `ReasonMissingSignature`; wrong-signer
  → reject with `ReasonWrongSigner`; trust-anchor not trusted → reject
  with a typed "untrusted anchor" error.

## Acceptance criteria

- The bundle resolver dispatches `policy` artifacts to the handler
  during a representative resolve pass.
- A signed test policy artifact loads end-to-end; its clauses appear in
  the resulting `EffectivePolicy` with `source_layer` provenance.
- An unsigned artifact is rejected with `ReasonMissingSignature`; the
  engine's active snapshot is unchanged.
- A wrong-signer artifact is rejected with `ReasonWrongSigner`.
- The handler does not contain evaluation logic — only parse,
  signature-verify, and forward (DIRECTIVE_001 boundary check enforced
  by an import-graph test).
- Bootstrap order test: registration happens at the documented phase;
  if the bundle resolver tries to dispatch before registration, the
  resolver returns the documented "kind not registered" error.

## Files to create/modify

- Create `core/policy/bundle/handler.go`.
- Create `core/policy/bundle/handler_test.go` (black-box integration
  per charter / DIRECTIVE_036).
- Create test fixtures: signed policy bundle under
  `core/policy/bundle/testdata/`.
- Modify `core/bundle/...` only via the published artifact-kind
  registration API; if that API does not exist yet, file an interface
  seam under `core/policy/bundle/seam.go` and call it out as a
  cross-mission dependency in the PR body.
- Modify the harness bootstrap (in the consuming app entry, e.g.,
  `main.go` or whatever `core/runtime` package owns startup) to call
  the policy bundle handler registration in the documented phase.

## Definition of done

- Acceptance criteria pass.
- Charter quality gates clean.
- A black-box integration test (DIRECTIVE_036) drives the bundle
  resolver from outside the policy package and asserts only on
  observable outputs (loaded clauses, returned error reason codes).
- The PR clearly identifies any cross-mission API gaps in
  `bundle-format-resolver-01KQ1A3J` or `a2a-signed-cards-trust-01KQ18P9`
  and links the dependency, per DIRECTIVE_010.
- Conventional-commit message; commit attributed per DIRECTIVE_029.
