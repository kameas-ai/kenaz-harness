# tasks.md — Markdown / rendering polish (`markdown-rendering-polish-01KQ8TDT`)

Eight work packages, sequenced by dependency. Each WP lands as a self-contained commit on `feat/markdown-rendering-polish-01KQ8TDT`. The WP06–WP08 trio can run in parallel after WP05 if multiple agents pick up the mission.

## WP01 — Foundations: settings enum + sanitize helper + toast queue

**Depends on**: nothing.

- Extend `frontend/src/lib/types.ts` with `MarkdownExtensions` type and `Settings.markdownExtensions?: MarkdownExtensions`.
- Add a "Rendering" sub-section to `frontend/src/views/settings/SettingsView.vue` with a four-radio control (basic / math / diagrams / all). Wire through `debouncedSave`.
- New `frontend/src/lib/markdown/sanitize.ts` exporting `sanitize(html, profile)` with profiles `default`, `katex`, `mermaid-svg`.
- New `frontend/src/composables/useToastQueue.ts` (singleton queue + 5s auto-dismiss + Undo callback support). Mount toast root in `App.vue`.
- Vitest: `sanitize.test.ts` (default strips `<script>`, katex profile keeps `span.katex`, mermaid profile keeps `<svg>` + strips `<foreignObject>`); `useToastQueue.test.ts` (push, auto-dismiss, undo).
- **Acceptance**: enum lands in `Settings`; settings UI persists choice across reload; sanitize unit tests pass.

## WP02 — `MarkdownBlock.vue` consolidation + StreamingText shim

**Depends on**: WP01 (uses `sanitize`).

- New `frontend/src/components/rendering/MarkdownBlock.vue`. Replicates current `StreamingText` pipeline (marked + DOMPurify) plus injected `markdownExtensions` value (default `'all'`).
- Reduce `frontend/src/components/chat/StreamingText.vue` to a delegating shim — same props, renders `<MarkdownBlock>` + streaming caret.
- Move shared markdown CSS (paragraphs / lists / headings / blockquote / inline code / pre / link / hr / table tokens) into `MarkdownBlock.vue` scoped style; `StreamingText.vue` keeps only caret styles.
- Vitest: `MarkdownBlock.test.ts` (FR-001) — renders GFM, sanitizes, respects `extensions='basic'` no-op for math/mermaid (regression baseline before WP04/WP05).
- **Acceptance**: existing `StreamingText.test.ts` still passes; new MarkdownBlock test green; visual no-change in chat surface.

## WP03 — `CodeBlock.vue` with header, copy, save-as-artifact (silent + Undo toast), collapse

**Depends on**: WP01 (toast queue), WP02 (placeholder swap mechanism).

- New `frontend/src/components/rendering/CodeBlock.vue` — lang pill + Copy + Save-as-Artifact + collapse footer (>30 lines).
- Add custom marked extension in `MarkdownBlock.vue` that emits `<div data-md-block="code" data-lang data-source-b64>` placeholders; mount-time walker swaps to `<CodeBlock>` instances.
- Save-as-artifact: compute `${lang}-${sha8}.${ext}` title, call `client.sessions.saveAsArtifact`, push `useToastQueue` toast with 5s + Undo (Undo calls `client.artifacts.delete`). Disable Copy/Save while `streaming === true`.
- Vitest: `CodeBlock.test.ts` (FR-002, FR-003) — header buttons render, Copy writes clipboard, Save calls RPC + pushes toast + Undo deletes; >30 line block collapses then expands.
- **Acceptance**: chat code blocks display the new header; manual smoke through Save→Undo round-trip works.

## WP04 — KaTeX math (lazy)

**Depends on**: WP02.

- Install `katex` dep (npm install katex; `--save`). Add per-chunk bundle-size cap.
- New `frontend/src/components/rendering/MathInline.vue`, `MathBlock.vue`, `frontend/src/composables/useKatex.ts` (lazy import + CSS).
- Custom marked extension recognizes `$...$` (inline, requires non-space neighbor) and `$$...$$` (block); streaming-safe (no close → no token).
- Wire `markdownExtensions` gate: skip math tokenizing when value is `basic` or `diagrams`.
- Vitest: `Math.test.ts` (FR-004, FR-008) — inline + block render; partial `$x^2` (no close) stays as text; sanitize keeps `span.katex`; `extensions='basic'` renders raw text.
- **Acceptance**: fixture message `$E=mc^2$` and `$$\int_0^1 x dx$$` render as KaTeX; gate works.

## WP05 — Mermaid diagrams (lazy, theme-auto)

**Depends on**: WP02, WP03 (CodeBlock short-circuit).

- Install `mermaid` dep. Per-chunk bundle cap.
- New `frontend/src/components/rendering/MermaidDiagram.vue` + `frontend/src/composables/useMermaid.ts`.
- `CodeBlock.vue` short-circuits when `lang === 'mermaid'` to `<MermaidDiagram>`.
- Theme auto-pick: read existing theme ref (`Settings.theme` projection); pass `default` (light) or `dark` (dark) into `mermaid.initialize`. Watch theme ref → re-render on change. Per-diagram `%%{init: ...}%%` works through Mermaid parser unchanged.
- Render error → fallback to plain code fence + warning chip.
- Debounce 120ms while `streaming === true`.
- Gate via `markdownExtensions` (`basic` / `math` skip).
- Vitest: `MermaidDiagram.test.ts` (FR-005) — renders SVG (mock `mermaid.render`); theme switch triggers re-render; render error shows fallback; gate works.
- **Acceptance**: fixture flowchart renders SVG; theme toggle re-themes; bad mermaid syntax falls back gracefully.

## WP06 — Tables: overflow scroll wrapper + alternating rows

**Depends on**: WP02.

- In `MarkdownBlock.vue`, post-process `<table>` nodes (or via marked renderer hook) to wrap each in `<div class="md-table-wrap">` (`overflow-x: auto`).
- Add `--surface-2-alt` token to `frontend/src/styles/tokens.css` if missing; alternating-row rule on `tbody tr:nth-child(even)`.
- Vitest: `MarkdownBlock.tables.test.ts` (FR-006) — wide table is wrapped; alternating-row class applied; `npm run check:css-tokens` passes.
- **Acceptance**: wide tables scroll horizontally; alternating rows visible.

## WP07 — `@filename` and `@artifact:` chips

**Depends on**: WP02.

- New `frontend/src/components/rendering/FileMentionChip.vue`.
- Custom marked text-walker emits `<span data-md-mention="...">` placeholders; mount-time walker swaps to `FileMentionChip`.
- Resolution: `@artifact:<id>` → `client.artifacts.get`; `@<path>` → optimistic enable + best-effort `Tools.OpenInEditor` (clipboard fallback toast on failure).
- Q2=A: missing target (artifact not found / `client.fs.exists` false when API present) renders disabled chip with `file not found` tooltip; click is no-op.
- Wire chip-click → `MessageBubble` `open-artifact` emit (existing channel) for artifact case; file case handled inside the chip.
- Vitest: `FileMentionChip.test.ts` (FR-007) — happy path renders chip + click invokes editor RPC; missing target → tooltip + no-op; artifact form opens preview.
- **Acceptance**: in-chat `@frontend/src/main.ts` opens editor (or clipboard); missing path shows tooltip; `@artifact:<id>` opens preview.

## WP08 — Polish, perf bench, docs, full coverage sweep

**Depends on**: WP01–WP07.

- Add Vitest fixture: a single message with code fence (40 lines), inline math, block math, mermaid, table, file mention, artifact mention. Assert all render correctly together (FR-011 success criterion §7).
- Bench script (or Vitest test with `performance.now()`) ensures NFR-001 under 100 ms post-stream on the fixture; gate as a soft check (warn, not fail) since CI hardware varies.
- Update `scripts/ci/check-bundle-size.mjs` per-chunk caps for `katex` and `mermaid` async chunks (NFR-002).
- Update `frontend/README.md` and add an ADR under `docs/adr/` (per charter `DIRECTIVE_003`) titled "Markdown rendering pipeline (MarkdownBlock)".
- PR-prep: ensure `vue-tsc --noEmit`, `eslint`, `prettier --check`, `vitest run`, `npm run check:bundle-size`, `npm run check:css-tokens` all green.
- **Acceptance**: full smoke checklist (plan §4 step 4) passes; CI green; ADR landed.

## Sequencing summary

```
WP01 ──┬── WP02 ──┬── WP03 ──┬── WP05 ──┐
       │          ├── WP04 ─────────────┤
       │          ├── WP06 ─────────────┤
       │          └── WP07 ─────────────┤
       │                                 │
       └─────────────────────────────────┴── WP08
```

WP04, WP06, WP07 are independent after WP02 and may run in parallel.
WP05 needs WP03's `CodeBlock` short-circuit hook.
WP08 is the closing pass — runs last.

### Critical Files for Implementation

- /Users/alecfeeman/PycharmProjects/kaneaz-harness/frontend/src/components/chat/StreamingText.vue
- /Users/alecfeeman/PycharmProjects/kaneaz-harness/frontend/src/components/chat/MessageBubble.vue
- /Users/alecfeeman/PycharmProjects/kaneaz-harness/frontend/src/views/settings/SettingsView.vue
- /Users/alecfeeman/PycharmProjects/kaneaz-harness/frontend/src/lib/types.ts
- /Users/alecfeeman/PycharmProjects/kaneaz-harness/frontend/src/lib/harnessClient.ts
