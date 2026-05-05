/**
 * detectHtml.ts — cheap regex detector for embedded HTML inside a
 * markdown source string (artifact-preview-binary-rendering-01KQ8TD5 FR-007b).
 *
 * Purpose: route markdown-with-HTML through the sandboxed iframe path
 * rather than the inline prose renderer. Detection is a *performance* gate,
 * not a security gate — the iframe path is always safe; worst case is an
 * unnecessary iframe for a false positive.
 *
 * Algorithm: presence of `<letter` after trimming is treated as HTML.
 * Matches standard HTML tags (`<div`, `<details`, `<img`, etc.) but not
 * markdown-escaped angle brackets that don't start a tag.
 */

/**
 * Returns true when `source` contains at least one HTML open-tag sequence
 * (`<letter...>`). Case-insensitive.
 */
export function containsHtml(source: string): boolean {
  return /<[a-zA-Z][a-zA-Z0-9-]*\b[^>]*>/.test(source);
}

/**
 * Module-scoped cache keyed by `contentHash`. Lives for the lifetime of
 * the page; sized by the number of distinct artifacts ever opened in this
 * session (typically small).
 */
const _cache = new Map<string, boolean>();

/**
 * Cached variant: given a stable `contentHash` + source string, returns
 * the detection result and caches it for future calls with the same hash.
 */
export function containsHtmlCached(contentHash: string, source: string): boolean {
  if (_cache.has(contentHash)) {
    return _cache.get(contentHash)!;
  }
  const result = containsHtml(source);
  _cache.set(contentHash, result);
  return result;
}

/** Exposed for tests: clear the module-level cache. */
export function clearDetectHtmlCache(): void {
  _cache.clear();
}
