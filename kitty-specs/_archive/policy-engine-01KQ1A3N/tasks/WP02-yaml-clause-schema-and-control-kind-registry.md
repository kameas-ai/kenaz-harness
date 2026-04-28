---
work_package_id: "WP02"
title: "YAML clause schema, Clause type, and control-kind registry"
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
phase: "Phase 2 - YAML clause schema + Clause type + control-kind registry"
assignee: ""
agent: ""
shell_pid: ""
history:
  - timestamp: "2026-04-25T00:00:00Z"
    agent: "system"
    action: "Prompt generated via /spec-kitty.tasks"
---

# Work Package Prompt: WP02 – YAML clause schema, Clause type, and control-kind registry

## Goal

Define the canonical YAML clause shape, the `Clause` and `PolicyArtifact`
Go types, and the `ControlKind` registration contract every per-kind
package will implement starting in WP03. Land a registry that the engine
consults at load time so unknown control kinds fail load (FR-017) and so
adding a new control kind never modifies an existing one (FR-005,
SC-006).

## Spec references

- FR-002 (policies declared as a registered artifact kind in bundles —
  this WP nails down the in-memory shape; bundle wiring is WP05).
- FR-005 (control-kind extensibility — new kinds in their own packages,
  no surgery on existing code).
- FR-017 (default-deny posture for unknown control kinds — load-time
  rejection, never silent no-op).
- C-001 (architectural integrity — clause packages own their schema).
- C-007 (OSS / enterprise control kinds share the same contract).

## Plan references

- Plan §2 directory layout — `core/policy/lower/schema/`, `clauses/<kind>/`.
- Plan §3 `ControlKind` interface (`Kind`, `ParamSchema`,
  `FailurePostureDefault`, `LowerToRego`, `NarrowingMerge`).
- Plan §5 data model — `PolicyArtifact`, `Clause`, `ControlKind` per
  data-model.md.
- Plan §4 step 3 — per-clause schema validation; unknown kind → reject.

## Subtasks

- T001: Define `core/policy/types.go` (or extend WP01's `policy.go`) with
  exported `PolicyArtifact`, `Clause`, `Layer` (enum: `org`, `team`,
  `personal`), `FailurePosture` (enum: `fail_closed`, `fail_open` per
  data-model.md), and `ControlKind` interface matching plan §3. Field
  names track data-model.md verbatim: `policy_id`, `name`, `version`,
  `layer`, `clauses`, `not_before`, `not_after`, `content_hash`.
- T002: Create `core/policy/lower/schema/` with the canonical YAML
  document schema for a `PolicyArtifact` (top-level metadata + a
  `clauses:` list where each entry has `clause_id`, `kind`, `params`,
  optional `failure_posture`). Encode with a JSON Schema document plus a
  Go struct and round-trip unmarshal/marshal tests.
- T003: Create `core/policy/registry.go` providing `Register(kind
  ControlKind)` and `Lookup(kind string) (ControlKind, bool)`. The
  registry is package-level but initialization-order-safe: per-kind
  packages call `policy.Register(...)` from `init()`. Duplicate
  registrations panic (developer error). Include a `RegisteredKinds()
  []string` accessor used by validators and by `harness policy explain`.
- T004: Wire `engine.Reload` (stubbed in WP01) to walk an incoming
  `PolicyArtifact` set, call `Lookup` for every `clause.kind`, and
  return a typed `unknown control kind` error if any clause's kind is
  not registered. This is the FR-017 enforcement seam — a unit test
  proves it rejects an unknown kind and an end-to-end test proves
  successful load when a stub kind is registered.
- T005: Add a content-hash helper that produces a stable SHA-256 over
  the canonical clause set (sorted, JSON-serialized) — used downstream
  by WP12's cache keying and WP05's signature-binding flow.

## Acceptance criteria

- `PolicyArtifact` and `Clause` types match data-model.md verbatim
  (field names, enums, optionality). Round-trip YAML→struct→YAML
  preserves the canonical form.
- `ControlKind` interface compiles; an in-test fake kind registers,
  validates a clause, returns `FailurePostureDefault() == FailClosed`,
  and emits a placeholder Rego string from `LowerToRego`. Duplicate
  registration panics in test.
- Loading a policy with an unknown `kind` returns a typed error whose
  message identifies the offending clause id and kind. The engine MUST
  NOT activate the policy in this case (FR-017).
- The content-hash function is deterministic across two encodings of
  the same logical policy (test asserts identical hashes for
  re-ordered map keys, etc.).
- `RegisteredKinds()` returns the v1 catalog (eleven kinds) once
  WPs 06–11 have landed. In this WP, only the test fake kind is
  registered.

## Files to create/modify

- Create / extend `core/policy/types.go` with `PolicyArtifact`,
  `Clause`, `Layer`, `FailurePosture`, `ControlKind`.
- Create `core/policy/registry.go` with the registration / lookup API.
- Create `core/policy/registry_test.go` (registration, duplicate panic,
  unknown-kind rejection).
- Create `core/policy/lower/schema/schema.go` with the canonical YAML
  schema struct + JSON Schema document.
- Create `core/policy/lower/schema/schema_test.go` (round-trip,
  stable-hash, malformed-document rejection).
- Modify `core/policy/engine/engine.go` to consult the registry during
  `Reload` and surface the FR-017 unknown-kind error.

## Definition of done

- All acceptance criteria pass.
- `go test ./core/policy/... -race` clean.
- Quality gates clean per charter (`gofmt`, `goimports`, `go vet`,
  `golangci-lint run`).
- `data-model.md` and the implementation match field-for-field; any
  drift is called out as an ADR per DIRECTIVE_003.
- No control-kind-specific logic in `core/policy/lower/schema/` —
  per-kind logic lands in WPs 06–11.
- Conventional-commit message; commit attributed per DIRECTIVE_029.
