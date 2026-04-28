---
work_package_id: "WP10"
title: "HarnessAPI Go interface + 12 view-scoped accessors + Bindings wrapper"
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
phase: "Phase 10 - RPC backend"
assignee: ""
agent: ""
shell_pid: ""
history:
  - timestamp: "2026-04-25T00:00:00Z"
    agent: "system"
    action: "Prompt generated via /spec-kitty.tasks"
---

# Work Package Prompt: WP10 – HarnessAPI Go interface + view-scoped accessors + Bindings wrapper

## Goal

Implement the Go-side `HarnessAPI` interface mirroring Kenaz's `KenazAPI` shape: top-level cross-cutting methods (`ShellStatus`, `AppInfo`) plus 12 view-scoped sub-interfaces (LLMConnector, MCP, A2A, Workflow, Sessions, Trust, Context, Bundle, Policy, Audit, Settings — note `Settings` is also a view; `Workflow` wraps job, `Sessions` wraps session, `Bundle` wraps bundle from existing `core/rpc/`). Wrap with a `Bindings` struct Wails reflects as the JS-callable surface using flat method names like `Sessions_List`. View accessors are stable instances (same Go pointer for the lifetime of the API value).

## Spec references

- FR-007 (typed RPC client)
- FR-008 (RPC client swappability for tests — interface-driven design)
- FR-019 (type-safe streaming consumers)
- NFR-007 (RPC type fidelity 100 %)
- C-001 (architectural integrity — frontend talks to `core/` only through `core/rpc`)

## Plan references

- §2.2 (backend tree extensions: `api.go`, `bindings.go`, `views/{llm,mcp,a2a,workflow,sessions,trust,context,bundle,policy,audit,settings}/`)
- §3.1 `HarnessAPI` interface signature (`ShellStatus`, `AppInfo`, 12 sub-interface accessors)
- §3.2 `Bindings` struct (Wails-reflected, flat method names with `_` separator)
- §4.2 ("View accessors are stable instances: each `HarnessAPI.<View>()` call returns the same Go pointer for the lifetime of the API value … `var _ HarnessAPI = (*fakeHarnessAPI)(nil)` compile-time check lives in `core/rpc/api_test.go`")
- §7 v1.0 item 6 (HarnessAPI interface + view sub-interfaces; Bindings struct Wails-reflected)
- §8 R-6 (Wails reflection binding name collisions — forbid underscores inside view/operation names)

## Subtasks

- T001 — Create `core/rpc/views/` package tree with one sub-package per view (`llm`, `mcp`, `a2a`, `workflow`, `sessions`, `trust`, `context`, `bundle`, `policy`, `audit`, `settings`). Each package exports a `<Name>API` interface stub with method signatures aligned to the downstream missions (Sessions: List/Get/Create/Rename/Delete/Reorder/StartStream/StopStream — see plan §3.1 example).
- T002 — Refactor existing `core/rpc/{session,job,bundle,config}.go` to implement the new `views/sessions`, `views/workflow`, `views/bundle`, `views/settings` interfaces respectively. Preserve external behavior; add type aliases / shims to avoid breaking any caller during the transition.
- T003 — Create `core/rpc/api.go` defining `type HarnessAPI interface` with `ShellStatus(ctx) (ShellStatus, error)`, `AppInfo(ctx) (AppInfo, error)`, and 12 view accessors. Define `ShellStatus` and `AppInfo` structs per §3.1.
- T004 — Create `core/rpc/bindings.go` defining `type Bindings struct { api HarnessAPI; settings settings.SettingsStore; broker *streamBroker }` with one Wails-reflected method per `(view, operation)` pair using flat name `<View>_<Operation>` (e.g., `Sessions_List`, `MCP_StartStream`). Wire via `main.go` Wails options. Add a lint rule (Go vet check or custom) that fails if any view or operation name contains `_` (R-6 mitigation).
- T005 — Add `core/rpc/api_test.go` with a `fakeHarnessAPI` compile-time check `var _ HarnessAPI = (*fakeHarnessAPI)(nil)` and a test asserting view accessors return the same pointer across calls (stable-instance invariant).

## Acceptance criteria

- `core/rpc/api.go` exposes `HarnessAPI` with `ShellStatus`, `AppInfo`, and exactly 12 view accessors.
- `core/rpc/bindings.go` exposes Wails-reflectable methods covering every view × operation in §3.1 + §3.2.
- `var _ HarnessAPI = (*fakeHarnessAPI)(nil)` compiles.
- View accessor stability test passes (same pointer across N calls).
- Existing functionality from `core/rpc/{session,job,bundle,config}.go` continues to work via the new interface boundaries.
- `go vet` and lint rule for binding-name underscore collisions both pass.
- `wailsjs/` regenerates with the new `Bindings` surface.

## Files to create/modify

- Create: `core/rpc/api.go`, `core/rpc/bindings.go`, `core/rpc/api_test.go`.
- Create: `core/rpc/views/llm/api.go`, `core/rpc/views/mcp/api.go`, `core/rpc/views/a2a/api.go`, `core/rpc/views/workflow/api.go`, `core/rpc/views/sessions/api.go`, `core/rpc/views/trust/api.go`, `core/rpc/views/context/api.go`, `core/rpc/views/bundle/api.go`, `core/rpc/views/policy/api.go`, `core/rpc/views/audit/api.go`, `core/rpc/views/settings/api.go`.
- Modify: `core/rpc/session.go`, `core/rpc/job.go`, `core/rpc/bundle.go`, `core/rpc/config.go` to plug into new interfaces.
- Modify: `main.go` to construct `Bindings` and register with Wails.
- Create: Go-side lint rule or `scripts/ci/check-binding-names.sh`.

## Definition of done

- All acceptance criteria pass.
- `go test ./core/rpc/...` passes.
- The Wails build regenerates `wailsjs/` reflecting the new bindings; WP12's `harnessClient.ts` consumes them.
- Cross-mission note: `views/policy/api.go` carries the Explainer hook signature (`Explain(input) Denial`) reserved for `policy-engine-01KQ1A3N`; `views/audit/api.go` carries `ListEntries / VerifyEntry` reserved for `event-log-01KQ1A3M`'s Reader; `views/trust/api.go` carries `GetSecretReference` returning reference-only metadata reserved for `secrets-keychain-01KQ1A3M`'s Resolver.
