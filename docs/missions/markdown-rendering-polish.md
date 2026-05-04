# Markdown rendering polish (`markdown-rendering-polish-01KQ8TDT`)

Mission status: shipped in `v0.3.0` beta.

## Surface

Every chat markdown render path goes through `MarkdownBlock.vue`
(`frontend/src/components/rendering/MarkdownBlock.vue`).
`StreamingText.vue` is a thin shim that delegates to `MarkdownBlock`
and overlays the streaming caret — public props unchanged.

Pipeline (per-render, synchronous through marked, async only for
the heavy renderers):

1. `marked.use(...)` configures GFM + a math tokenizer extension and
   a code-fence renderer that emits placeholder `<div>`s with
   base64-encoded source bytes.
2. `marked.parse` produces raw HTML.
3. `sanitize(...)` (`frontend/src/lib/markdown/sanitize.ts`) runs
   DOMPurify with one of three profiles — `default`, `katex`,
   `mermaid-svg`.
4. The sanitized HTML mounts via `v-html` into `<div class="md-body">`.
5. A post-mount walker swaps placeholder elements for live Vue
   components: `CodeBlock`, `MermaidDiagram`, `MathInline/MathBlock`,
   `FilenameChip`, plus inline artifact mention buttons.

## Settings dial

`Settings.markdownExtensions: 'basic' | 'math' | 'diagrams' | 'all'`
(default `all`). The Settings view exposes a four-stop radio under
the "Rendering" section. The choice persists through
`client.settings.set` and propagates to mounted `MarkdownBlock`s via
the `markdownExtensionsRef` singleton + Vue `provide/inject` keyed by
`MD_EXTENSIONS_KEY`.

`basic` skips both KaTeX and Mermaid; `math` and `diagrams` skip the
other; `all` enables everything.

## Lazy renderers

`katex` and `mermaid` are imported with dynamic `import()` only when
the corresponding content appears in a message. Vite splits each into
its own async chunk; the synchronous chat bundle delta stays small.

## Toast queue

`useToastQueue()` (`frontend/src/composables/useToastQueue.ts`) is a
singleton reactive queue with auto-dismiss and Undo support. The
`ToastRoot.vue` mounted in `App.vue` renders the stack and listens
for the `md-toast` `CustomEvent` on `document`, so emitters mounted
outside the Vue tree (`createApp(...).mount(host)`) can still push
toasts.

## File and artifact mentions

Regex match runs on text nodes after sanitization (skipping `<pre>`,
`<code>`, `<a>`, and existing placeholders). `@filename` mentions
become `FilenameChip.vue` instances; `@artifact:<id>` mentions become
inline buttons that dispatch a bubbling `md-open-artifact`
`CustomEvent` for the host MessageBubble to handle.

`FilenameChip` is optimistic: it renders enabled and probes
`client.fs.exists` in the background; a definitive `false` flips the
chip to disabled with a `file not found` tooltip. Click invokes
`client.tools.openInEditor` if present, otherwise falls back to
`navigator.clipboard.writeText(path)` plus a "Path copied to
clipboard" toast.

## Tables

GFM tables are wrapped in `<div class="md-table-wrap">` with
`overflow-x: auto`, and `tbody tr:nth-child(even)` gets a
`var(--surface-2)` background for alternating rows.

## Test coverage

- `MarkdownBlock.spec.ts` — GFM, sanitization, code placeholder,
  collapse, math, table wrap, extensions gate, StreamingText shim.
- `MarkdownBlock.katex.spec.ts` — KaTeX integration.
- `MarkdownBlock.mentions.spec.ts` — `@filename` and `@artifact:`.
- `MarkdownBlock.coverage.spec.ts` — full-fixture sweep + soft perf
  bench.
- `FilenameChip.spec.ts` — optimistic render, missing-target disable,
  clipboard fallback.
- `useToastQueue.spec.ts` — push, dismiss, auto-dismiss, undo.
- `sanitize` cases inside `MarkdownBlock.spec.ts`.

## Known gaps deferred past v0.3.0

- Non-chat surfaces (artifact preview, context preview,
  `NewSessionDialog`) still call `marked` directly. Migrating them
  through `MarkdownBlock` is tracked under a separate refactor
  mission.
- A `Tools.OpenInEditor` JSON-RPC route is not yet on the wails
  surface; `FilenameChip` already handles its absence by falling back
  to the clipboard.
- The `check:bundle-size` per-chunk caps for the `katex` and
  `mermaid` async chunks are not yet enforced in CI; the dynamic
  imports are confirmed but the threshold script still gates only
  the chat surface aggregate.
