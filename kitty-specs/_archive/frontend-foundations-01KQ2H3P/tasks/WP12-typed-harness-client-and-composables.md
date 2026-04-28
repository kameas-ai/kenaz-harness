---
work_package_id: "WP12"
title: "Typed harnessClient.ts + harnessClientContext + useHarnessAPI composables"
dependencies:
  - "WP10"
  - "WP11"
planning_base_branch: "main"
merge_target_branch: "main"
branch_strategy: "Planning artifacts were generated on main; completed changes must merge back into main."
subtasks:
  - "T001"
  - "T002"
  - "T003"
  - "T004"
  - "T005"
phase: "Phase 12 - RPC frontend"
assignee: ""
agent: ""
shell_pid: ""
history:
  - timestamp: "2026-04-25T00:00:00Z"
    agent: "system"
    action: "Prompt generated via /spec-kitty.tasks"
---

# Work Package Prompt: WP12 – Typed harnessClient.ts + composables (KenazClient-style polling hook)

## Goal

Build the TypeScript side of the RPC bridge: a typed `harnessClient.ts` wrapping `window.go.kanazea.Bindings` (the only module allowed to import `wailsjs/`); a `harnessClientContext.ts` providing the client through Vue's `provide/inject` with a fake-swap entry point for tests; and a suite of `useHarnessAPI()` composables (sessions, chat stream, audit stream, theme, connection state, policy decisions, command palette) plus a Kenaz-style polling hook for cross-cutting status (`useShellStatus()` polls `HarnessAPI.ShellStatus()` every 5 s while the window is focused).

## Spec references

- FR-007 (typed RPC client)
- FR-008 (RPC client swappability for tests)
- FR-009 (composables over direct RPC)
- FR-013 (connection-lost handling)
- FR-014 (streaming-friendly text rendering)
- FR-017 (first-paint state machine)
- FR-018 (error-boundary hygiene)
- FR-019 (type-safe streaming consumers)
- NFR-007 (RPC type fidelity 100 %)
- C-001 (architectural integrity)

## Plan references

- §2.1 frontend tree under `src/lib/`: `harnessClient.ts`, `harnessClientContext.ts`, `useHarnessAPI.ts`, `routing.ts`, `useKeepAlive.ts`, `useTheme.ts`, `useConnectionState.ts`, `useStream.ts`, `useCommandPalette.ts`, `eventLog.ts`, `categories.ts`, `types.ts`
- §3.3 `harnessClient.ts` interface (illustrative)
- §3.4 composables (`useHarnessClient`, `useSessions`, `useChatStream`, `useEventLogStream`, `useTheme`, `useConnectionState`, `usePolicyDecisions`, `useCommandPalette`)
- §4.1 ("Views consume composables only … ESLint rule `no-restricted-imports` forbids `wailsjs/*` outside `frontend/src/lib/harnessClient.ts`")
- §7 v1.0 item 8 (`harnessClient.ts` typed wrapper + `harnessClientContext.ts` provide/inject + `useHarnessAPI()` composables)

## Subtasks

- T001 — Implement `frontend/src/lib/harnessClient.ts` exporting `interface HarnessClient`, `createHarnessClient()` (wraps `wailsjs/go/rpc/Bindings`), and `createFakeHarnessClient(seed?)` for tests. Re-shape Wails flat methods (`Sessions_List`) into nested view-scoped client objects (`client.sessions.list()`). 100 % typed; no `any`. Generate `frontend/src/lib/types.ts` mirroring `wailsjs/go/models` for the parts the client exposes (FR-019).
- T002 — Implement `frontend/src/lib/harnessClientContext.ts` providing the client via Vue's `provide/inject` with a symbol key. Add `installHarnessClient(app, client)` for `main.ts` and `provideFakeClient(seed)` for tests.
- T003 — Implement `frontend/src/lib/useHarnessAPI.ts` exporting `useHarnessClient()`, `useSessions()`, `useChatStream(sessionId)`, `useEventLogStream(filter)`, `useShellStatus()` (Kenaz-style 5 s polling while focused), `usePolicyDecisions()`. Plus standalone composables in their own files: `useTheme.ts`, `useConnectionState.ts`, `useStream.ts`, `useCommandPalette.ts`, `useKeepAlive.ts`, `eventLog.ts`.
- T004 — Add ESLint `no-restricted-imports` rule forbidding `wailsjs/*` outside `frontend/src/lib/harnessClient.ts`. Add a CI grep `scripts/ci/check-wailsjs-isolation.sh` as a backstop.
- T005 — Write Vitest tests using `createFakeHarnessClient()` to render `LeftRail.vue` and confirm: sessions list populates from a fake; renaming a session calls the fake's `rename`; stream subscription routes a fake-emitted event into `useChatStream`. Also test that `useShellStatus` stops polling when the window blurs and resumes on focus.

## Acceptance criteria

- A component renders correctly with `createFakeHarnessClient({ sessions: [...] })` swapped in via `provideFakeClient` — no Wails involved (FR-008, SC-006).
- Outside `harnessClient.ts`, no file under `frontend/src/` imports from `wailsjs/*` (SC-005); ESLint and CI grep both enforce.
- `useShellStatus()` polls every 5 s while focused, suspends on blur, resumes on focus — verified by Vitest with fake timers.
- `useStream` exposes a typed event payload per topic from `contracts/wails-events.md` (WP11).
- `vue-tsc --noEmit` clean; no `any` in production code (NFR-007).
- The status footer (WP09) and privacy panel (WP09) are wired through `useShellStatus()`.

## Files to create/modify

- Create: `frontend/src/lib/harnessClient.ts`, `harnessClientContext.ts`, `useHarnessAPI.ts`, `types.ts`, `useTheme.ts`, `useConnectionState.ts`, `useStream.ts`, `useCommandPalette.ts`, `useKeepAlive.ts`, `eventLog.ts`, `routing.ts`, `rail.ts`.
- Modify: `frontend/src/main.ts` to install the client.
- Modify: `frontend/.eslintrc` (or `eslint.config.ts`) with `no-restricted-imports` rule.
- Create: `scripts/ci/check-wailsjs-isolation.sh`.
- Create: Vitest tests under `frontend/src/lib/__tests__/`.

## Definition of done

- All acceptance criteria pass.
- WP06 shell components, WP09 status surfaces, and the WP15 command palette consume composables from this WP — no direct Wails calls anywhere else.
- Cross-mission note: `usePolicyDecisions().onDenied` is the entry point downstream missions hand to `<DenialNotice>` (WP15) for `policy-engine-01KQ1A3N` denials; `useEventLogStream` is the entry point for the audit-log viewer (downstream) consuming `event-log-01KQ1A3M`'s Reader.
