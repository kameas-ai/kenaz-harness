# A11y Captions Deferral — D-06

**Mission:** a11y-backlog-cleanup-01NDFSEX07 (v0.16.1)  
**Deferral ID:** D-06  
**Status:** deferred-post-1.0  
**Date:** 2026-05-15

---

## What was deferred

`<audio controls>` and `<video controls preload="metadata">` renderers in
`frontend/src/views/artifacts/preview/renderers/` do not have a
`<track kind="captions">` element. WCAG 1.2.2 (Captions — Prerecorded, Level AA)
requires captions for prerecorded audio in synchronized media.

---

## Why deferred

1. **WKWebView caption UI not verified in live Wails build.** Apple WebKit
   supports the `<track>` element and WebVTT per specification, but whether
   the Wails WKWebView host exposes the native captions control to users in
   a packaged Wails app has not been confirmed with a hands-on build.
   Without this confirmation, shipping a `<track>` element that silently
   fails to render would be worse than deferring.

2. **No companion `.vtt` discovery API in the harness.** The harness RPC
   layer has no method to locate a companion `.vtt` caption file for a given
   audio/video artifact. A full solution requires:
   - A new `Artifacts_FindCompanionCaption(artifactId) (string, error)` RPC
     method in the Go backend.
   - A corresponding frontend composable `useCaptionDiscovery`.
   - Integration tests with a fixture `.vtt` + audio/video artifact pair.

3. **Content-authoring concern.** End users create artifacts by prompting the
   AI; the AI does not produce `.vtt` files today. Without content-authoring
   support, the caption infrastructure would have no practical use even if
   technically shipped.

---

## Wails / WKWebView version at time of deferral

- **Wails version:** see `wails.json` (v2.x)
- **Go module pinned version:** see `go.mod`
- **WKWebView:** macOS system-provided (inherits macOS version at runtime)

---

## Criteria for revisiting

The deferral should be reconsidered when **all three** of the following are met:

1. **Human smoke test:** A developer runs `wails dev` and opens a test
   `<audio>` or `<video>` element with `<track kind="captions" src="test.vtt" default>`
   in the Wails renderer. The captions overlay (CC button) appears in the
   native audio/video controls and `.vtt` text renders correctly.

2. **RPC extension:** `Artifacts_FindCompanionCaption` is implemented in
   `core/rpc/` and exposed via the Wails bindings.

3. **Content pipeline:** The harness can produce or accept `.vtt` files as
   companion artifacts to audio/video outputs, or the user can upload a `.vtt`
   alongside a media artifact.

---

## Proposed implementation (ready for when criteria are met)

See `docs/a11y-captions-investigation.md` §"Proposed renderer-side caption
discovery logic" for the `<track>` markup and `useCaptionDiscovery` composable
design.

Files to modify when shipping:
- `frontend/src/views/artifacts/preview/renderers/AudioRenderer.vue`
- `frontend/src/views/artifacts/preview/renderers/VideoRenderer.vue`
- `frontend/src/views/artifacts/preview/useCaptionDiscovery.ts` (new)
- `core/rpc/` — new RPC method + Wails binding
- `frontend/src/lib/harnessClient.ts` + `types.ts` — client extension

---

## Audit report reference

`docs/a11y-audit-2026-05-15.md` §6, row D-06: status updated to
`deferred-post-1.0` in v0.16.1.
