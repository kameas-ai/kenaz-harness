# Feature Specification: Frontend Foundations — Design System, Layout, and RPC Bridge

**Feature Branch**: `feat/frontend-foundations-01KQ2H3P`
**Created**: 2026-04-25
**Status**: Draft
**Input**: User direction (2026-04-25): "Bring in the Kenaz design language. Kenaz is the new name for Sigil OS — a local VM giving a secure ephemeral environment for AI use. The kaneaz-harness is a separate native app shipped *alongside* Kenaz; the harness usually runs *inside* the Kenaz VM. Follow the same design language and theme so they feel like one thing. Everything is enterprise-first." This mission establishes the design system, layout shell, theming, accessibility baseline, and the bridge from the Vue/Vite frontend to the existing `core/rpc` Wails surface. Every subsequent UI mission layers on top of this foundation.

## Why this mission exists

The harness is a control-plane companion to the Kenaz VM. From the operator's point of view, opening the harness should be indistinguishable from opening any other Kenaz surface: same dark-first palette, same warm accent, same monospace event-stream language, same category color system, same numbered-section information architecture, same "Privacy guarantees" framing for AI / data-handling chrome. A divergent design language defeats the "one product" promise — and given the harness usually runs *inside* the Kenaz VM, visual drift would feel like the user accidentally opened a third-party app on a clean Kenaz install.

Without a single source of truth for tokens, layout primitives, and copy register, every subsequent UI feature mission relitigates "what does a button look like?" — that is the mistake this mission prevents.

## Dependencies and relationships

- **Depends on**: existing `core/rpc` Wails bindings (already scaffolded in `core/rpc/`); the canonical Kenaz design system (a Tailwind config / CSS custom-property file or equivalent shipped by the Kenaz project — to be imported, not re-invented).
- **Blocks** (or strongly enables): chat / session UI, tool & MCP management UI, bundle / context management UI, workflow authoring UI, provider config UI, audit-log viewer UI.
- **Coordinates with**: `policy-engine-01KQ1A3N` (UI surfaces denied actions per the explanation surface), `event-log-01KQ1A3M` (audit-log viewer is the consumer-facing surface for it; reuses Kenaz's event-stream visual primitive), `secrets-keychain-01KQ1A3M` (UI never displays resolved credential values; only references).
- **Does not cover**: any specific feature surface (chat, MCP management, etc.) — those are separate UI missions. This one is the chassis.

## Kenaz design language — observed primitives

Source observed: a single rendered section ("02 / VM SANDBOX — the ephemeral workbench") from the Kenaz Native App Design — Control Plane UI System artifact, dated 2026-04-25. The full design system is assumed to be more extensive than this section; the canonical token source (Tailwind config or CSS custom-property file) is required before implementation can pin exact values.

What is observable from the rendered section and committed as the v1 alignment target:

- **Theme**: dark-first. Near-black background with light gray foreground. Subtle borders, no drop shadows on cards. Light theme exists as a parity goal but the Kenaz visual register is dark.
- **Accent**: a single warm amber / muted gold, used sparingly — toggle "on" state, primary affordances. **Not** a saturated brand purple / blue.
- **Typography**: sans-serif for headings and body; monospace for event streams, file paths, version strings, hashes, numeric metadata, and any technical content. Type scale is restrained; no display-size headlines.
- **Section information architecture**: numbered section labels rendered as muted small caps with a separator (`02 / VM SANDBOX`), followed by a prominent section title and a one-paragraph subtitle. Every primary surface in the harness adopts this header pattern.
- **Event-stream primitive**: a dense, scrollable, monospace tabular list — `timestamp · CATEGORY · subject · trailing-metadata`. Each row is a single line; categories are color-coded. This is a first-class harness primitive (the audit-log viewer uses it; live MCP / LLM / scheduler streams use it).
- **Category color system**: each event domain has a stable color used as a small leading dot in legends and as the row's accent. Observed mapping from the source artifact:
  - **FILESYSTEM** — blue
  - **PROCESS** — green
  - **CLIPBOARD** — orange
  - **NETWORK** — pink / magenta
  - **KEYSTROKE** — yellow
  Harness extensions (LLM, MCP, A2A, POLICY, TRUST, BUNDLE, CONTEXT, SECRETS, STORAGE, SCHEDULER) MUST be assigned distinct colors that preserve the existing five and stay within the Kenaz palette. Final assignments are owned by Kenaz design and pulled in once the full token set is available.
- **"Privacy guarantees" panel**: a right-rail (or sibling panel) labeled with a small uppercase status (e.g., `APPLIED`), listing checkmarked guarantees about data handling. The harness adopts this exact pattern wherever the operator should see "what we promise about your data right now" (e.g., redaction status, local-first network egress status, credential-handling posture).
- **"Model · Tier · Build" footer**: a quiet bottom-left key-value triple — `Model | LFM2-24B Q4_K_M`, `Tier | Local · Full`, `Build | 0.3.14-a3f`. The harness adopts a parallel triple for itself: `Active provider`, `Trust tier`, `Harness build`.
- **Disclaimer chrome**: top bar carries a quiet "Content is user-generated and unverified" disclaimer adjacent to the brand mark, plus utility actions on the right (link, flag, customize). Reused across surfaces that render model output.
- **Iconography**: tiny, line-style, low-contrast icons. Consistent with a Lucide-class set; whatever Kenaz ships as canonical is what the harness uses.
- **Motion**: not directly observable in a static PDF, but the visual register implies restrained motion (≤ 200 ms transitions, ease-out, no spring physics).
- **Live-status affordances**: small inline rate indicators (e.g., `0.4 e/s` next to a toggle). The harness adopts this for any continuously-emitting surface (event stream rate, scheduler tick rate, RPC stream rate).
- **Toggles**: pill-shaped, amber-on / muted-off, with the rate indicator floated alongside.
- **"Customize" action**: top-right utility action that opens a per-surface configuration panel. The harness adopts the same affordance per surface where settings exist.

What is **not** observable from this single section and must be imported from the canonical Kenaz design source before implementation:

- Exact hex / OKLCH values for background, surface, foreground, muted, border, accent, success, warning, danger, and each category color.
- Exact font families (sans + mono) and font-size / line-height scale.
- Exact spacing scale, radius scale, and motion-duration tokens.
- Light-theme parity values.
- Modal, dialog, sheet, command-palette, and toast component visual specs.
- Form-control specs (input, select, textarea, checkbox, radio, slider).

## User Scenarios & Testing *(mandatory)*

### User Story 1 — Operator opens the harness and it is indistinguishable from a Kenaz surface (Priority: P1)

When the harness window opens — most commonly inside the Kenaz VM, occasionally on the host alongside Kenaz — the operator sees an interface whose visual language is byte-identical to other Kenaz surfaces: same dark-first palette, same warm amber accent, same numbered section headers (`NN / SECTION NAME`), same monospace event-stream primitive, same "Privacy guarantees" panel pattern, same `Model · Tier · Build` status footer. A user who alt-tabs between a Kenaz settings surface and the harness should see no visual seam.

**Why this priority**: This is the "feel like one thing" promise. The harness is sold and shipped as part of the Kenaz product family. Any visual divergence — a different accent, different typography, different section-header pattern — would read as "third-party app" the moment the user opens it on a clean Kenaz install. Visual coherence with Kenaz is the single most important UX outcome of this mission.

**Independent Test**: A Kenaz designer reviews the harness's first-open screen alongside a canonical Kenaz surface and identifies zero token-level divergences (color, type, spacing, accent, category color system).

**Acceptance Scenarios**:

1. **Given** the harness window opens for the first time inside the Kenaz VM, **When** the operator sees the layout, **Then** the palette, type, accent, section headers, and event-stream primitive match the canonical Kenaz design tokens exactly.
2. **Given** the operator is viewing both the harness and a Kenaz surface side by side, **When** they compare, **Then** there is no visual seam in palette, type, or component register.
3. **Given** the dark theme is the Kenaz default, **When** the harness opens, **Then** dark theme is the default with light theme available as a parity option.

---

### User Story 2 — Left-rail navigation lets the operator move between sessions and primary surfaces in one click (Priority: P1)

The left rail shows: (a) a sessions list — recent conversations / agent runs, scrollable, with active-session indicator; (b) a primary-surfaces section — tools / MCP servers, bundles, providers, audit log, settings; (c) a "new session" affordance at the top. Sessions are reorderable, renameable, deletable. Surfaces switch in one click without losing the active session's state.

**Why this priority**: This is the harness's primary navigation. If session-switching costs more than a click or session state evaporates on switch, the multi-concurrent-chat charter goal collapses.

**Independent Test**: An operator runs three concurrent sessions, switches between them via the left rail, and the previous state (scroll position, draft text in input) is preserved on return for each.

**Acceptance Scenarios**:

1. **Given** three active sessions, **When** the operator switches between them via the left rail, **Then** each session's UI state (scroll position, draft input) is preserved.
2. **Given** a session is renamed in the rail, **When** the operator confirms, **Then** the new name appears in the rail and is persisted to the harness data dir.
3. **Given** the operator clicks a primary surface (tools, bundles, providers, audit log), **When** the surface loads, **Then** the active session in the rail remains highlighted and resumable.

---

### User Story 3 — The frontend talks to `core/` only through the existing RPC surface (Priority: P1)

The frontend never imports a Wails-specific type into a component, never reaches around the RPC surface, and never carries business logic that should live in `core/`. A typed RPC client wraps `core/rpc` bindings; components consume it through composables. When the harness ever adds a hosted backend that implements the same RPC contract, no frontend code changes.

**Why this priority**: Charter DIRECTIVE_001 (architectural integrity) plus the deployment_constraints commitment that the same RPC contract a future hosted backend exposes. Letting Wails types leak into Vue components forks the frontend the day a cloud build ships.

**Independent Test**: A grep across `frontend/src/` finds zero direct imports from Wails-generated types in components or pages — only in the typed RPC client layer. A test fixture swaps the RPC client for a fake implementation; components render and behave correctly with no other changes.

**Acceptance Scenarios**:

1. **Given** the typed RPC client wraps every `core/rpc` binding, **When** a component needs harness data, **Then** it calls a composable backed by the client; it does not call Wails directly.
2. **Given** the RPC client is swapped for a fake in tests, **When** components render, **Then** they behave correctly without code changes.

---

### User Story 4 — The design system ships as code in the repo, not as an external dependency that drifts (Priority: P2)

Layout primitives (sidebar shell, panel, dialog, sheet, command palette) and form primitives (input, select, button, toggle, dropdown, tabs, table) live in the repository as components the team owns. Updates are deliberate; there is no "the framework changed how button looks in v3.4" surprise. Theming tokens (color, spacing, type scale, motion) are centralized and overrideable.

**Why this priority**: Frontier AI tools succeed visually because their teams own the design system. A copied-from-shadcn approach gives us the head start without locking us into a framework we'll later regret.

**Independent Test**: Bumping a third-party UI dependency does not change the visual appearance of any harness primitive. Changing a theme token in one place changes appearance everywhere consistently.

**Acceptance Scenarios**:

1. **Given** the primitive components live in `frontend/src/components/ui/`, **When** an upstream UI library publishes a release, **Then** harness primitives are unaffected unless we explicitly pull changes.
2. **Given** the theme tokens are centralized, **When** an operator changes the accent color or font-size scale, **Then** every primitive reflects it without per-component edits.

---

### User Story 5 — Accessibility is baseline, not optional (Priority: P2)

Every primitive ships with correct ARIA roles, keyboard navigation, focus management, color contrast at WCAG 2.2 AA, and screen-reader-correct labels. Dialogs trap focus; menus and selects are keyboard-driven; the command palette is invocable from anywhere with the standard `Cmd/Ctrl+K`. No primitive ships without an accessibility test.

**Why this priority**: Enterprise procurement increasingly requires an accessibility statement; SOC 2 doesn't mandate it but every comparable enterprise tool offers WCAG 2.2 AA. Retrofitting accessibility is materially more expensive than baking it in.

**Independent Test**: An automated axe-core scan across every primitive returns zero serious or critical violations; manual keyboard-only navigation exercises every primary surface end-to-end without a mouse.

**Acceptance Scenarios**:

1. **Given** any primary surface is open, **When** the operator navigates with keyboard only, **Then** every interactive element is reachable, focused, and operable.
2. **Given** any primitive renders, **When** axe-core scans it, **Then** zero serious or critical violations are reported.

---

### User Story 6 — Light, dark, and system themes are first-class and instant (Priority: P2)

The operator picks light, dark, or system-follow. Switching is instant, with no flash of the wrong theme on app start. Theme tokens drive every primitive. A handful of accent options are available; org policy may pin or restrict them.

**Why this priority**: Frontier AI tools all ship dark mode; OS-follow is table stakes. A flash-of-wrong-theme on cold start is the most-noticed polish failure.

**Independent Test**: Opening the harness in macOS dark mode shows dark theme immediately, with no light flash. Toggling the system theme while the harness runs flips the harness theme within the next paint cycle.

**Acceptance Scenarios**:

1. **Given** the OS is in dark mode, **When** the harness opens, **Then** the dark theme renders on first paint with no flash.
2. **Given** the operator toggles the OS theme while the harness runs, **When** the OS notifies, **Then** the harness theme updates within the next paint cycle.

---

### User Story 7 — Policy-denied actions surface clearly, never silently (Priority: P3)

When a policy denies an action initiated from the UI (per `policy-engine-01KQ1A3N`'s explanation surface), the operator sees a typed denial: which clause matched, which input violated, what they can do about it. No mystery "this didn't work." This UX foundation is shared by every feature that performs a policy-checked action.

**Why this priority**: The single biggest UX failure of enterprise tools is silent denial. Building the surface once at this layer means every feature gets it for free.

**Independent Test**: An action gated by an org policy that denies it produces a single explanatory toast / modal naming the policy artifact, the clause id, the violating input, and a remediation hint.

**Acceptance Scenarios**:

1. **Given** an action is denied by org policy, **When** the operator triggers it from the UI, **Then** a denial surface explains policy id, clause id, violating input, and a remediation hint.

---

### Edge Cases

- The harness opens before the RPC surface is ready (race during startup): show a quiet "starting…" state, not an error toast; transition automatically when ready.
- The OS theme switches mid-session while a long agent run is streaming: the theme updates without dropping the stream.
- The operator's machine is below the minimum window size for the layout (e.g., 800×600 — uncommon but possible): the layout collapses the left rail to icons and stays usable down to a documented minimum.
- The operator drags the window to a smaller display with different DPI: layout reflows; nothing pixel-snaps incorrectly.
- A surface contains an embedded code block with extreme width: it scrolls horizontally inside its container, does not break the layout.
- The sessions list grows past one screen: it virtualizes; no jank on scroll.
- The frontend cannot reach `core/rpc` (e.g., process crash): a quiet, non-alarming connection-lost banner with a retry; not a wall of error toasts.
- The operator opens the command palette (`Cmd/Ctrl+K`) before any session is loaded: still works for app-level actions (open settings, switch theme, start new session).

## Requirements *(mandatory)*

### Functional Requirements

| ID | Title | User Story | Priority | Status |
|----|-------|------------|----------|--------|
| FR-001 | Left-rail layout shell | As an operator, I want a persistent left-rail layout (sessions list + primary surfaces + new-session affordance) and a main content pane, in the Kenaz visual register. | High | Open |
| FR-001a | Kenaz token import | As a contributor, I want the harness's design tokens (color, type, spacing, radius, motion, category colors) imported from the canonical Kenaz design source — never re-defined locally — so visual drift between Kenaz and the harness is impossible. | High | Open |
| FR-001b | Numbered-section header pattern | As an operator, I want every primary surface in the harness to use Kenaz's numbered section header (`NN / SECTION NAME` muted small caps + prominent title + subtitle paragraph). | High | Open |
| FR-001c | Event-stream primitive | As a contributor, I want a single reusable event-stream primitive (dense monospace tabular list with `timestamp · CATEGORY · subject · trailing-metadata`) used by the audit-log viewer, the live LLM/MCP/A2A/scheduler streams, and any other monitoring surface. | High | Open |
| FR-001d | Category color system | As a contributor, I want a stable category color registry (Kenaz's existing FILESYSTEM/PROCESS/CLIPBOARD/NETWORK/KEYSTROKE plus harness-extension colors for LLM/MCP/A2A/POLICY/TRUST/BUNDLE/CONTEXT/SECRETS/STORAGE/SCHEDULER) imported from the canonical Kenaz palette; harness extensions are reviewed and pinned by Kenaz design. | High | Open |
| FR-001e | Privacy-guarantees panel pattern | As an operator, I want every surface that handles user data or model output to expose a "Privacy guarantees" panel with checkmarked guarantees (e.g., "Credentials never persisted", "Event-log redaction applied", "Local-first: zero outbound traffic") matching the Kenaz panel pattern. | High | Open |
| FR-001f | Model · Tier · Build status footer | As an operator, I want a quiet bottom-left footer triple parallel to Kenaz's `Model · Tier · Build` showing harness `Active provider · Trust tier · Harness build`. | Medium | Open |
| FR-001g | Live-rate inline indicators | As an operator, I want continuously-emitting surfaces to show small inline rate indicators (e.g., `0.4 e/s` next to the event-stream toggle) consistent with Kenaz's pattern. | Medium | Open |
| FR-001h | AI-output disclaimer chrome | As an operator, I want surfaces that render model output to carry the Kenaz "Content is user-generated and unverified" disclaimer adjacent to the brand mark or surface title. | Medium | Open |
| FR-002 | Multi-session UI state preservation | As an operator, I want each session to preserve its UI state (scroll position, draft input) when I switch between sessions. | High | Open |
| FR-003 | Primary surface navigation | As an operator, I want one-click navigation to tools / MCP servers, bundles, providers, audit log, and settings without losing the active session. | High | Open |
| FR-004 | Owned design-system primitives | As a contributor, I want layout, form, and feedback primitives living in our repository (not as a runtime dependency we cannot edit), so that visual identity is ours. | High | Open |
| FR-005 | Centralized theme tokens | As a contributor, I want color, spacing, type-scale, motion tokens centralized; primitives reference tokens, never raw values. | High | Open |
| FR-006 | Light / dark / system theme | As an operator, I want theme = light / dark / system-follow with no flash of wrong theme on startup. | High | Open |
| FR-007 | Typed RPC client | As a contributor, I want a single typed RPC client that wraps every `core/rpc` binding; components and composables consume it; no other module touches Wails-generated types. | High | Open |
| FR-008 | RPC client swappability for tests | As a contributor, I want the RPC client interface to accept a fake implementation for tests so that components are unit-testable without Wails. | High | Open |
| FR-009 | Composables over direct RPC | As a contributor, I want a stable layer of composables (data-loading, mutation, streaming) that components use; no component calls the RPC client directly except through composables. | High | Open |
| FR-010 | Command palette (`Cmd/Ctrl+K`) | As an operator, I want a command palette invocable from anywhere for navigation, common actions, and search. | High | Open |
| FR-011 | Accessibility baseline (WCAG 2.2 AA) | As an operator, I want every primary surface keyboard-navigable, focus-managed, and screen-reader-correct, meeting WCAG 2.2 AA. | High | Open |
| FR-012 | Policy-denied action surface | As an operator, I want any UI-initiated action denied by `policy-engine` to produce a single typed surface naming policy id, clause id, violating input, and remediation. | High | Open |
| FR-013 | Connection-lost handling | As an operator, I want a quiet connection-lost banner with retry when the frontend cannot reach `core/rpc`, not a wall of error toasts. | Medium | Open |
| FR-014 | Streaming-friendly text rendering | As an agent author, I want primitives that render token-by-token streaming text smoothly with no layout thrash and no flicker. | High | Open |
| FR-015 | Session list virtualization | As an operator with many sessions, I want the sessions list virtualized so it stays smooth past hundreds of entries. | Medium | Open |
| FR-016 | Window-size minimum + collapsing rail | As an operator, I want the layout to remain usable at small window sizes by collapsing the left rail to icons and staying responsive. | Medium | Open |
| FR-017 | First-paint state machine | As an operator, I want a clear "starting…" state when the harness boots before `core/rpc` is ready, transitioning automatically without errors. | Medium | Open |
| FR-018 | Error-boundary hygiene | As a contributor, I want top-level error boundaries that capture component crashes, log them through the event-log RPC, and present a quiet recovery affordance. | Medium | Open |
| FR-019 | Type-safe streaming consumers | As a contributor, I want streaming RPC events typed end-to-end so consumers cannot accidentally mishandle a chunk shape. | High | Open |
| FR-020 | No credential values in UI | As an operator, the UI MUST NEVER display a resolved credential value, even when configured; only references and source labels appear. | High | Open |

### Non-Functional Requirements

| ID | Title | Requirement | Category | Priority | Status |
|----|-------|-------------|----------|----------|--------|
| NFR-001 | First paint latency | Cold app open to first meaningful paint of the layout shell: under 1 second p95 on a developer laptop. | Performance | High | Open |
| NFR-002 | Theme-change paint cycle | Theme switch (light↔dark) updates the visible UI within one paint cycle (≤ 16 ms target on 60Hz). | Performance | High | Open |
| NFR-003 | Session-switch latency | Switching the active session via the left rail completes (UI swapped + state restored) under 100 ms p95. | Performance | High | Open |
| NFR-004 | Streaming smoothness | Token-by-token streaming text renders without dropped frames at 60 fps for typical model output rates. | Performance | High | Open |
| NFR-005 | Accessibility compliance | Automated axe-core scans across every primitive and primary surface return zero serious or critical violations. | Accessibility | High | Open |
| NFR-006 | Bundle size budget | Frontend production bundle (excluding embedded assets) stays under 1.5 MB gzipped for the layout-shell + primitives baseline. | Performance | Medium | Open |
| NFR-007 | RPC type fidelity | 100 % of RPC bindings exposed to the frontend are typed; no `any` escape hatches in production code. | Maintainability | High | Open |
| NFR-008 | Theme contrast | All token combinations meet WCAG 2.2 AA contrast in both light and dark themes. | Accessibility | High | Open |

### Constraints

| ID | Title | Constraint | Category | Priority | Status |
|----|-------|------------|----------|----------|--------|
| C-001 | Architectural integrity | The frontend talks to `core/` only through `core/rpc`. No frontend code imports types from any `core/` package other than RPC. No `core/` package imports anything from `frontend/`. | Technical | High | Open |
| C-002 | Owned primitives, not runtime framework | Layout, form, and feedback primitives live as code in the repo. Heavy admin-dashboard frameworks (CoreUI PRO, AdminLTE templates, Vuetify dashboard kits) are not adopted. | Technical | High | Open |
| C-003 | OSS / enterprise compatibility | The chosen design system (and any commercial themes / templates) MUST be license-compatible with the open-source build. No "PRO" templates that cannot be shipped in OSS. | Business | High | Open |
| C-004 | No credential values in UI | The UI never displays resolved credential values; only references. The credential machinery from `secrets-keychain` enforces this server-side, but UI also defends in depth. | Security | High | Open |
| C-005 | Local-first | The frontend functions with zero outbound network traffic in steady state (matches charter NFR-009 of `llm-connector` for the runtime). External links open in OS browser, not embedded. | Technical | High | Open |
| C-006 | Single accent color | Visual identity uses a single accent color matching Kenaz's warm amber. Multi-color "rainbow" badges / category chips are not used as a primary navigation mechanism (the category color system applies only to event-stream rows, legends, and stream-domain labels). | Technical | High | Open |
| C-007 | SOC 2 readiness | UI surfaces for the audit log and policy decisions present immutable views consistent with `event-log` and `policy-engine`. | Regulatory | High | Open |
| C-008 | Kenaz tokens are the source of truth | Design tokens (color, type, spacing, radius, motion, category colors) MUST be imported from the canonical Kenaz design source. The harness MUST NOT redefine tokens locally; if Kenaz changes a token, the harness adopts the change in the next bump. Kenaz design owns approval of any harness-extension category colors. | Technical | High | Open |
| C-009 | VM-host visual coherence | The harness MUST be visually indistinguishable from Kenaz surfaces when running inside the Kenaz VM. When running on the host alongside Kenaz, the same constraint holds. No "harness-only" visual identity is permitted. | Technical | High | Open |
| C-010 | Enterprise-first defaults | All UI defaults assume an enterprise deployment: dark theme default, explicit privacy-guarantee surfacing, conservative motion, no consumer-shaped onboarding flows, no playful empty states. | Business | High | Open |

### Key Entities

- **Layout Shell**: the persistent app-level layout — left rail (sessions list + primary-surface nav + new-session affordance), thin top bar (active surface title, profile / settings affordance), main content pane, optional right-side drawer for context (tool details, settings panels).
- **Session UI Record**: the per-session frontend state cache — scroll position, draft input, expanded panels, last-viewed message id. Lives in front-end memory; persisted summary in `core/` via RPC for restart resilience.
- **Theme Token Set**: typed token vocabulary — `color.bg.{primary,secondary,muted}`, `color.fg.{primary,secondary,muted,accent}`, `color.border.{default,muted,strong}`, `space.{0,1,2,3,4,...}`, `font.{sans,mono}`, `text.{xs,sm,base,lg,xl,...}`, `radius.{sm,md,lg}`, `shadow.{sm,md,lg}`, `motion.{fast,base,slow}`. Light and dark variants share token names; values differ.
- **Primitive Component**: an owned, in-repo component (e.g., `Button`, `Dialog`, `Sheet`, `Tabs`, `Input`, `Select`, `Toggle`, `Tooltip`, `Toast`, `Popover`, `CommandPalette`, `Sidebar`, `ScrollArea`, `Table`). Receives only token-derived styles; never raw colors.
- **Typed RPC Client**: a single object exposing every `core/rpc` binding as typed methods (sync calls, mutations, streams). Generated or hand-written; the only module that imports Wails-generated types.
- **Composable**: a `use*` hook layered on the RPC client (e.g., `useSession`, `useChatStream`, `useBundles`, `useProviders`, `usePolicyDecisions`, `useEventLog`). Components consume these, never the RPC client directly.
- **Surface**: a primary content pane the operator switches to from the left rail (sessions, tools, bundles, providers, audit log, settings). Each surface owns its own routes and panels.
- **Denial Notice**: typed UI element that renders a `policy-engine` denial uniformly — policy id, clause id, violating input, remediation hint.
- **Connection State**: typed runtime state for the RPC bridge — `connecting`, `ready`, `degraded`, `lost`. Drives layout-level UX (starting screen, connection-lost banner).

## Success Criteria *(mandatory)*

### Measurable Outcomes

- **SC-001**: Two designers familiar with frontier AI tools rate the harness's first-open screen ≥ 4/5 on a "feels like a frontier AI tool" axis.
- **SC-002**: Cold open to first meaningful layout paint completes under 1 second p95 on a developer laptop.
- **SC-003**: Switching the active session via the left rail completes under 100 ms p95 with state preserved.
- **SC-004**: Automated axe-core scans across every primitive and primary surface return zero serious or critical violations.
- **SC-005**: A grep of `frontend/src/` outside the typed RPC client layer finds zero imports of Wails-generated types.
- **SC-006**: A test fixture swaps the RPC client for a fake; every component renders and behaves correctly with no other changes.
- **SC-007**: Theme switch (light↔dark) and OS-theme follow update the UI within one 16 ms paint cycle on 60 Hz, with no flash of wrong theme on cold open.
- **SC-008**: Frontend production bundle (layout-shell + primitives baseline) stays under 1.5 MB gzipped.
- **SC-009**: A policy-denied action initiated from the UI surfaces a single typed denial naming policy id, clause id, violating input, and remediation, 100 % of the time.

## Assumptions

- The runtime is the existing Wails v2 + Vue 3 + Vite scaffold; no rewrite to Tauri / Electron is in scope.
- Vue 3 Composition API is the primary authoring shape; the Options API is permitted but discouraged.
- TypeScript is used end-to-end in the frontend.
- The runtime ships dark mode at parity with light mode; no "dark mode is a v2 feature."
- The frontend bundle is embedded in the binary (Wails default); no separate web hosting in v1.
- A third-party CSS / design library is acceptable as a *source* for primitives the team owns (e.g., shadcn-vue's component recipes copied into our repo); the dependency on the library itself is *not* a runtime dependency.
- The team accepts maintaining owned primitives going forward — the cost is well understood and worth it for visual identity ownership.
- Animations and motion are subtle; we follow the frontier-tool norm of restrained motion (≤ 200 ms transitions, ease-out curves).

## Open Questions

The Kenaz design alignment fixes most prior open questions. Remaining items below; each blocks implementation rather than design intent.

1. **[BLOCKS PLAN]** Canonical Kenaz token source — where does the harness import design tokens from? Options: (a) a published `@kenaz/design-tokens` package consumed via npm, (b) a CSS file vendored into `frontend/src/styles/kenaz.css` and refreshed on Kenaz design bumps, (c) a Tailwind preset shared between Kenaz and the harness. Default if unresolved: option (b) — vendored CSS with a documented refresh procedure — until Kenaz publishes a stable token package, then migrate to (a). Implementation cannot start until the token source is identified.
2. **[BLOCKS PLAN]** Kenaz design system stack — what is Kenaz itself built on? If Kenaz uses Tailwind + Radix-class primitives, the harness's prior pick (shadcn-vue + Tailwind + Radix Vue) holds and we import Kenaz tokens on top. If Kenaz uses a different stack (raw CSS custom properties, Mantine, MUI, etc.), the harness's component-library pick may need to change to align. Confirm Kenaz's stack before locking the harness's component layer.
3. **[NEEDS CLARIFICATION]** Harness-extension category colors — the source artifact shows five Kenaz categories (FILESYSTEM, PROCESS, CLIPBOARD, NETWORK, KEYSTROKE). The harness needs a parallel set for LLM, MCP, A2A, POLICY, TRUST, BUNDLE, CONTEXT, SECRETS, STORAGE, SCHEDULER. Default if unresolved: the harness proposes initial assignments from within the Kenaz palette and submits them to Kenaz design for approval before any UI ships.
4. **[NEEDS CLARIFICATION]** Light-theme parity — Kenaz's visual register is dark-first; does Kenaz ship a light theme today, and if so are its tokens defined? Default if unresolved: dark-first in v1; light-theme parity ships in v1.x once Kenaz publishes light-theme tokens. The harness MUST NOT invent its own light-theme palette.
5. **[NEEDS CLARIFICATION]** Icon set alignment — proposed default is Lucide Vue (matches the line-style register observed in the source artifact). Default if unresolved: Lucide. If Kenaz has standardized on a different icon set, the harness adopts Kenaz's pick.
6. **[NEEDS CLARIFICATION]** Source artifact completeness — the design source provided was a single rendered section ("02 / VM SANDBOX"). Is there a complete Kenaz design system document covering modals, dialogs, sheets, command palette, forms, toasts, and light-theme tokens? If yes, we need it before plan-phase. If not, we coordinate with Kenaz design to produce it as a blocker for harness UI implementation.
7. **[NEEDS CLARIFICATION]** RPC-client generation — hand-written typed wrapper over `wailsjs/go/rpc/` generated types vs a code-gen step. Default if unresolved: hand-written wrapper for v1; code-gen as a v1.x improvement once the RPC surface stabilizes. Independent of Kenaz alignment.
