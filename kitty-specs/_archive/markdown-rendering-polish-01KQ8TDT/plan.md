# plan.md — Markdown / rendering polish (`markdown-rendering-polish-01KQ8TDT`)

## 0. Mission summary

Consolidate every chat markdown render path through a single `MarkdownBlock.vue` component with a header bar on code fences (language tag + Copy + Save-as-Artifact), long-block collapse, KaTeX math, Mermaid diagrams, themed tables, file-mention chips, streaming safety, and DOMPurify sanitization. Heavy renderers (KaTeX, Mermaid) are lazy-loaded; a `Settings.MarkdownExtensions` enum (`basic | math | diagrams | all`, default `all`) lets users opt out for performance.

Locked decisions:

- **Q1 = A**: Code-block "Save as artifact" is silent; the flow surfaces a 5-second toast `Saved as artifact: <title>` with an `Undo` link.
- **Q2 = A**: File-mention chip whose target is missing renders the chip but disabled with a `file not found` tooltip; click is a no-op (no fuzzy search in v1).
- **Q3 = B**: Mermaid theme auto-picks (`default` for light harness theme, `dark` for dark) at render time. Per-diagram `%%{init: {'theme': '...'}}%%` overrides flow through Mermaid's parser unchanged — documented as a power-user escape hatch.

## 1. Branch contract

- **Branch**: `feat/markdown-rendering-polish-01KQ8TDT` (per charter §Branch Strategy: `feat/<slug>` + mission slug).
- **Base**: `main`.
- **Merge style**: squash via PR; PR description references this mission slug per `DIRECTIVE_010`.
- **Quality gates** (charter §Quality Gates): `eslint`, `prettier --check`, `vue-tsc --noEmit`, `vitest run`, `npm run check:bundle-size`, `npm run check:css-tokens` all clean.
- **Bundle budget**: see NFR-002 — total compressed delta < 200 KiB. KaTeX + Mermaid MUST land in async chunks; the synchronous chat bundle delta MUST be < 15 KiB.
- **Commit hygiene**: explicit staging only (`DIRECTIVE_033` — never `git add .`). Conventional Commits.
- **No backend changes** beyond reuse of existing `client.sessions.saveAsArtifact` and (if available) a `Tools.OpenInEditor` JSON-RPC route. If `OpenInEditor` doesn't yet exist on the wails surface, the file-mention click degrades to `navigator.clipboard.writeText(path)` per FR-007 and we annotate that path explicitly without adding a new RPC under this mission (`C-001`).

## 2. Architecture

### 2.1 `MarkdownBlock.vue` — consolidated rendering entry point (FR-001)

New file: `frontend/src/components/rendering/MarkdownBlock.vue`.

- Props: `text: string`, `streaming?: boolean`, `messageId?: string`, `extensions?: MarkdownExtensions` (defaults to settings value via injection).
- Replaces the inline marked+DOMPurify pipeline currently in `StreamingText.vue`. `StreamingText.vue` is reduced to a thin shim that delegates to `MarkdownBlock` and keeps the streaming caret + role wrapper. Other callers (`ArtifactPreview.vue`, `ContextPreview.vue`, `ResolvedContextPanel.vue`, `ContextsView.vue`, `NewSessionDialog.vue`, `AttachmentTreePicker.vue`) continue to use their existing direct `marked` calls only where the surface is non-chat (preview panels). The mission deliberately scopes the consolidation to the **chat render path** to keep blast radius bounded; non-chat callers migrate opportunistically once the API stabilizes (`C-004`).
- Custom marked renderer hooks (via `marked.use({ renderer })`):
  - `code(token)` returns a placeholder `<div data-md-block="code" data-lang="..." data-source-b64="...">` that the Vue layer converts to `<CodeBlock>` post-mount via a lightweight tree-walk of the rendered HTML (we reconstitute by parsing data attributes, not by re-parsing markdown).
  - `link/text` walks for `@filename` and `@artifact:<id>` and emits `<span data-md-mention="...">` placeholders → `<FileMentionChip>`.
  - Math segments are pre-tokenized **before** `marked.parse` runs (a custom marked extension with `level: 'inline'` and `level: 'block'` for `$...$` and `$$...$$`) so KaTeX gets fed the raw TeX before HTML escaping.
- Pipeline: `tokenize math/mention → marked.parse(GFM) → DOMPurify.sanitize → mount Vue placeholders → progressive lazy upgrade for <pre>/code-fence/mermaid`.
- Vue placeholders use `<Teleport>`-free portals: a `ref` on the mounted `<div v-html>` runs a `MutationObserver`-backed walker (or a single `nextTick` walk) that swaps placeholder elements for live Vue components via `createApp(Component, props).mount(host)`. We pick the simpler `nextTick` walk since `marked.parse` is synchronous — `MutationObserver` is overkill here.

### 2.2 Code-block header bar (FR-002, Q1=A)

New: `frontend/src/components/rendering/CodeBlock.vue`.

- Header bar: language pill, Copy button (clipboard.writeText with checkmark flash), Save-as-Artifact button.
- Save flow (Q1=A — silent + toast + undo):
  1. Compute title = `${lang}-${sha8(content)}.${extForLang(lang)}` (ext map: `ts→ts, tsx→tsx, js→js, py→py, go→go, sh→sh, json→json, yaml→yaml, md→md, *fallback→txt`).
  2. Call `client.sessions.saveAsArtifact(sessionId, messageId, title, rangeStart, rangeEnd)`. The **range** is the byte offset of this code block within the assistant message (computed from the marked tokenizer pass — we keep token offsets alongside placeholders).
  3. Push a `Saved as artifact: <title>` toast with an `Undo` link onto a new `useToastQueue` composable (5-second auto-dismiss; on Undo click the toast invokes `client.artifacts.delete(artifactId)`). Existing toast precedent: `CapHitToast.vue`, `MergeSuggestionToast.vue` — but both are scoped one-shot; we add a thin shared queue.
- Failure mode: any RPC error pushes an error toast; no silent-drop.

### 2.3 Long code-block collapse (FR-003)

In `CodeBlock.vue`:

- Threshold: > 30 lines (count by `\n` in raw source).
- Collapsed state shows first 30 lines and a footer button: `Show N more lines` → `Show less` after expansion.
- State held locally on the component instance — **not persisted** (per FR-003).
- Collapse height computed from line height to avoid layout jump on toggle.

### 2.4 KaTeX integration (FR-004, FR-008)

New: `frontend/src/components/rendering/MathInline.vue` and `MathBlock.vue`.

- Lazy import: `const katex = await import('katex'); await import('katex/dist/katex.min.css');`.
- A `useKatex()` composable caches the import promise so subsequent blocks reuse it.
- Custom marked extension matches `$...$` (inline) and `$$...$$` (block). Streaming guard: only emit math token when the closing `$`/`$$` is present in the buffer. Mid-stream `\$x^2` (no close yet) renders as plain text — when the close arrives the next reactive recompute promotes it.
- KaTeX `throwOnError: false`, `output: 'html'`, `trust: false` (no `\href`, no `\includegraphics`). Output passes through DOMPurify with the KaTeX class allowlist appended.
- `extensions === 'basic' || 'diagrams'` skips math entirely (raw text passthrough).

### 2.5 Mermaid diagrams (FR-005, Q3=B)

New: `frontend/src/components/rendering/MermaidDiagram.vue`.

- Triggered when `lang === 'mermaid'` in a code fence — `CodeBlock.vue` short-circuits to render `<MermaidDiagram>` with the source instead of the standard fence.
- Lazy import via `useMermaid()`. On first call: `mermaid.initialize({ startOnLoad: false, securityLevel: 'strict', theme })` where `theme = harnessTheme.value === 'dark' ? 'dark' : 'default'`.
- Theme reactivity: when the harness theme changes (via Settings), `MermaidDiagram` reruns `mermaid.render()` for visible diagrams. We watch the same `theme` ref the rest of the app uses (`Settings.theme` projected through the existing theme application surface).
- Per-diagram `%%{init: ...}%%` directives are passed through unchanged to Mermaid; user overrides win because Mermaid's parser applies them after our initialize call. Documented inline in the component header comment.
- Render error: catch the rejection from `mermaid.render` and fall back to the raw code block + a small `Mermaid render failed: <err>` warning chip (using `--ink-muted`).
- `extensions === 'basic' || 'math'` skips mermaid (renders as plain code fence).

### 2.6 Tables (FR-006)

- The marked GFM table output is wrapped in a `<div class="md-table-wrap">` whose CSS sets `overflow-x: auto`. Existing borders/header colors come from `--surface-2`, `--border-muted`, `--ink-muted` tokens already in `StreamingText.vue` — moved into a shared scoped style on `MarkdownBlock.vue`. Alternating-row rule added (`tbody tr:nth-child(even) { background: var(--surface-2-alt); }`) — the `--surface-2-alt` token is added if missing in `tokens.css`; we verify with `npm run check:css-tokens`.

### 2.7 `@filename` / `@artifact:` chips (FR-007, Q2=A)

New: `frontend/src/components/rendering/FileMentionChip.vue`.

- Regex: `/(^|\s)@(?:artifact:[\w.-]+|[\w./~-]+\.[A-Za-z0-9]{1,8}|[\w./~-]+\/[\w./~-]+)/g` — anchored at word boundaries to avoid email-like false positives. The `@artifact:<id>` form is matched first.
- Resolution path:
  - `@artifact:<id>` → look up via `client.artifacts.get(id)`. Success → chip click opens `ArtifactPreview` (emit upward via the existing `open-artifact` event channel on `MessageBubble`). Missing → disabled chip + `artifact not found` tooltip.
  - `@<path>` → call `client.fs.exists(path)` if the API exists; **if it does not exist on the surface**, treat the chip as best-effort — render enabled and let `Tools.OpenInEditor` (or clipboard fallback) decide success at click time. (Since FR-007 references `Tools.OpenInEditor` "if available", the existence check is opportunistic.) Click action: invoke `Tools.OpenInEditor`; on absence/error, clipboard-write the path and toast `Path copied to clipboard`.
  - Q2=A: on a confirmed-missing target, the chip shows the `file not found` tooltip on hover and click is a no-op.
- Visual: pill with `--surface-2` background, `--border-muted` border, leading file icon (lucide `File` / `Database` for artifacts).

### 2.8 Streaming-safe rendering (FR-008)

- Math tokens only emit when the closing delimiter has arrived (extension guards on remaining buffer scan).
- Mermaid render is debounced 120ms while `streaming === true` so we don't re-run the full SVG generation on each token. Final-render runs once when `streaming` flips to false.
- Code-block source streams char-by-char; the header bar Copy / Save buttons are disabled while `streaming === true` (greyed via `opacity-50 pointer-events-none`).
- Sanitization runs on every reactive `html` recompute, same cadence as today's `StreamingText.vue`.

### 2.9 DOMPurify sanitization (FR-009, C-002)

- Single `sanitize(rawHtml, profile)` helper in `frontend/src/lib/markdown/sanitize.ts`. Profile adds `ADD_ATTR: ['target', 'rel', 'data-md-block', 'data-md-mention', 'data-lang', 'data-source-b64', 'class']`, retains DOMPurify's default tag allowlist (no `<script>`, no `<iframe>`, no event handlers).
- KaTeX HTML is sanitized with an extended profile that allows `span.katex*` classes; SVG produced by Mermaid is sanitized with `USE_PROFILES: { svg: true, svgFilters: true }` — Mermaid's `securityLevel: 'strict'` already removes script tags but DOMPurify is the load-bearing guarantee.
- Code execution risk: `data-source-b64` is base64-encoded for round-trip integrity (avoid HTML-encoding edge cases). Code blocks NEVER execute — copy/save just propagate the bytes.

### 2.10 `Settings.MarkdownExtensions` enum (FR-010)

- `frontend/src/lib/types.ts`: extend `Settings` with `markdownExtensions?: MarkdownExtensions` where `type MarkdownExtensions = 'basic' | 'math' | 'diagrams' | 'all'`. Empty → `'all'` (default).
- Backend `core/config` settings store schema gets the new key as a string with the same enum (Go side: a small typed alias + JSON marshalling; not strictly required since the field is opaque to the core, but we follow the precedent set by `compactionAggressiveness`).
- `SettingsView.vue` adds a four-radio control under a new "Rendering" sub-section. Help text: "Disable on slow machines if heavy diagrams or math feel laggy."
- The `MarkdownBlock` component reads the value via a Vue `inject` key `mdExtensionsKey` provided at the App root; tests pass it directly via `provide`.

### 2.11 Bundle-size considerations (NFR-002, C-003)

- `katex` and `mermaid` imported via dynamic `import()` so Vite splits them into async chunks. Each renderer component holds the import promise as a module-level singleton.
- KaTeX CSS is dynamically imported alongside the JS — Vite handles the CSS chunk wiring.
- `npm run check:bundle-size` thresholds in `scripts/ci/check-bundle-size.mjs` updated if needed (the script already gates the chat surface; we add per-chunk caps for the new async bundles: katex ≤ 90 KiB compressed, mermaid ≤ 110 KiB compressed). If thresholds aren't already structured per-chunk, add the harness for them under this mission.

## 3. Risk register

| ID | Risk | Mitigation |
|---|---|---|
| R1 | DOMPurify config too tight strips KaTeX/Mermaid output (math vanishes). | Profile-per-renderer; Vitest tests assert KaTeX `<span class="katex">` survives sanitization and Mermaid `<svg>` survives. |
| R2 | DOMPurify config too loose lets Mermaid SVG `<foreignObject>` smuggle scripts. | Mermaid `securityLevel: 'strict'` + DOMPurify `FORBID_TAGS: ['foreignObject', 'script']` even in SVG profile. |
| R3 | Mermaid bundle blows past NFR-002. | Lazy chunk; bundle-size CI threshold gates regressions; if Mermaid trips the cap, we trim by importing only `mermaid/dist/mermaid.esm.mjs` (tree-shakable) and excluding rarely-used diagram subtypes via Mermaid's `loadConfig`. |
| R4 | KaTeX edge cases (`\href`, raw HTML) execute. | `trust: false`; sanitize after render. |
| R5 | Streaming math flickers when closing `$` arrives. | Render plain `$x^2` until close; transition is non-animated, single tick — measured imperceptible. |
| R6 | Custom marked renderer hooks break GFM tables / nested lists. | Use `marked.use({ extensions })` instead of overriding `renderer` wholesale; extensions are additive. Add Vitest fixtures covering nested list + table + math + code mix. |
| R7 | Per-message Vue placeholder mounts leak component instances on rerender. | Track mounted apps in a `WeakMap` keyed by host element; unmount on `onBeforeUnmount`. |
| R8 | `Tools.OpenInEditor` not yet on the RPC surface — file chips degrade silently. | Documented degradation path (clipboard write + toast); unit-test both branches via mocked client. |
| R9 | Long-message renders (NFR-001 threshold 100 ms post-stream) regress with diagrams. | Vitest-bench (existing pattern) with a 200-line/5-block/1-diagram fixture; CI fails on regression. |
| R10 | Consolidation breaks non-chat markdown previews. | Scope the v1 consolidation to chat (`StreamingText`); leave preview panels on their existing inline pipeline; track follow-up under a separate refactor mission. |

## 4. Rollout

1. **Land behind a feature flag** is **not** required — the new `Settings.MarkdownExtensions` enum is itself the safety valve. Default `all`.
2. **Migration step**: `StreamingText.vue` becomes a delegating shim — public props unchanged. Single PR; no caller migration needed.
3. **Backfill tests** for FR-001 through FR-011 (Vitest) before merge — at least one per FR (the spec mandates this).
4. **Manual smoke** (post-merge):
   - Open a chat, paste a fixture message containing a Python code fence (40 lines), inline `$E=mc^2$`, block `$$\int_0^1 x^2 dx$$`, a `mermaid` flowchart, a GFM table, a `@frontend/src/main.ts` mention, and an `@artifact:abc123` mention.
   - Verify code-block header (lang pill, Copy → clipboard, Save → toast with Undo). Click Undo → artifact is removed.
   - Click "Show 10 more lines" → expansion. Click "Show less" → collapse.
   - Toggle theme dark↔light → Mermaid re-renders. Insert `%%{init:{'theme':'forest'}}%%` line → diagram uses forest.
   - Settings → Rendering → set "basic" → reload chat → math/mermaid render as plain code/text. Restore "all".
   - Click a `@frontend/src/main.ts` chip → editor opens (or clipboard fallback toast). Edit fixture to reference a missing path → chip disabled with `file not found` tooltip.
5. **Telemetry** (out of scope — no new metrics this mission).
6. **Rollback**: revert the squash-merge commit; settings field becomes inert (existing `Settings` struct ignores unknown keys gracefully on the backend).

## 5. Out of scope (per spec §8 + WP scope guard)

- Migrating non-chat markdown surfaces (artifact preview, context preview) to `MarkdownBlock`. Track separately.
- WYSIWYG composer rendering, footnotes / definition lists, real-time collab.
- New backend RPCs for `Tools.OpenInEditor` if not already present.
