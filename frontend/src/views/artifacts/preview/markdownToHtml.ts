/**
 * markdownToHtml.ts — synchronous marked adapter for converting markdown to
 * HTML before handing it to IframeSandbox (WP05).
 *
 * DOMPurify runs as defence-in-depth before iframe handoff. The iframe's own
 * CSP + sandbox attribute are the primary security boundary; DOMPurify is
 * an additional layer.
 *
 * Why synchronous? The `composeIframeDoc` path runs in a computed property
 * inside a Vue SFC. Async would require a watcher + ref; the current approach
 * keeps the component simple. marked() with `async: false` (the default) is
 * synchronous.
 */

import { marked } from 'marked';
import DOMPurify from 'dompurify';

/**
 * Convert markdown source to a sanitized HTML string.
 * The result is safe to embed inside an `IframeSandbox` srcdoc document.
 */
export function markdownToHtml(source: string): string {
  // marked() with default options is synchronous (returns string when async
  // option is not set). Cast to string because the overloaded return type
  // includes Promise<string> for the async variant.
  const raw = marked(source) as string;
  // DOMPurify defence-in-depth before iframe handoff. The iframe CSP is the
  // primary guard; DOMPurify catches patterns DOMPurify knows but CSP misses.
  if (typeof window !== 'undefined' && typeof DOMPurify.sanitize === 'function') {
    return DOMPurify.sanitize(raw, {
      // Allow a generous set of HTML elements that appear in typical markdown
      // output; still blocks script, event handlers, data: hrefs.
      USE_PROFILES: { html: true },
    });
  }
  // Fallback for non-browser environments (SSR / test with jsdom without DOM).
  return raw;
}
