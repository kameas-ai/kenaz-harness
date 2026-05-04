# Spec — Markdown / rendering polish (`markdown-rendering-polish-01KQ8TDT`)

**Status**: draft · **Owner**: alecfeeman

## 1. Why

The harness today renders markdown in chat messages but lacks the polish that makes responses pleasant to read and act on:
- Code blocks lack copy / save-as-artifact buttons.
- LaTeX math (`$x^2$`) renders as plain text.
- Mermaid diagrams render as plain text.
- Tables may be ugly or broken.
- Long code blocks lack collapse/expand.
- File-mention syntax (`@filename`) doesn't link to anything.

These are basics for a developer-flavored AI tool.

## 2. Goals

- Code blocks get a header bar: language tag + copy button + "Save as artifact" button.
- LaTeX math (inline `$...$` and block `$$...$$`) renders via KaTeX.
- Mermaid code blocks (` ```mermaid `) render as SVG diagrams.
- Tables match the design tokens; horizontal scroll on overflow.
- Long code blocks (> 30 lines) collapse with "Show more" / "Show less".
- `@filename` references in messages render as clickable chips that open the file in an external editor (via `Tools.OpenInEditor` if available) or the artifact preview if it's an artifact id.
- All rendering goes through the same shared component so streaming + final-render are visually consistent.

## 3. Functional requirements

| ID | Requirement | Status |
|---|---|---|
| FR-001 | New `frontend/src/components/rendering/MarkdownBlock.vue` consolidates the existing per-component markdown rendering. Single entry point for streaming partial markdown + final render. | proposed |
| FR-002 | Code block header: language tag + Copy button + Save-as-Artifact button. Save uses `kaneaz__save_artifact`-equivalent backend path (or the artifact pipeline directly). Title defaults to `<language>-<sha8>.<ext>`. | proposed |
| FR-003 | Long code blocks collapse: when > 30 lines, render first 30 + "Show N more lines" footer. Clicking expands inline. State per-message, not persisted. | proposed |
| FR-004 | LaTeX math: integrate KaTeX (already a permissively-licensed library). Inline `$...$` for inline math; `$$...$$` for block math. Streaming renders progressively (don't render incomplete `$...` until the closing `$` arrives). | proposed |
| FR-005 | Mermaid diagrams: ` ```mermaid ` blocks render via the `mermaid` library (small bundle; tree-shakable). On render error, fall back to the raw code block + warning chip. | proposed |
| FR-006 | Tables: respect the existing design tokens for borders, header background, alternating rows. Wrap in a horizontal-scroll container when content overflows. | proposed |
| FR-007 | `@filename` syntax: regex matches `@<path>` where path is a recognizable file path or starts with `@artifact:<id>`. Renders as a clickable chip. Click action: file path → invoke `Tools.OpenInEditor` (if available; gracefully degrade to `navigator.clipboard.writeText(path)`); artifact ref → open artifact preview panel. | proposed |
| FR-008 | Streaming-safe: partial content doesn't break rendering. KaTeX / Mermaid only render when their delimiters close; until then, raw text shown. | proposed |
| FR-009 | Sanitization: every rendered markdown passes through DOMPurify (or equivalent already in the bundle) before insertion. Code blocks and KaTeX content are NOT executable. | proposed |
| FR-010 | Settings dial `Settings.MarkdownExtensions` enum: `"basic" | "math" | "diagrams" | "all"` (default "all"). Lets users disable heavier renderers (KaTeX/Mermaid) for performance on slow machines. | proposed |
| FR-011 | Vitest test coverage: at least one test per FR (code-block actions, LaTeX render, Mermaid render, table styling, file-mention chip, sanitization). | proposed |

## 4. Non-functional requirements

| ID | Requirement | Threshold |
|---|---|---|
| NFR-001 | Render latency for a 200-line message with 5 code blocks + 1 diagram | < 100ms (post-stream-complete) |
| NFR-002 | Bundle size impact | < 200 KiB compressed (KaTeX + Mermaid lazy-loaded) |
| NFR-003 | Streaming-render: partial markdown updates within one frame |  |

## 5. Constraints

| ID | Constraint |
|---|---|
| C-001 | DIRECTIVE_001: rendering is purely frontend; no new backend dependencies. |
| C-002 | All rendering safe (sanitized); no model-injected scripts ever execute. |
| C-003 | KaTeX and Mermaid are lazy-loaded — chat surface still loads fast on cold start. |
| C-004 | Existing markdown rendering callers refactored to use `MarkdownBlock.vue` rather than replicating logic. |

## 6. Locked open questions

- **Q1 = A**: Code-block "Save as artifact" saves silently and surfaces a toast `Saved as artifact: <title>` with a 5-second `Undo` link. Low-friction; high-frequency action shouldn't gate behind a modal. Title auto-derives from `<language>-<sha8>.<ext>`; user can rename via the artifact panel afterward.
- **Q2 = A**: File-mention chip with a missing target shows a "file not found" tooltip on hover; click is a no-op. Honest — chip explains why it can't navigate without dragging the user into a side path. (No fuzzy-search-similar-paths affordance in v1; cross-project file index isn't in scope here.)
- **Q3 = B**: Mermaid theme auto-picks from the current harness theme via Mermaid's `theme` config (`default` for light, `dark` for dark mode). Per-diagram `%%{init: {'theme': '...'}}%%` overrides work for free via Mermaid's existing parser — documented as a power-user escape hatch.

## 7. Success criteria

- A model response with a code block, an inline LaTeX equation, a Mermaid flowchart, and a table renders all four correctly.
- Code-block "Save as artifact" produces an artifact row in the artifact panel.
- Disabling math via Settings reverts LaTeX to plain text.

## 8. Out of scope

- WYSIWYG editing of markdown in the composer.
- Custom markdown extensions (footnotes, definition lists, etc. beyond GFM).
- Real-time collaborative editing.
