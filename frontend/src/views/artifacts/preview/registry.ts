/**
 * registry.ts — renderer registry for the artifact binary-preview feature
 * (artifact-preview-binary-rendering-01KQ8TD5 WP01, FR-001).
 *
 * `pickRenderer(mimeType, source, contentHash)` returns the first matching
 * RendererSpec. Order is defined by the spec:
 *
 *  1. image/*                → ImageRenderer
 *  2. application/pdf        → PdfRenderer
 *  3. audio/*                → AudioRenderer
 *  4. video/*                → VideoRenderer
 *  5. text/html              → HtmlRenderer (always iframed)
 *  6. text/markdown + HTML   → MarkdownIframedRenderer
 *  7. text/markdown          → MarkdownInlineRenderer
 *  8. text/*                 → TextRenderer
 *  9. text-like app/* mimes  → TextRenderer
 * 10. fallback               → UnknownBinaryRenderer
 */

import type { Component } from 'vue';
import type { RendererKind, RendererSpec } from './types';
import { containsHtmlCached } from './detectHtml';

import ImageRenderer from './renderers/ImageRenderer.vue';
import PdfRenderer from './renderers/PdfRenderer.vue';
import AudioRenderer from './renderers/AudioRenderer.vue';
import VideoRenderer from './renderers/VideoRenderer.vue';
import HtmlRenderer from './renderers/HtmlRenderer.vue';
import MarkdownInlineRenderer from './renderers/MarkdownInlineRenderer.vue';
import MarkdownIframedRenderer from './renderers/MarkdownIframedRenderer.vue';
import TextRenderer from './renderers/TextRenderer.vue';
import UnknownBinaryRenderer from './renderers/UnknownBinaryRenderer.vue';

function spec(kind: RendererKind, component: Component, enforceCap = true): RendererSpec {
  return { kind, component, enforceCap };
}

/**
 * Returns the RendererSpec for the given mime type and (optional) source
 * string. `contentHash` is used as the detection cache key.
 */
export function pickRenderer(
  mimeType: string,
  source: string,
  contentHash = '',
): RendererSpec {
  const m = (mimeType ?? '').toLowerCase().split(';')[0].trim();

  // 1. Images
  if (m.startsWith('image/')) {
    return spec('image', ImageRenderer);
  }

  // 2. PDF
  if (m === 'application/pdf') {
    return spec('pdf', PdfRenderer);
  }

  // 3. Audio
  if (m.startsWith('audio/')) {
    return spec('audio', AudioRenderer);
  }

  // 4. Video
  if (m.startsWith('video/')) {
    return spec('video', VideoRenderer);
  }

  // 5. HTML — always iframed
  if (m === 'text/html') {
    return spec('html', HtmlRenderer, false);
  }

  // 6. Markdown with embedded HTML → iframed
  if (m === 'text/markdown' && source && containsHtmlCached(contentHash || source, source)) {
    return spec('markdown-iframed', MarkdownIframedRenderer, false);
  }

  // 7. Plain markdown → inline prose
  if (m === 'text/markdown') {
    return spec('markdown-inline', MarkdownInlineRenderer, false);
  }

  // 8–9. Text-like mimes (text/*, application/json, application/xml, etc.)
  if (isTextLikeMime(m)) {
    return spec('text', TextRenderer, false);
  }

  // 10. Fallback
  return spec('unknown-binary', UnknownBinaryRenderer, false);
}

/** Mirrors the logic in ArtifactPreview.vue's legacy isTextLikeMime helper. */
function isTextLikeMime(m: string): boolean {
  if (!m) return false;
  if (m.startsWith('text/')) return true;
  if (m === 'application/json' || m.endsWith('+json')) return true;
  if (m === 'application/xml' || m.endsWith('+xml')) return true;
  if (m === 'application/javascript' || m === 'application/ecmascript') return true;
  if (m === 'application/x-yaml' || m === 'application/yaml') return true;
  return false;
}
