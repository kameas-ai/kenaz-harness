# Design primitives

This page enumerates the owned UI primitives downstream UI missions
consume from this chassis (plan §7 v1.0 item 5 / WP06–WP09 / WP15).
None of these primitives carry per-component color, size, spacing or radius
values — every paint and every step flows through a Tailwind utility
resolving to a `@kameas-ai/kenaz-design-system` variable (imported by
`frontend/src/styles/tokens.css`; privacy CI invariant #4, extended to the
DS v0.13.0 space / type / radius groups).

## Token utilities (DS v0.13.0, `frontend/tailwind.config.ts`)

| Group | Utilities | DS variable |
|-------|-----------|-------------|
| Type size | `text-tag` 9.5 · `text-eyebrow` 10.5 · `text-caption` 11 · `text-small` / `text-xs` 12 · `text-ui` 13 · `text-body` / `text-sm` 14 · `text-h3` 15 · `text-title` 18 · `text-canvas-title` 23 · `text-hero` 26 | `--type-*-size` |
| Tracking | `tracking-eyebrow` · `tracking-eyebrow-xs` · `tracking-meta` · `tracking-h3` · `tracking-title` · `tracking-hero` | `--type-*-tracking` |
| Spacing | Tailwind steps `0.5 1 1.5 2 2.5 3 3.5 4 5 6 8 12` (= 2…48 px) | `--space-{2…48}` |
| Radius | `rounded-xs` · `rounded` / `rounded-sm` · `rounded-md` · `rounded-lg` / `rounded-xl` · `rounded-pill` / `rounded-full` | `--radius-*` |
| Family | `font-ui` · `font-mono` · `font-heading` | `--font-*` |
| Colour | `surface-0…4`, `border{,-muted,-strong}`, `ink{,-muted,-subtle,-dim,-trace}`, `accent{,-muted,-dim,-glow,-hairline}`, `signal-{ok,warn,danger,info,violet,neutral,git}`, `signal-{ok,warn,danger}-text` (words), `signal-{ok,warn,danger,info,violet}-soft` (callout field), `modal-{overlay,shadow}`; all accept `/NN` opacity | colour tokens |
| Elevation | `shadow-sm|DEFAULT|md|lg|xl` | `--modal-shadow` |

Icons come from `frontend/src/shell/icons.ts`: DS glyphs (rendered from the
package's `ICON_REGISTRY` by `shell/dsIcon.ts`) where the DS has one,
Lucide otherwise. `DsKenazMark` renders the DS brand mark.

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
