---
work_package_id: "WP02"
title: "Registry and Provider Profile bundle-artifact handler"
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
phase: "Phase 1 - Core skeleton"
assignee: ""
agent: ""
shell_pid: ""
history:
  - timestamp: "2026-04-25T00:00:00Z"
    agent: "system"
    action: "Prompt generated via /spec-kitty.tasks"
---

# Work Package Prompt: WP02 – Registry and Provider Profile bundle-artifact handler

## Goal

Implement the in-memory `core/llm/registry` package (adapter registration,
profile loading, `Profile(id)` lookup, `Stream` façade dispatch) and a
`kind: llm_provider` artifact-kind handler that the bundle-format-resolver
uses to materialize `ProviderProfile` declarations from bundle YAML.

## Spec references

- FR-002 — Named provider profiles in bundle config.
- FR-018 — Provider adapter extensibility (Registry is the seam).
- C-001 — Architectural-integrity boundary (Registry is the public façade).
- C-004 — Bundle-format compatibility (no new top-level config surface).
- C-005 — OSS / enterprise split (RegisterAdapter is the only entrypoint).
- SC-002 — Switch active provider with at-most one bundle-config edit.
- SC-006 — Add provider end-to-end with no `core/` edits outside its
  adapter package.

## Plan references

- §2 Architectural Placement — `core/llm/registry/` directory layout.
- §3 Public API — `Registry` interface signatures.
- §5.1 Bundle artifact: Provider Profile — YAML schema and validation.
- §6.3 bundle-format-resolver integration — `ArtifactKindHandler`
  contract (`Parse`, `Validate`, `Activate`).

## Subtasks

- T001 — Implement `core/llm/registry.Registry` (in-memory map keyed by
  profile id; `RegisterAdapter`, `LoadProfiles`, `Profile(id)`,
  `Stream`).
- T002 — Implement `core/llm/profile_schema.go`: parse + validate YAML
  per §5.1 (kind ∈ registered set, exactly one auth field, bedrock
  region required, unique id).
- T003 — Implement `core/llm/bundleartifact` package providing the
  `ArtifactKindHandler` for `kind: llm_provider`. `Parse` decodes YAML;
  `Validate` runs schema rules + checks adapter is registered for the
  declared kind; `Activate` calls `Registry.LoadProfiles`.
- T004 — Stub a `PolicyGuard` interface returning `Allow` (per plan §6.4
  no-op default until policy-engine lands); wire it as a no-op
  pre-check in `Registry.Stream`.
- T005 — Reject inline-plaintext credentials at parse time (defense in
  depth alongside the upstream secrets validator).
- T006 — Table-driven tests covering: profile-id collision detection,
  unknown-kind rejection, missing-region for bedrock, plaintext-cred
  rejection, RegisterAdapter / LoadProfiles happy path, and
  `Stream` dispatch to a fake adapter.

## Acceptance criteria

- `go test ./core/llm/registry/... ./core/llm/bundleartifact/...`
  passes with ≥ 80 % coverage.
- A bundle declaring `kind: llm_provider` artifact resolves to a
  registered `ProviderProfile` retrievable via `Registry.Profile(id)`.
- A profile with inline plaintext credentials fails `Validate` with a
  typed error citing C-002.
- A bedrock profile missing `region` fails validation before any AWS
  call (plan §5.1 + R7).
- Two profiles with the same `id` produce a deterministic conflict
  error surfaced via `Activate`.
- No provider SDK imports in `core/llm/registry/` or
  `core/llm/bundleartifact/` (verified via `go list -deps`).

## Files to create / modify

- `core/llm/registry/registry.go`
- `core/llm/registry/registry_test.go`
- `core/llm/profile_schema.go`
- `core/llm/profile_schema_test.go`
- `core/llm/bundleartifact/handler.go`
- `core/llm/bundleartifact/handler_test.go`
- `core/llm/policyguard.go` (no-op `PolicyGuard` interface + default)

## Definition of done

- All subtasks complete; tests green; lint clean.
- Bundle-format-resolver mission's artifact-kind contract satisfied
  (cross-mission dependency closed; document the version of the
  upstream contract used in commit message).
- PR merged via squash to `feat/llm-connector-01KQ1770`.
