# Implementation Plan: Frontend Foundations — Design System, Layout, and RPC Bridge

**Mission**: `frontend-foundations-01KQ2H3P`
**Spec**: `kitty-specs/frontend-foundations-01KQ2H3P/spec.md`
**Status**: Draft
**Created**: 2026-04-25

This is the HOW for the spec.md WHAT. It establishes the Vue 3 + shadcn-vue + Tailwind frontend chassis for kaneaz-harness, brings in the Kenaz visual identity verbatim via vendored design tokens, and defines the typed RPC bridge between the frontend and `core/rpc`.

---

## 1. Overview

The harness frontend is a Vue 3 + Vite single-page app embedded in a Wails v2 binary. This mission delivers the chassis on which every subsequent UI mission layers:

- **Stack**: Vue 3 (Composition API) + TypeScript + Vite + Vitest, styled with **Tailwind CSS** consuming **vendored Kenaz design tokens**, with primitives copied from **shadcn-vue** (built on **Radix Vue**) into `frontend/src/components/ui/` so the harness team owns them outright.
- **Visual identity**: Kenaz tokens (surfaces, ink, brass accent, signal palette, radii, Geist + Geist Mono) are vendored verbatim into `frontend/src/styles/tokens.css` and exposed to Tailwind through a token-driven theme config. No raw hex, OKLCH, or `rgba()` literal appears outside `tokens.css` — the build fails if it does.
- **Layout**: a Kenaz-aligned shell (Titlebar, Toolbar, LeftRail, CanvasHead, LegendBar) wraps a Vue Router + KeepAlive surface area. Every primary surface uses Kenaz's numbered-section header pattern (`NN / SECTION NAME` muted small caps + prominent title + subtitle paragraph).
- **RPC bridge**: a typed `harnessClient` wraps `core/rpc` Wails bindings; components consume it through `useHarnessAPI()` composables that mirror Kenaz's `KenazAPI` view-scoped accessor pattern (one top-level `HarnessAPI` interface returns sub-interfaces per view).
- **Streaming**: Go-side `streamBroker` emits one Wails event per `(view-name, event-kind)` topic, registered in `contracts/wails-events.md`. Direct `runtime.EventsEmit` calls outside `emitter.go` / `stream_broker.go` are prohibited.
- **Privacy CI invariants**: five guardrails are mirrored verbatim from Kenaz (see §4 and §6).
- **Why this stack, not Kenaz's stack**: Kenaz uses Preact + hand-written CSS. The harness keeps its already-scoped Vue + shadcn-vue + Tailwind pick. The *visual identity* is shared by adopting Kenaz tokens; the *implementation stack* is independent. This is the firm user direction for this mission.

This plan does **not** implement any feature surface (chat, MCP management, etc.) — those are downstream missions that consume this chassis.

---

## 2. Architectural placement

### 2.1 Frontend tree

```
frontend/
├── index.html
├── vite.config.ts                 # Vite + Tailwind + tsconfig paths
├── tailwind.config.ts             # consumes tokens.css via CSS variables
├── postcss.config.cjs
├── tsconfig.json
├── vitest.config.ts
├── package.json
├── src/
│   ├── main.ts                    # mounts Shell, registers router + plugins
│   ├── App.vue                    # thin wrapper: <Shell />
│   ├── shell/
│   │   ├── Shell.vue              # 3-region grid: Titlebar / [LeftRail | content+Toolbar/CanvasHead/LegendBar]
│   │   ├── Titlebar.vue           # window-drag region + brand mark + AI-output disclaimer (FR-001h)
│   │   ├── Toolbar.vue            # surface-level action row (Customize, theme switch, palette trigger)
│   │   ├── LeftRail.vue           # sessions list + primary-surface nav + new-session affordance (FR-001, FR-002)
│   │   ├── RailEntry.vue          # single rail row (icon + label + active state)
│   │   ├── CanvasHead.vue         # numbered-section header pattern: `NN / SECTION` + title + subtitle (FR-001b)
│   │   ├── LegendBar.vue          # category color legend + live-rate inline indicators (FR-001d, FR-001g)
│   │   ├── StatusPill.vue         # toolbar status pills (provider, trust tier, build) (FR-001f)
│   │   └── icons.ts               # Lucide-vue-next re-exports (line-style, low-contrast)
│   ├── views/
│   │   └── <surface>/
│   │       └── <SurfaceView>.vue  # one folder per primary surface (sessions, tools, bundles, providers, audit, settings)
│   ├── components/
│   │   └── ui/                    # owned shadcn-vue primitives (token-themed)
│   │       ├── Button.vue
│   │       ├── Dialog.vue
│   │       ├── Sheet.vue
│   │       ├── Tabs.vue
│   │       ├── Input.vue
│   │       ├── Select.vue
│   │       ├── Toggle.vue
│   │       ├── Tooltip.vue
│   │       ├── Toast.vue
│   │       ├── Popover.vue
│   │       ├── CommandPalette.vue # Cmd/Ctrl+K (FR-010)
│   │       ├── ScrollArea.vue
│   │       ├── Table.vue
│   │       ├── EventStreamRow.vue # the dense monospace row primitive (FR-001c)
│   │       ├── PrivacyGuarantees.vue  # "APPLIED" panel pattern (FR-001e)
│   │       └── DenialNotice.vue   # policy-engine denial render (FR-012)
│   ├── lib/
│   │   ├── harnessClient.ts       # typed wrapper over wailsjs bindings (FR-007, FR-019)
│   │   ├── harnessClientContext.ts # provide/inject token + fake-swap (FR-008)
│   │   ├── useHarnessAPI.ts       # composables (FR-009)
│   │   ├── routing.ts             # router setup, persisted lastRoute restore
│   │   ├── rail.ts                # rail-entry registry + ordering
│   │   ├── useKeepAlive.ts        # per-session UI-state cache (FR-002)
│   │   ├── useTheme.ts            # light/dark/system (FR-006)
│   │   ├── useConnectionState.ts  # connecting/ready/degraded/lost (FR-013, FR-017)
│   │   ├── useStream.ts           # Wails EventsOn wrapper, typed (FR-014, FR-019)
│   │   ├── useCommandPalette.ts   # palette registry + Cmd/Ctrl+K binding (FR-010)
│   │   ├── eventLog.ts            # client-side error reporter routed through RPC (FR-018)
│   │   ├── settings.ts            # persisted UI state shape + IO via RPC
│   │   ├── categories.ts          # category color registry (FR-001d)
│   │   └── types.ts               # generated/hand-curated types mirror of core/rpc payloads
│   ├── styles/
│   │   ├── tokens.css             # vendored verbatim from /Users/alecfeeman/.../kenaz/frontend/src/styles/tokens.css
│   │   ├── reset.css              # minimal CSS reset
│   │   ├── global.css             # body/font defaults, scrollbar treatment
│   │   └── shell.css              # shell-specific layout (grid regions only; colors via tokens)
│   └── assets/
│       └── fonts/
│           ├── Geist-*.woff2      # SIL OFL 1.1 — bundled, served via @font-face, font-src 'self'
│           └── GeistMono-*.woff2  # SIL OFL 1.1
└── wailsjs/                       # Wails-generated; the only place outside lib/ allowed to import these is harnessClient.ts
```

### 2.2 Backend tree (extensions to existing `core/rpc/`)

```
core/rpc/
├── api.go                # existing — extend to expose HarnessAPI (see §3)
├── api.go (new types)    # add HarnessAPI interface + view sub-interfaces
├── bindings.go           # NEW — Bindings struct that Wails reflects (wraps HarnessAPI + Settings + streamBroker)
├── emitter.go            # NEW — Emitter interface, only authorised caller of runtime.EventsEmit
├── stream_broker.go      # NEW — owns subscription lifecycle, fan-out, stream-closed payloads
├── settings.go           # NEW — LoadRoute / SaveRoute / LoadTheme / SaveTheme (single JSON file)
├── bundle.go             # existing
├── session.go            # existing — wrap behind HarnessAPI.Sessions()
├── job.go                # existing — wrap behind HarnessAPI.Workflow()
├── config.go             # existing — wrap behind HarnessAPI.Settings() / Provider config
└── views/                # NEW — one sub-package per view-scoped accessor
    ├── llm/              # LLMConnectorAPI
    ├── mcp/              # MCPAPI
    ├── a2a/              # A2AAPI
    ├── workflow/         # WorkflowAPI (wraps existing job/)
    ├── sessions/         # SessionsAPI (wraps existing session/)
    ├── trust/            # TrustAPI
    ├── context/          # ContextAPI
    ├── bundle/           # BundleAPI (wraps existing bundle/)
    ├── policy/           # PolicyAPI (Explainer hook)
    ├── audit/            # AuditAPI (event-log consumer)
    └── settings/         # SettingsAPI
```

### 2.3 Charter alignment

- **DIRECTIVE_001**: `frontend/` imports from `core/` *only* through generated `wailsjs/` — and within `wailsjs/`, only `harnessClient.ts` is allowed to import. No `core/` package imports `frontend/`. Enforced by lint rule + grep CI check (§4).
- **DIRECTIVE_003**: every material design choice in this plan (vendoring vs npm, shadcn-vue, Geist licensing) lands as an ADR under `docs/adr/`.
- **DIRECTIVE_010**: every spec FR maps to a §3–§6 placement and a v1.0 phasing entry in §7.

---

## 3. Public API

### 3.1 Go `HarnessAPI` interface (mirrors Kenaz's `KenazAPI` shape)

```go
// core/rpc/api.go — illustrative, not final.

package rpc

import (
    "context"

    "github.com/sigil-tech/kaneaz-harness/core/rpc/views/audit"
    "github.com/sigil-tech/kaneaz-harness/core/rpc/views/bundle"
    "github.com/sigil-tech/kaneaz-harness/core/rpc/views/context"
    "github.com/sigil-tech/kaneaz-harness/core/rpc/views/llm"
    "github.com/sigil-tech/kaneaz-harness/core/rpc/views/mcp"
    "github.com/sigil-tech/kaneaz-harness/core/rpc/views/a2a"
    "github.com/sigil-tech/kaneaz-harness/core/rpc/views/policy"
    "github.com/sigil-tech/kaneaz-harness/core/rpc/views/sessions"
    "github.com/sigil-tech/kaneaz-harness/core/rpc/views/settings"
    "github.com/sigil-tech/kaneaz-harness/core/rpc/views/trust"
    "github.com/sigil-tech/kaneaz-harness/core/rpc/views/workflow"
)

// HarnessAPI is the boundary between the Wails-hosted Vue frontend and the
// Go core. Top-level cross-cutting methods (ShellStatus, AppInfo) live here;
// view-specific surfaces are accessed through stable, view-scoped sub-
// interfaces. Implementations MUST be safe for concurrent use.
type HarnessAPI interface {
    ShellStatus(ctx context.Context) (ShellStatus, error)
    AppInfo(ctx context.Context) (AppInfo, error)

    LLMConnector() llm.LLMConnectorAPI
    MCP() mcp.MCPAPI
    A2A() a2a.A2AAPI
    Workflow() workflow.WorkflowAPI
    Sessions() sessions.SessionsAPI
    Trust() trust.TrustAPI
    Context() context.ContextAPI
    Bundle() bundle.BundleAPI
    Policy() policy.PolicyAPI
    Audit() audit.AuditAPI
    Settings() settings.SettingsAPI
}

// ShellStatus drives the Toolbar status pills + LegendBar live-rate
// indicators. Polled every 5 s while the window is focused; future
// optimization replaces the poll with a `shell:status-changed` push event.
type ShellStatus struct {
    ActiveProvider string  // FR-001f
    TrustTier      string  // FR-001f
    HarnessBuild   string  // FR-001f
    Connection     string  // connecting | ready | degraded | lost (FR-013, FR-017)
    EventRate      float64 // events/sec (FR-001g)
    PolicyApplied  bool    // FR-001e
    RedactionOn    bool    // FR-001e
    LocalFirstOn   bool    // FR-001e
}

// AppInfo is read once on app start; cached frontend-side for the session.
type AppInfo struct {
    Build       string
    Commit      string
    BuildTime   string
    GoVersion   string
    Platform    string
    WindowSize  WindowSize // from charter
}
```

View sub-interfaces (one example; the rest follow the same shape):

```go
// core/rpc/views/sessions/api.go — illustrative.

type SessionsAPI interface {
    List(ctx context.Context) ([]Session, error)
    Get(ctx context.Context, id string) (Session, error)
    Create(ctx context.Context, name string) (Session, error)
    Rename(ctx context.Context, id, name string) error
    Delete(ctx context.Context, id string) error
    Reorder(ctx context.Context, ids []string) error
    StartStream(ctx context.Context, id string) (subscriptionID string, err error) // sessions:event
    StopStream(ctx context.Context, subscriptionID string) error
}
```

### 3.2 Go `Bindings` struct (Wails-reflected)

```go
// core/rpc/bindings.go — illustrative.

// Bindings is what Wails reflects as the JS-callable surface. It wraps
// HarnessAPI plus settings (LoadRoute/SaveRoute/LoadTheme/SaveTheme) and the
// streamBroker. Every method on Bindings is a flat name like
// "Sessions_List" or "MCP_StartStream" so Wails can reflect it; the typed
// frontend client re-shapes them into the view-scoped accessor pattern.
type Bindings struct {
    api      HarnessAPI
    settings settings.SettingsStore
    broker   *streamBroker
}

// One method per (view, operation) pair — illustrative subset.
func (b *Bindings) ShellStatus(ctx context.Context) (ShellStatus, error)
func (b *Bindings) AppInfo(ctx context.Context) (AppInfo, error)
func (b *Bindings) LoadRoute() (string, error)
func (b *Bindings) SaveRoute(route string) error
func (b *Bindings) LoadTheme() (string, error)
func (b *Bindings) SaveTheme(theme string) error // light | dark | system
func (b *Bindings) Sessions_List(ctx context.Context) ([]Session, error)
func (b *Bindings) Sessions_StartStream(ctx context.Context, id string) (string, error)
func (b *Bindings) Sessions_StopStream(ctx context.Context, subID string) error
// ... one method per view × operation, mechanically generated from the
// HarnessAPI interface during code-gen (v1.x); hand-rolled for v1.0.
```

### 3.3 TypeScript `harnessClient` (the only module that imports `wailsjs/`)

```ts
// frontend/src/lib/harnessClient.ts — illustrative.

import * as Bindings from '../../wailsjs/go/rpc/Bindings';
import type * as W from '../../wailsjs/go/models';

export interface HarnessClient {
  shellStatus(): Promise<ShellStatus>;
  appInfo(): Promise<AppInfo>;

  loadRoute(): Promise<string>;
  saveRoute(route: string): Promise<void>;
  loadTheme(): Promise<Theme>;
  saveTheme(theme: Theme): Promise<void>;

  sessions: SessionsClient;
  mcp: MCPClient;
  a2a: A2AClient;
  llm: LLMConnectorClient;
  workflow: WorkflowClient;
  trust: TrustClient;
  context: ContextClient;
  bundle: BundleClient;
  policy: PolicyClient;
  audit: AuditClient;
  settings: SettingsClient;
}

export interface SessionsClient {
  list(): Promise<Session[]>;
  get(id: string): Promise<Session>;
  create(name: string): Promise<Session>;
  rename(id: string, name: string): Promise<void>;
  delete(id: string): Promise<void>;
  reorder(ids: string[]): Promise<void>;
  startStream(id: string): Promise<string>;
  stopStream(subscriptionId: string): Promise<void>;
}

export function createHarnessClient(): HarnessClient { /* wraps Bindings */ }
export function createFakeHarnessClient(seed?: Partial<HarnessClient>): HarnessClient { /* test fixture (FR-008) */ }
```

A Vue plugin (`harnessClientContext.ts`) provides the client via Vue's `provide/inject`; tests swap in `createFakeHarnessClient()`.

### 3.4 Composables (the only client consumers)

```ts
// frontend/src/lib/useHarnessAPI.ts — illustrative.

export function useHarnessClient(): HarnessClient { /* inject */ }
export function useSessions(): UseSessionsResult { /* CRUD + reactive list */ }
export function useChatStream(sessionId: Ref<string>): UseStreamResult<ChatEvent> { /* sessions:event */ }
export function useEventLogStream(filter: Ref<EventFilter>): UseStreamResult<AuditEntry> { /* audit:event */ }
export function useTheme(): UseThemeResult { /* light/dark/system, persisted */ }
export function useConnectionState(): Ref<ConnectionState> { /* FR-013, FR-017 */ }
export function usePolicyDecisions(): { onDenied: (cb: (d: Denial) => void) => void } { /* FR-012 */ }
export function useCommandPalette(): UseCommandPaletteResult { /* FR-010 */ }
```

---

## 4. Internal layering

### 4.1 Vue side

- **Shell mounts**: router + KeepAlive + HarnessClientContext (provided once at the root). Router restores `lastRoute` from `Settings.LoadRoute()` on first paint; falls back to `/sessions` if absent.
- **Views consume composables only**: a view never imports `wailsjs/` directly. ESLint rule `no-restricted-imports` forbids `wailsjs/*` outside `frontend/src/lib/harnessClient.ts`.
- **Primitives in `components/ui/` are token-themed shadcn-vue copies**: each primitive imports only Tailwind utility classes that resolve to CSS variables (`var(--surface-2)`, `var(--ink)`, `var(--accent)`, etc). No raw hex/rgba/oklch literal in any primitive.
- **`tokens.css` is the source of truth**: vendored verbatim from `/Users/alecfeeman/PycharmProjects/kenaz/frontend/src/styles/tokens.css`. Surfaces `#0A0A0B → #26262C`, ink `#F4F1EA → #2E2E34`, brass `#C8A56A` (active/live/CTA only), signal palette (ok/warn/danger/info/violet/neutral/git), radii 4/8/12, Geist + Geist Mono. **Do not redefine. Do not re-derive.**
- **Tailwind theme config** (`tailwind.config.ts`) maps token CSS variables into Tailwind utilities: `theme.colors.surface[0..4]`, `theme.colors.ink[default|muted|subtle|dim|trace]`, `theme.colors.accent[default|muted|dim]`, `theme.colors.signal[ok|warn|danger|info|violet|neutral|git]`, `theme.borderRadius[sm|md|lg]`, `theme.fontFamily[ui|mono]`. shadcn-vue components become token-driven by construction.
- **KeepAlive** (`useKeepAlive.ts`) caches each session view's scroll position + draft input in front-end memory keyed by session id. Persisted summaries (last-viewed message id, expanded panels) round-trip through RPC for restart resilience.
- **First-paint state machine** (`useConnectionState.ts`): the Shell renders a quiet "starting…" state until the first `ShellStatus` poll succeeds; transitions are stateful (`connecting → ready`; `ready ↔ degraded`; any → `lost` after N failed polls). The connection-lost banner is a single dismissable `<DenialNotice>`-style component, not a toast wall (FR-013).

### 4.2 Go side

- **`Emitter` interface** is the only authorised caller of `runtime.EventsEmit`. Defined in `core/rpc/emitter.go`, injected into `streamBroker`. A grep CI check forbids `runtime.EventsEmit` outside these two files.
- **`streamBroker`** owns subscription lifecycle: `Subscribe(viewName, eventKind, source <-chan T) (id, error)`, `Unsubscribe(id)`, and a pump that emits `(view-name):(event-kind)` topic + `(view-name):stream-closed` payload `{ id, reason, message? }` on close. `reason ∈ {ctx-cancelled, stop-called, backend-error}`. Mirrors Kenaz's spec 024 broker exactly.
- **Topic registry** lives at `contracts/wails-events.md` (new file in this repo, mirroring the Kenaz registry's structure). Reserved entries for v1.0: `sessions:event`, `mcp:event`, `a2a:event`, `llm:event`, `policy:event`, `audit:event`, `workflow:event`, `bundle:event`, `context:event`, `secrets:event`, `storage:event`, `scheduler:event`, plus a `:stream-closed` for each. Adding a topic requires a registry edit in the same PR (DIRECTIVE_010).
- **View accessors are stable instances**: each `HarnessAPI.<View>()` call returns the same Go pointer for the lifetime of the API value (mirrors Kenaz's ADR-024a). `var _ HarnessAPI = (*fakeHarnessAPI)(nil)` compile-time check lives in `core/rpc/api_test.go`.

### 4.3 Five privacy CI invariants (mirrored verbatim from Kenaz)

1. **Strict CSP**: `Content-Security-Policy: default-src 'none'; connect-src 'none'; script-src 'self'; style-src 'self' 'unsafe-inline'; img-src 'self' data:; font-src 'self'; base-uri 'none'; form-action 'none'; frame-ancestors 'none'; object-src 'none'`. Set in `index.html` `<meta http-equiv>` AND duplicated as a Wails response-header policy where supported. **No CDNs.** CI grep test: `script-src` MUST NOT contain `unsafe-inline` or `unsafe-eval`; `connect-src` MUST be exactly `'none'` (Wails IPC bypasses fetch, so this is enforceable).
2. **No user-content fields in slog calls**: a CI script greps every Go file and fails the build if any `slog.*` call references fields named `Subject`, `SubjectDim`, `Body`, `Prompt`, `Response`, `DraftInput`, `Path`, or any field flagged `// privacy:never-log` in struct comments. Mirrors Kenaz spec 024 FR-027.
3. **Test-only hooks stay in `_test.go` files**: a CI script forbids exported identifiers prefixed `Test*`, `Fake*`, `Stub*`, or `Fixture*` in non-test files in `core/rpc/` and `core/rpc/views/*`.
4. **CSS token discipline**: a CI script greps `frontend/src/` and fails the build if any file other than `frontend/src/styles/tokens.css` contains a raw `#[0-9a-fA-F]{3,8}` literal, an `rgb(...)` / `rgba(...)` / `hsl(...)` / `oklch(...)` literal, or a hardcoded font stack. Tailwind utilities and `var(--token)` references are the only way to express color in the codebase.
5. **Single-file persistence with schema versioning**: all UI state lives in `$USER_CONFIG_DIR/kaneaz-harness/settings.json` with a top-level `schemaVersion: 1` integer. The file is read once on app start, written debounced (250 ms) on change. No second persistence file. Rotation/migration logic gates `schemaVersion` mismatches behind a guarded migration step. Charter `WindowSize` defaults are read from the charter at first run and merged in — never re-read after first persist.

---

## 5. Data model

### 5.1 Token vocabulary (vendored from Kenaz `tokens.css`)

| Group     | Tokens                                                                                                                                  |
|-----------|----------------------------------------------------------------------------------------------------------------------------------------|
| Surfaces  | `--surface-0` `#0A0A0B`, `--surface-1` `#101012`, `--surface-2` `#17171A`, `--surface-3` `#1E1E22`, `--surface-4` `#26262C`             |
| Borders   | `--border` `#22222A`, `--border-muted` `#18181D`, `--border-strong` `#2E2E36`                                                          |
| Ink       | `--ink` `#F4F1EA`, `--ink-muted` `#A0A0A8`, `--ink-subtle` `#6F6F78`, `--ink-dim` `#48484F`, `--ink-trace` `#2E2E34`                    |
| Accent    | `--accent` `#C8A56A`, `--accent-muted` `#8A7550`, `--accent-dim` `#4A3F2A`, `--accent-glow` `rgba(200,165,106,0.12)`, `--accent-hairline` `rgba(200,165,106,0.3)` |
| Signal    | `--ok` `#7FB392`, `--warn` `#D4A34A`, `--danger` `#D48280`, `--info` `#8DA4C4`, `--violet` `#A58BC4`, `--signal-neutral` `#8A8F99`, `--signal-git` `#6FA8A0` |
| Modal     | `--modal-overlay` `rgba(10,10,11,0.72)`, `--modal-shadow` `rgba(0,0,0,0.6)`                                                            |
| Radii     | `--radius-sm` 4px, `--radius-md` 8px, `--radius-lg` 12px                                                                                |
| Type      | `--font-ui` Geist + system fallbacks, `--font-mono` Geist Mono + monospace fallbacks                                                    |
| Breakpoint| `--breakpoint-two-col` 960px                                                                                                           |
| Motion    | (harness-local until Kenaz publishes) `--motion-fast` 120ms, `--motion-base` 200ms, `--motion-slow` 320ms, all `cubic-bezier(0.2,0,0,1)` |

### 5.2 Event-stream row shape (FR-001c)

```
[timestamp] · [CATEGORY DOT + LABEL] · [subject] · [trailing-metadata]
```

- `timestamp`: RFC 3339 second-precision, UTC, monospace, `--ink-muted`.
- Category dot: 6×6 px, `border-radius: 50%`, color from §5.3 registry; label uppercase tracking-wide small caps, color matches dot.
- `subject`: ≤ 256 UTF-8 bytes, monospace, `--ink`. Truncated mid-string with `…` if it would wrap.
- `trailing-metadata`: optional secondary text + optional size chip, `--ink-subtle`.

Implemented as `<EventStreamRow>` primitive consumed by audit-log viewer, live LLM/MCP/A2A/scheduler streams, and any future monitoring surface.

### 5.3 Category color registry (FR-001d)

Kenaz's existing 5 (preserved verbatim, sourced from Kenaz palette):

| Category      | Token     | Color (proposed-from-Kenaz-palette) |
|---------------|-----------|-------------------------------------|
| FILESYSTEM    | `--info`  | `#8DA4C4` (blue)                    |
| PROCESS       | `--ok`    | `#7FB392` (green)                   |
| CLIPBOARD     | `--warn`  | `#D4A34A` (orange)                  |
| NETWORK       | `--danger`| `#D48280` (pink/magenta)            |
| KEYSTROKE     | TBD-yellow| (Kenaz-defined; vendor on bump)     |

Harness extensions (10 — proposed; final assignments owned by Kenaz design per FR-001d):

| Category   | Proposed token         | Notes                                   |
|------------|------------------------|-----------------------------------------|
| LLM        | `--accent`             | warm brass; this is the active provider |
| MCP        | `--violet` `#A58BC4`   | distinct from FILESYSTEM/NETWORK        |
| A2A        | `--signal-git` `#6FA8A0` | muted teal — agent-to-agent             |
| POLICY     | `--danger` (subtle)    | shares with NETWORK semantically OK     |
| TRUST      | `--ok` (subtle)        | shares with PROCESS semantically OK     |
| BUNDLE     | TBD-amber-2            | needs Kenaz-defined extension           |
| CONTEXT    | TBD-blue-2             | needs Kenaz-defined extension           |
| SECRETS    | `--signal-neutral`     | deliberately desaturated                |
| STORAGE    | TBD-green-2            | needs Kenaz-defined extension           |
| SCHEDULER  | TBD-violet-2           | needs Kenaz-defined extension           |

The registry lives at `frontend/src/lib/categories.ts` as `Record<Category, { token: string; label: string }>`. Final assignments are reviewed and pinned by Kenaz design before any UI ships against them (open question §9.3 of spec).

### 5.4 Wails event topic registry (mirrors Kenaz's `contracts/wails-events.md`)

A new file `contracts/wails-events.md` at the repo root lists every topic, payload (Go + TS), cardinality, lifecycle, ordering, privacy. v1.0 entries: `sessions:event`, `mcp:event`, `a2a:event`, `llm:event`, `policy:event`, `audit:event`, `workflow:event`, `bundle:event`, `context:event`, `secrets:event`, `storage:event`, `scheduler:event`, plus `:stream-closed` for each. `shell:status-changed` is reserved for v1.x.

### 5.5 Persisted UI state

Single JSON at `$USER_CONFIG_DIR/kaneaz-harness/settings.json`:

```json
{
  "schemaVersion": 1,
  "lastRoute": "/sessions",
  "theme": "system",
  "accent": "default",
  "windowSize": { "width": 1280, "height": 800 }
}
```

Read once on boot, written debounced (250 ms) on change. Migrations gated on `schemaVersion`. **No** session-by-session state lives here — that's per-session via RPC summaries.

---

## 6. Integration points

| Integration              | Mechanism                                                                                       |
|--------------------------|-------------------------------------------------------------------------------------------------|
| `core/rpc` bindings      | hand-written `harnessClient.ts` wrapper for v1.0; codegen step for v1.x once RPC stabilizes (open question §9.7 of spec) |
| `event-log.ConsumerAPI`  | the audit-log viewer surface consumes via `HarnessAPI.Audit().ListEntries()` + `audit:event` stream; verifies append-only invariant via `VerifyEntry` |
| `policy-engine.Explainer`| `HarnessAPI.Policy().Explain(input)` returns `Denial { policyID, clauseID, violatingInput, remediation }`; the `<DenialNotice>` primitive renders it uniformly (FR-012) |
| `secrets-keychain.Resolver` | UI calls `HarnessAPI.Trust().GetSecretReference(id)` returning `{ id, label, source, createdAt }` only — never the resolved value (FR-020, C-004). UI defends in depth: TS type for credential references has no `value` field at all. |
| Charter `WindowSize`     | read once at app start via `HarnessAPI.AppInfo()`; applied to first window if no persisted size exists |
| `useStream` consumer     | every view that subscribes to a `*:event` topic uses the same composable; `:stream-closed` with `reason=backend-error` transitions the view to a `<ErrorState>` while the rest of the surface remains rendered (mirrors Kenaz spec 024) |

---

## 7. Phasing

### v1.0 (this mission's deliverable)

1. Vue 3 + Vite + TypeScript + Vitest scaffold; ESLint + Prettier + `vue-tsc` clean.
2. `tokens.css` vendored verbatim from Kenaz; Tailwind theme config consumes it.
3. Geist + Geist Mono bundled at `frontend/src/assets/fonts/` (SIL OFL 1.1, vendor licence file alongside).
4. Shell components: `Shell.vue`, `Titlebar.vue`, `Toolbar.vue`, `LeftRail.vue`, `RailEntry.vue`, `CanvasHead.vue`, `LegendBar.vue`, `StatusPill.vue`, `icons.ts`.
5. shadcn-vue primitives copied into `frontend/src/components/ui/` and token-themed: Button, Dialog, Sheet, Tabs, Input, Select, Toggle, Tooltip, Toast, Popover, CommandPalette, ScrollArea, Table, EventStreamRow, PrivacyGuarantees, DenialNotice.
6. `HarnessAPI` interface + view sub-interfaces in `core/rpc/`; `Bindings` struct Wails-reflected.
7. `streamBroker` + `Emitter` + `contracts/wails-events.md` (12 view topics × 2 kinds = 24 registered entries).
8. `harnessClient.ts` typed wrapper + `harnessClientContext.ts` provide/inject + `useHarnessAPI()` composables.
9. Command palette (`Cmd/Ctrl+K`) — app-level actions (open settings, switch theme, start new session).
10. Dark theme default; theme switch via `useTheme` (light theme falls back to dark until Kenaz publishes light tokens — per open question §9.4 of spec).
11. Accessibility baseline: axe-core integrated into Vitest; zero serious/critical at PR gate (NFR-005, SC-004).
12. **All five Kenaz privacy CI invariants** implemented and gating the PR: CSP grep, slog grep, test-only-symbols grep, CSS token-discipline grep, single-file-persistence schemaVersion check.
13. Connection-lost banner (FR-013), first-paint state machine (FR-017), error-boundary hygiene routed through `eventLog.ts` (FR-018).
14. Virtualized session list — minimal: deferred to v1.x unless straightforward via `vue-virtual-scroller`; FR-015 marked v1.x if scope-pressed.
15. AI-output disclaimer chrome in Titlebar (FR-001h); Privacy-guarantees panel primitive (FR-001e); Model · Tier · Build status footer (FR-001f); live-rate inline indicators (FR-001g).

### v1.x

- Light-theme parity once Kenaz publishes light-theme tokens (the harness MUST NOT invent its own light palette per spec assumption).
- Audit log viewer surface (consumes the `<EventStreamRow>` primitive established here).
- Virtualized session list (FR-015) if deferred from v1.0.
- `shell:status-changed` push event replaces the 5-second poll (mirrors Kenaz ADR-023c Decision 4).
- RPC client codegen replaces hand-written wrapper (open question §9.7).

### v2

- Cross-app design-token sync via a published `@kenaz/design-tokens` npm package once Kenaz ships one. Harness migrates from vendored CSS to package consumption (open question §9.1 default, target migration).
- Shared Tailwind preset between Kenaz and harness once both projects use Tailwind (would require Kenaz migration; harness-side is already Tailwind in v1.0).

---

## 8. Risk register

| ID  | Risk                                                                                                              | Likelihood | Impact | Mitigation |
|-----|-------------------------------------------------------------------------------------------------------------------|-----------|--------|------------|
| R-1 | **Kenaz token drift** — vendoring `tokens.css` means a Kenaz colour bump silently lags in the harness             | High      | Medium | Document a refresh-cadence procedure (every Kenaz release). CI alert when upstream `tokens.css` hash changes vs vendored copy. Migrate to npm package (v2) as soon as Kenaz publishes one. |
| R-2 | **shadcn-vue + Tailwind diverges from Kenaz's hand-written-CSS approach** — primitives may visually drift even with shared tokens | Medium    | Medium | Visual-regression Storybook with side-by-side Kenaz screenshots. Token-only theming (no per-primitive colour) keeps drift bounded to layout/density only. Periodic Kenaz design review per spec FR-009. |
| R-3 | **Geist font licensing audit** — SIL OFL 1.1 is permissive but enterprise procurement sometimes flags self-hosted fonts | Low       | Low    | Bundle the OFL 1.1 licence file at `frontend/src/assets/fonts/OFL.txt`. Add an entry to the project NOTICES file. ADR documenting the licence decision (DIRECTIVE_003). |
| R-4 | **CSP strictness breaks Vue dev tooling** — `script-src 'self'` blocks Vite HMR; `style-src 'self'` blocks scoped style injection | Medium    | High   | Two CSPs: relaxed in dev (`unsafe-eval` for HMR, `unsafe-inline` for styles) gated by a `import.meta.env.DEV` guard; strict in production builds. CI test runs the production CSP grep against the production-built `index.html` only. |
| R-5 | **RPC streaming smoothness under heavy event volume** — token-by-token streams at ≥ 30 Hz can cause Vue re-render thrash | Medium    | High   | Use `markRaw` + manual `triggerRef` patterns for stream buffers; batch updates via `requestAnimationFrame` (NFR-004). Streaming-friendly text primitive uses `<pre>` + content-editable=false + manual DOM append for the hot path; reactive only for "session is streaming" flag. |
| R-6 | **Wails reflection binding name collisions** — flat method names like `Sessions_List` could collide if a view adds an underscore | Low       | High   | Forbid underscores inside `<view>` and `<operation>` names; reserve `_` strictly as the separator. Lint rule on Bindings method names. |
| R-7 | **Open questions §9.3 / §9.6 (category colour assignments and design-doc completeness) block downstream UI missions if not resolved** | High      | Medium | Plan calls out the resolution gate (Kenaz design review). v1.0 ships with proposed assignments behind a feature flag; downstream missions block on Kenaz design sign-off, not on this mission. |
| R-8 | **Bundle size budget (NFR-006: < 1.5 MB gzipped) at risk** — shadcn-vue + Radix Vue + Lucide-vue-next + Geist fonts all add weight | Medium    | Medium | Tree-shakable imports only (`import { X } from 'lucide-vue-next'`, never `import * as`). Geist subset to Latin + numerics. CI bundle-size check at PR gate. |
| R-9 | **First-paint flash of unthemed content** — CSS-variable theming + cold app start can flash light before tokens.css parses | Medium    | High   | Inline a 200-byte critical token block in `index.html` `<style>` for surfaces + ink + accent only; rest of the tokens load from `tokens.css`. NFR-002 + SC-007 enforce this. |

---

## 9. Open questions

1. **Kenaz token-source mechanism** — vendored CSS now (option b in spec §9.1) vs published npm package once Kenaz ships one (option a). Plan defaults to vendored CSS for v1.0 with a documented refresh procedure; migrate to npm package in v2. **Decision needed before v1.0 freeze**: confirm vendored-CSS path is the right v1 bridge.
2. **Tailwind config structure** — harness-only Tailwind preset for v1.0 vs preparing a shared preset that Kenaz could later consume (Kenaz currently uses hand-written CSS). Plan defaults to harness-only with token-mapping config; a shared preset is a v2 follow-up coordinated with Kenaz design. **Decision needed**: do we structure `tailwind.config.ts` with future-shared-preset extraction in mind?
3. **shadcn-vue component-vendoring procedure** — copy primitives at v1.0 freeze and never auto-track upstream (full ownership) vs follow upstream releases via a documented sync script. Plan defaults to **copy-and-own**, no auto-sync, per FR-004 / C-002 ("owned primitives, not runtime framework"). **Decision needed**: confirm the team accepts the maintenance cost (spec assumption already states yes).

---

**End of plan.**
