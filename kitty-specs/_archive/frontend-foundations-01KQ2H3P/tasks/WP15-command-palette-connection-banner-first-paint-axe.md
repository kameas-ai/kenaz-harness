---
work_package_id: "WP15"
title: "Command palette + connection-lost banner + first-paint state machine + accessibility baseline"
dependencies:
  - "WP06"
  - "WP07"
  - "WP12"
  - "WP13"
planning_base_branch: "main"
merge_target_branch: "main"
branch_strategy: "Planning artifacts were generated on main; completed changes must merge back into main."
subtasks:
  - "T001"
  - "T002"
  - "T003"
  - "T004"
  - "T005"
phase: "Phase 15 - UX baseline"
assignee: ""
agent: ""
shell_pid: ""
history:
  - timestamp: "2026-04-25T00:00:00Z"
    agent: "system"
    action: "Prompt generated via /spec-kitty.tasks"
---

# Work Package Prompt: WP15 – Command palette + connection-lost banner + first-paint state machine + axe-core baseline

## Goal

Close out the chassis with the four UX primitives that gate every downstream UI mission:

1. The command palette (`Cmd/Ctrl+K`) — invocable from anywhere; app-level actions (open settings, switch theme, start new session) work even before a session loads.
2. The connection-lost banner — a single dismissable, non-toasting banner driven by `useConnectionState()` from WP12.
3. The first-paint state machine — quiet "starting…" state until first `ShellStatus()` succeeds; transitions `connecting → ready ↔ degraded → lost`.
4. Accessibility baseline — axe-core integrated into Vitest with a CI gate that fails on any serious or critical violation across every primitive and primary surface.
5. The `<DenialNotice>` primitive (renders `policy-engine` denials) shipped as a stub so downstream missions wire their actual denials through it.

## Spec references

- FR-010 (command palette `Cmd/Ctrl+K`)
- FR-011 (accessibility baseline WCAG 2.2 AA)
- FR-012 (policy-denied action surface)
- FR-013 (connection-lost handling)
- FR-017 (first-paint state machine)
- FR-018 (error-boundary hygiene)
- NFR-005 (accessibility compliance — zero serious/critical axe-core violations)
- SC-004 (axe-core scans across every primitive and primary surface return zero serious/critical violations)
- SC-009 (policy-denied action surfaces a single typed denial 100 % of the time)

## Plan references

- §2.1 (`CommandPalette.vue`, `DenialNotice.vue` under `components/ui/`; `useCommandPalette.ts`, `useConnectionState.ts` under `lib/`)
- §3.1 ShellStatus connection field (`connecting | ready | degraded | lost`)
- §4.1 ("First-paint state machine: the Shell renders a quiet 'starting…' state until the first `ShellStatus` poll succeeds … connection-lost banner is a single dismissable `<DenialNotice>`-style component, not a toast wall")
- §7 v1.0 item 9 (command palette), item 11 (axe-core integrated into Vitest; zero serious/critical at PR gate), item 13 (connection-lost banner, first-paint state machine, error-boundary hygiene)

## Subtasks

- T001 — Implement `frontend/src/components/ui/CommandPalette.vue` using Radix Vue's `Dialog` + `Listbox` primitives. Bind `Cmd/Ctrl+K` globally via `useCommandPalette` (WP12 stubbed; this WP completes). Register app-level actions: open settings, switch theme, start new session. Allow per-surface action providers to register/unregister.
- T002 — Implement `frontend/src/components/ui/ConnectionLostBanner.vue` as a single dismissable banner driven by `useConnectionState()`. Show on `lost`; hide on `ready`. Include a "Retry" affordance that re-pokes the bridge.
- T003 — Implement the first-paint state machine in `useConnectionState()`: states `connecting → ready ↔ degraded → lost`. The Shell (WP06) renders a quiet "starting…" surface while `connecting`. Transition on first `ShellStatus()` success/failure. After N consecutive failures (configurable, default 3) move to `lost`.
- T004 — Implement `frontend/src/components/ui/DenialNotice.vue` rendering `Denial { policyID, clauseID, violatingInput, remediation }`. Stub the data flow via `usePolicyDecisions().onDenied` (WP12). Add a smoke test feeding a fake denial and asserting all four fields render.
- T005 — Integrate axe-core into Vitest: add `@axe-core/playwright` or `vitest-axe`. Write tests scanning every shell component, every `components/ui/` primitive, and every placeholder primary surface. Fail CI on any serious or critical violation. Add a top-level `ErrorBoundary.vue` (Vue's `errorCaptured` pattern) routing crashes through `eventLog.ts` (WP12) with a quiet recovery affordance (FR-018).

## Acceptance criteria

- `Cmd/Ctrl+K` opens the command palette anywhere in the app, including before a session is loaded.
- Disconnecting the RPC bridge (kill the Wails backend in dev) shows the connection-lost banner; restoring it auto-recovers without a wall of error toasts.
- Cold start renders the "starting…" state until first `ShellStatus()` succeeds (≤ 1 s p95 per NFR-001 with the inline critical token block from WP02 in place).
- `<DenialNotice>` renders the four fields uniformly given a `Denial` object.
- Axe-core CI gate: zero serious or critical violations across every primitive and the populated shell.
- An error-boundary fixture intentionally throws inside a child component; the boundary captures, logs through `eventLog.ts`, and shows a quiet recovery affordance.

## Files to create/modify

- Create: `frontend/src/components/ui/CommandPalette.vue`, `ConnectionLostBanner.vue`, `DenialNotice.vue`, `ErrorBoundary.vue`.
- Modify: `frontend/src/lib/useCommandPalette.ts`, `useConnectionState.ts` (real implementations replacing WP12 stubs).
- Modify: `frontend/src/shell/Shell.vue` to render `<ConnectionLostBanner>` and the "starting…" state.
- Modify: `frontend/vitest.config.ts` to integrate axe-core test setup.
- Create: Vitest tests under `frontend/src/components/ui/__tests__/` for each primitive plus an axe-core scan suite.
- Modify: CI workflow to include the axe-core PR gate.

## Definition of done

- All acceptance criteria pass.
- Mission-level gates close: SC-004 (axe-core zero serious/critical), SC-009 (denial-notice 100 % rendering), NFR-001 first-paint, FR-010 / FR-011 / FR-012 / FR-013 / FR-017 / FR-018 fulfilled.
- Cross-mission note: `<DenialNotice>` is the single render path for `policy-engine-01KQ1A3N`'s `Explainer` output; downstream policy mission wires real denials through `usePolicyDecisions().onDenied`. The audit-log viewer mission consumes `EventStreamList` (WP08) backed by `event-log-01KQ1A3M`'s Reader via `useEventLogStream` (WP12). Trust/secrets surfaces consume `useTrust()` returning reference-only metadata from `secrets-keychain-01KQ1A3M`'s Resolver (FR-020 / C-004 enforced by WP14's no-credential-in-UI lint).
- This WP closes the foundation mission. Every subsequent UI mission layers on this chassis without re-litigating tokens, layout, RPC bridge, streaming, persistence, accessibility, or privacy CI.
