# Accessibility Backlog Cleanup — v0.16.1

Mission: `a11y-backlog-cleanup-01NDFSEX07`

Target release: **v0.16.1**

## Status

| WP | Title | Closes | Status |
|---|---|---|---|
| WP01 | Cheap mechanical fixes — providers table `scope` + NodePalette label | D-08, D-09 | Shipped |
| WP02 | Modal/drawer migration to `radix-vue` `<Dialog>` | D-04, D-05, D-07 | Shipped |
| WP03 | SlashAutocomplete `aria-activedescendant` + `tabindex="-1"` | D-03 | Shipped |
| WP04 | LeftRail `⋯` icon-button + context-menu keyboard access | D-01 | Shipped |
| WP05 | LeftRail "Move to project" keyboard menu | D-02 | Shipped |
| WP06 | Audio/video caption feasibility + ship-or-defer | D-06 | Shipped (deferred post-1.0) |
| WP07 | Bulk `form-control-has-label` fixes across 20 components | D-10 | Shipped |
| WP08 | Update audit report + delete stubs + mission close | — | Shipped |

## What shipped in v0.16.1

### D-01 — LeftRail context menu keyboard trigger (WP04)

`shell/LeftRail.vue` now renders a `⋯` icon-button on every project header row.
Clicking/pressing Enter opens the Rename/Delete context menu without requiring
a right-click. The button carries `aria-label="Project options for {name}"`,
`aria-haspopup="menu"`, and `aria-expanded`. The menu itself has `role="menu"`,
each item has `role="menuitem"`, and Escape closes the menu.

### D-02 — Drag-and-drop keyboard alternative (WP05)

A `↗` "Move to project" icon-button is now rendered on every session row
(both inside project groups and in the flat loose-sessions view). Clicking
opens a `role="menu"` sub-panel listing all available projects plus a
"No project (global)" option. This gives keyboard users a non-drag path
for reorganizing sessions across projects.

### D-03 — SlashAutocomplete listbox ARIA (WP03)

Each `<li role="option">` in `SlashAutocomplete.vue` now has:
- `:id="\`slash-opt-${index}\`"` — unique DOM id per option
- `tabindex="-1"` — focusable via `aria-activedescendant` without being in tab order

The wrapping `<ul role="listbox">` now carries
`:aria-activedescendant="\`slash-opt-${activeIndex}\`"` (or `undefined` when
no item is active). Screen readers will announce the active option as the
user navigates with arrow keys.

### D-04, D-05 — ArtifactPreview modal focus-trap + restore-focus (WP02)

`views/artifacts/ArtifactPreview.vue` migrated from a home-grown overlay
`<div>` to Radix-Vue `<DialogRoot>` / `<DialogContent>`. The Dialog primitive
provides focus trap (Tab cycles within modal), Esc dismiss, and automatic
restore-focus to the trigger element — all for free.

### D-06 — Audio/video captions deferred post-1.0 (WP06)

WKWebView WebVTT support was investigated. Wails 2.x surfaces the renderer as
WKWebView ≥ 15.x on macOS 12+, which does support `<track kind="captions">` in
principle, but the companion `.vtt` discovery mechanism requires a new
`Artifacts_FindCompanionCaption` RPC that is out of scope for a patch release.
Decision: **deferred post-1.0**. Full rationale and implementation pointers
in `docs/a11y-captions-deferral.md`.

### D-07 — ProvidersView drawer focus-trap (WP02)

`views/providers/ProvidersView.vue` right-anchored add-provider drawer migrated
to Radix-Vue `<DialogRoot>` / `<DialogContent>` (same pattern as D-04).

### D-08 — Providers table `scope="col"` (WP01)

All five `<th>` elements in the providers table header row now carry
`scope="col"`.

### D-09 — NodePalette filter label (WP01)

The filter `<input>` in `views/agentgraph/NodePalette.vue` now has
`aria-label="Filter node types"`.

### D-10 — Bulk `form-control-has-label` fixes (WP07)

`aria-label` (or an `ariaLabel` prop threaded from parent question text) was
added to every unlabeled input, select, and textarea across 20 components:

**AskUserQuestion family** — `ariaLabel?: string` prop added to each question
sub-component (Text, Number, Slider, Date); the parent `AskUserQuestion.vue`
threads `head.question` as the label.

**All other views** — direct `aria-label` attributes added to:
`BranchAdvisorSettings`, `HooksPanel`, `HooksView`, `BundlesView`,
`ContextPreview`, `HookJournalView`, `MemoryView`, `PolicyView`,
`PlanApprovalModal`, `SessionAutonomyPanel`, `DirectoryPicker`,
`KaneazToolsPanel`, `PasteConfigTab`, `RecipeKeyPromptModal`, `CatalogView`.

## Axe-core regression tests

The six `*.a11y.test.ts` files added in the v0.16.0 audit (WP02 of
`accessibility-audit-01KQ8TDA`) provide ongoing regression coverage. The WP02
and WP03 commits in this mission extend those files with assertions for the new
Radix-Vue Dialog structure.

## Open items (not closed by this mission)

| ID | Finding | Status |
|---|---|---|
| NMV-01 | color-contrast (all views) | Needs manual browser/scanner run against Wails build |
| NMV-02 | VoiceOver behaviour with WKWebView | Needs macOS + VoiceOver + Wails desktop smoke |
| NMV-03 | `role="switch"` VoiceOver cadence (Settings) | Needs VoiceOver manual test |
| NMV-04 | 200% zoom layout integrity | Needs visual test at 200% browser zoom |
| D-06 | Audio/video captions | Deferred post-1.0 — see docs/a11y-captions-deferral.md |
