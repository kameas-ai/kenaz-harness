---
work_package_id: "WP03"
title: "Capability gate, credref bridge, and preflight coordinator"
dependencies:
  - "WP01"
  - "WP02"
  - "secrets-keychain:WP02"
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

# Work Package Prompt: WP03 – Capability gate, credref bridge, and preflight coordinator

## Goal

Implement `core/llm/capabilities` (per-(provider, model) capability data
+ pre-call gate), `core/llm/credref` (thin adapter to `core/secrets`
`Reference.Resolve`), and a `PreflightCoordinator` that resolves every
loaded profile's credential reference at startup before any model call.

## Spec references

- FR-003 — Indirect credential resolution only (drives credref shim).
- FR-013 — Unsupported-capability errors are first-class (drives gate).
- FR-019 — Pre-flight credential resolution.
- NFR-005 — Pre-flight resolution success rate (100 % resolvable, 100 %
  reported on failure).
- NFR-009 — Local-first guarantee (no network at preflight beyond the
  upstream secrets backend; capability gate is purely in-process data).
- C-002 — No inline plaintext credentials.
- Acceptance Scenario US1.3 — clear startup error for missing creds.
- Acceptance Scenario US2.2 — typed error before any provider cost when
  capability unsupported.
- Edge case "Bedrock aws_profile without region" (plan R7).

## Plan references

- §2 Architectural Placement — `core/llm/capabilities/`,
  `core/llm/credref/`.
- §4 Internal Layering — CapabilityGate, CredentialResolver, and
  PreflightCoordinator stages.
- §5.2 Credential Reference shape — credref does NOT redefine the
  upstream `core/secrets.Reference`; it bridges.
- §6.1 secrets-keychain integration — `Resolve`, zeroize after use,
  rely on upstream TTL cache, no connector-side credential cache.
- R4 — capability descriptors live as YAML data, not code.
- R6 — pre-flight UX (cache TTL, first-prompt hint).

## Subtasks

- T001 — Define `core/llm/capabilities/data/` directory and load
  per-provider YAML descriptors (anthropic, openai, openrouter,
  bedrock, ollama) at startup; unknown-model fallback = "streaming-only
  safe baseline" with a warning event.
- T002 — Implement `CapabilityGate.Check(req, profile, descriptor)`:
  returns `ErrCapabilityUnsupported` (with provider / model / capability
  list) before any wire call; emits `llm/capability_rejected` (event
  emit lands in WP04 — gate exposes a hook).
- T003 — Implement `core/llm/credref` bridging `CredentialReference` →
  `core/secrets.Reference`, with `Resolve(ctx) ([]byte, error)` and a
  `Zeroize` helper invoked by adapters after wire-request build.
- T004 — Implement `PreflightCoordinator.PreflightAll(ctx) []PreflightResult`:
  iterate every loaded profile, call `credref.Resolve`, build a
  `PreflightResult{ProfileID, Kind, Resolved bool, Err error}`. Never
  log resolved bytes. Emit `llm/preflight_resolved` /
  `llm/preflight_failed` (hook).
- T005 — Bedrock-specific preflight: validate `region` non-empty AND
  `aws_profile` resolvable before declaring success (R7).
- T006 — Tests: capability-gate matrix (every capability × every
  provider with descriptor), credref happy path + missing env var +
  missing keychain entry, preflight error reporting, bedrock missing
  region, zeroize-after-use assertion (re-read of buffer is zeroed).

## Acceptance criteria

- `go test ./core/llm/capabilities/... ./core/llm/credref/...` passes
  with ≥ 80 % coverage.
- Calling `Registry.Stream` with a `GenerationRequest` opting into a
  capability the descriptor marks unsupported returns
  `ErrCapabilityUnsupported` and does NOT invoke any adapter.
- `PreflightAll` produces a non-empty failure list when an env-var
  cred is unset, naming the failing profile id and reference kind.
- A bedrock profile with empty `region` is reported via `PreflightAll`
  with a typed "region not configured" error.
- No file in this WP imports a provider SDK; capabilities data files
  are pure YAML.
- Resolved credential bytes are zeroized within 1 ms of adapter wire
  build (asserted by a test using `runtime.SetFinalizer` or buffer
  read-back).

## Files to create / modify

- `core/llm/capabilities/gate.go`
- `core/llm/capabilities/loader.go`
- `core/llm/capabilities/data/anthropic.yaml`
- `core/llm/capabilities/data/openai.yaml`
- `core/llm/capabilities/data/openrouter.yaml`
- `core/llm/capabilities/data/bedrock.yaml`
- `core/llm/capabilities/data/ollama.yaml`
- `core/llm/capabilities/gate_test.go`
- `core/llm/credref/credref.go`
- `core/llm/credref/credref_test.go`
- `core/llm/preflight.go`
- `core/llm/preflight_test.go`

## Definition of done

- All subtasks complete; tests green; lint clean.
- Cross-mission dependency on `secrets-keychain:WP02` (Reference +
  Resolve API) verified by adapter import only of `core/secrets`
  exported types.
- PR merged.
