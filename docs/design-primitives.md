# Design primitives

This page enumerates the owned UI primitives downstream UI missions
consume from this chassis (plan §7 v1.0 item 5 / WP06–WP09 / WP15).
None of these primitives carry per-component color values — every paint
flows through a Tailwind utility resolving to a CSS variable in
`frontend/src/styles/tokens.css` (privacy CI invariant #4).

## Layout shell (`frontend/src/shell/`)

| Component | Purpose | Spec |
|-----------|---------|------|
| `Shell.vue`        | 3-region grid wrapping the app | FR-001 |
| `Titlebar.vue`     | Window-drag region + brand mark + AI-output disclaimer + utility actions | FR-001, FR-001h |
| `Toolbar.vue`      | Surface-level action row (Customize, theme, palette) | FR-001 |
| `LeftRail.vue`     | New-session affordance + sessions list + primary-surface nav | FR-001, FR-002 |
| `RailEntry.vue`    | One rail row (icon + label + active state) | FR-001 |
| `CanvasHead.vue`   | Numbered-section header pattern (`NN / SECTION` + title + subtitle) | FR-001b |
| `LegendBar.vue`    | Category color legend + live-rate inline indicator + status footer | FR-001d, FR-001g |
| `StatusPill.vue`   | Active provider · Trust tier · Harness build (Kenaz parallel triple) | FR-001f |

## Owned UI primitives (`frontend/src/components/ui/`)

| Component | Props | Spec |
|-----------|-------|------|
| `Button.vue`            | `variant`, `size`, `type`, `disabled` | FR-004 |
| `EventStreamRow.vue`    | `timestamp`, `category`, `subject`, `trailing?`, `size?` | FR-001c |
| `EventStreamList.vue`   | `entries: ReadonlyArray<EventStreamEntry>` | FR-001c, FR-014 |
| `PrivacyGuarantees.vue` | `status: APPLIED \| PARTIAL \| OFF`, `guarantees: ReadonlyArray<{ label, on }>` (no `value`/`secret`/etc fields) | FR-001e, FR-020 |
| `LiveRateIndicator.vue` | `rate: number`, `unit: string`, `precision?: number` | FR-001g |
| `DenialNotice.vue`      | `denial: Denial { policyId, clauseId, violatingInput, remediation }` | FR-012, SC-009 |
| `ConnectionLostBanner.vue` | (driven by `useConnectionState()`) | FR-013 |
| `CommandPalette.vue`    | (driven by `useCommandPalette()`; `Cmd/Ctrl+K`) | FR-010 |
| `ErrorBoundary.vue`     | catches via `errorCaptured`; reports through `eventLog.ts` | FR-018 |

## Composables (`frontend/src/lib/`)

| Composable | Purpose |
|------------|---------|
| `useHarnessClient()` | resolve the typed RPC client from provide/inject |
| `useShellStatus()`   | KenazClient-style 5 s polling for `ShellStatus` |
| `useSessions()`      | reactive list + CRUD for sessions |
| `useChatStream(id)`  | typed subscription for `sessions:event` |
| `useEventLogStream(filter)` | typed subscription for `audit:event` |
| `useTheme()`         | light / dark / system theme; persisted |
| `useConnectionState()` | first-paint state machine |
| `useCommandPalette()` | command-palette state + Cmd/Ctrl+K |
| `useKeepAlive(id)`   | per-session UI-state cache |
| `usePolicyDecisions()` | `onDenied(cb)` for `<DenialNotice>` |
