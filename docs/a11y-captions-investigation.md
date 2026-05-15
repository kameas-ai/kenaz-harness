# A11y Captions Investigation — WP06

**Mission:** a11y-backlog-cleanup-01NDFSEX07  
**Deferral ID:** D-06  
**Date:** 2026-05-15  
**Wails version:** 2.x (current in wails.json)  
**WKWebView platform:** macOS / iOS (Wails desktop shell)

---

## Summary

The WKWebView renderer used by Wails on macOS is based on WebKit. Apple's
WebKit has supported the `<track>` element and WebVTT caption rendering since
Safari 6 (2012). The HTML5 specification requires conforming browsers to
implement `<track kind="captions">` for `<audio>` and `<video>` elements.

However, whether WKWebView's embedded renderer in a Wails desktop app exposes
the native captions track UI (the "CC" toggle) to users **has not been
directly verified in this codebase**. The investigation is constrained to
documentation analysis; no live Wails build was run as part of this audit.

---

## Apple WebKit / WKWebView WebVTT documentation

Per Apple's [WebKit Blog](https://webkit.org/blog/3837/web-platform-status/)
and the [HTML specification for the track element](https://html.spec.whatwg.org/multipage/media.html#the-track-element):

- WebKit has shipped WebVTT caption support in Safari since 6.0.
- `<track kind="captions" src="..." default>` renders closed captions in the
  native video controls when a `.vtt` file is supplied.
- WKWebView (used by macOS apps via `WebView` / `WKWebView`) inherits the
  same rendering engine and supports `<track>`.

**Unverified gap:** Whether the Wails WKWebView frame exposes the media
controls' captions menu in its default configuration, or whether the system
level audio/video controls UI is suppressed by the webview host settings.

---

## Proposed renderer-side caption discovery logic

If WKWebView caption support is confirmed by a human tester, the following
implementation should be applied to `AudioRenderer.vue` and `VideoRenderer.vue`:

```vue
<!-- In AudioRenderer.vue / VideoRenderer.vue template -->
<audio controls :src="sourceUrl" class="w-full">
  <!-- Caption discovery: emit <track> when a companion .vtt exists -->
  <track
    v-if="captionUrl"
    kind="captions"
    :src="captionUrl"
    default
  />
</audio>
```

The `captionUrl` would be computed by a new `useCaptionDiscovery` composable:

```typescript
// frontend/src/views/artifacts/preview/useCaptionDiscovery.ts
import { computed, type Ref } from 'vue';
import type { Artifact } from '@/lib/types';

/**
 * Derives the companion WebVTT caption URL for an audio/video artifact.
 *
 * Discovery rule: if a sibling artifact in the same scope has the same
 * base filename but with a `.vtt` extension, treat it as the caption track.
 *
 * The harness client would need a new method:
 *   client.artifacts.findCompanionCaption(artifactId) → string | null
 *
 * This is a proposed API; it does not exist yet.
 */
export function useCaptionDiscovery(artifact: Ref<Artifact | null>) {
  // Placeholder: real implementation depends on harness API extension.
  const captionUrl = computed<string | null>(() => null);
  return { captionUrl };
}
```

**Prerequisite for shipping:** The harness RPC layer needs a
`Artifacts_FindCompanionCaption(artifactId string) (string, error)` method
so the renderer can query for a companion `.vtt` file without scanning all
artifacts on the frontend.

---

## Decision: DEFERRED

Full deferral documentation: see `docs/a11y-captions-deferral.md`.

Criteria for revisiting:
1. Human verification of WKWebView caption UI in a live Wails dev build.
2. Harness RPC extension for companion `.vtt` discovery.
3. Fixture test: a `.vtt` file paired with an audio/video artifact renders
   the captions overlay in the dev build.
