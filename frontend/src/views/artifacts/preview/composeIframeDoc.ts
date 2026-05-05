/**
 * composeIframeDoc.ts — pure string builder for sandboxed iframe documents
 * (artifact-preview-binary-rendering-01KQ8TD5 WP04).
 *
 * The function synthesises the outer HTML document and injects artifact HTML
 * into the body WITHOUT parsing the artifact content. This avoids the class
 * of injection where adversarial artifact HTML contains `</head>`, `<base>`,
 * or `</meta>` sequences that could escape the body context.
 *
 * CSP injected per FR-006:
 *   default-src 'none';
 *   style-src 'self' 'unsafe-inline';
 *   img-src 'self' data:;
 *   font-src 'self' data:;
 *   base-uri 'none';
 *   form-action 'none';
 *   frame-ancestors 'none'
 *
 * The `sandbox="allow-same-origin"` attribute on the outer iframe element
 * (set by IframeSandbox.vue) lets inline CSS work via `style-src 'self'`
 * while CSP `default-src 'none'` blocks all network. Scripts are blocked
 * both by the missing `allow-scripts` sandbox flag AND by CSP
 * `default-src 'none'` (belt-and-braces).
 */

const CSP =
  "default-src 'none'; style-src 'self' 'unsafe-inline'; img-src 'self' data:; font-src 'self' data:; base-uri 'none'; form-action 'none'; frame-ancestors 'none'";

/**
 * Returns a complete HTML document string with the FR-006 CSP injected and
 * `artifactHtml` placed inside `<body>`. The outer shell is constructed from
 * string literals; `artifactHtml` is never parsed at this stage so rogue tags
 * cannot affect the outer structure.
 */
export function composeIframeDoc(artifactHtml: string): string {
  return (
    `<!doctype html><html><head>` +
    `<meta http-equiv="Content-Security-Policy" content="${CSP}">` +
    `<base target="_blank">` +
    `</head><body>` +
    artifactHtml +
    `</body></html>`
  );
}

/** Exported for tests. */
export { CSP as IFRAME_CSP };
