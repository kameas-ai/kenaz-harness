# Spec: Artifact preview — binary rendering

**Status**: draft · **Owner**: alecfeeman

## 1. Why

`ArtifactPreview.vue` today renders text artifacts (markdown, code, JSON) but binaries (images, PDFs, audio, video) show only the chip with no preview surface. With multimodal generation landing (`multimodal-io-extended`), we need a real preview pane.

## 2. Goals

- Render images (png/jpg/webp/gif/svg) inline.
- Render PDFs via `<embed>` or PDF.js.
- Audio + video play inline via native HTML5 elements.
- Markdown renders with the existing `MessageBubble` markdown pipeline.
- HTML renders sandboxed via `<iframe sandbox>` with no JS by default.

## 3. Functional requirements

| ID | Requirement | Status |
|---|---|---|
| FR-001 | `ArtifactPreview.vue` adds a renderer registry keyed by mime prefix. | proposed |
| FR-002 | Image renderer: load via `media://<hash>` from CAS; max-height 80vh. | proposed |
| FR-003 | PDF renderer: native browser `<embed>` first; PDF.js polyfill for unsupported builds. | proposed |
| FR-004 | Audio renderer: `<audio controls>` with download affordance. | proposed |
| FR-005 | Video renderer: `<video controls>` with download affordance. | proposed |
| FR-006 | HTML renderer: `<iframe sandbox="allow-same-origin">` only — no scripts. Toggle to "open in browser" for full rendering. | proposed |
| FR-007 | Markdown / code / JSON / YAML use existing prose renderers. | proposed |
| FR-008 | Unknown binary types show file-size + "Open externally" affordance. | proposed |

## 4. Success criteria

- Every artifact mime type produced by the harness (image, PDF, audio, video, text, HTML) renders without leaving the artifact preview pane.
- Sandboxed HTML cannot execute scripts.
