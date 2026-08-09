/**
 * composeIframeDoc.test.ts — unit tests for the iframe document composer
 * (artifact-preview-binary-rendering-01KQ8TD5 WP04 acceptance).
 *
 * Key acceptance criteria:
 *   - Composed document starts with the exact CSP meta tag.
 *   - Adversarial corpus: artifact HTML containing </head>, </meta>, <base>,
 *     <script>, mixed quotes renders without hijacking the outer document.
 *   - The sandbox attribute itself is set on the consuming iframe element
 *     in IframeSandbox.vue (fully restrictive, `sandbox=""`), not here —
 *     see IframeSandbox.test.ts.
 */

import { describe, it, expect } from 'vitest';
import { composeIframeDoc, IFRAME_CSP } from '../composeIframeDoc';

const EXPECTED_CSP =
  "default-src 'none'; style-src 'self' 'unsafe-inline'; img-src 'self' data:; font-src 'self' data:; base-uri 'none'; form-action 'none'; frame-ancestors 'none'";

describe('composeIframeDoc', () => {
  it('starts with <!doctype html>', () => {
    const doc = composeIframeDoc('<p>hello</p>');
    expect(doc.startsWith('<!doctype html>')).toBe(true);
  });

  it('injects the CSP meta tag byte-exactly at the top of <head>', () => {
    const doc = composeIframeDoc('<p>test</p>');
    const cspMeta = `<meta http-equiv="Content-Security-Policy" content="${EXPECTED_CSP}">`;
    expect(doc).toContain(cspMeta);
    // CSP must appear before the body.
    const cspIdx = doc.indexOf(cspMeta);
    const bodyIdx = doc.indexOf('<body>');
    expect(cspIdx).toBeLessThan(bodyIdx);
  });

  it('exports the IFRAME_CSP constant matching the expected value', () => {
    expect(IFRAME_CSP).toBe(EXPECTED_CSP);
  });

  it('wraps artifact HTML inside <body>', () => {
    const artifactHtml = '<p>my artifact</p>';
    const doc = composeIframeDoc(artifactHtml);
    expect(doc).toContain(`<body>${artifactHtml}</body>`);
  });

  it('adversarial: </head> in artifact does not escape body', () => {
    const adversarial = '</head><script>alert(1)</script>';
    const doc = composeIframeDoc(adversarial);
    // The </head> is placed in <body> context; the outer </head> tag
    // was already closed by the shell.
    // Verify the body contains the adversarial content verbatim.
    expect(doc).toContain(`<body>${adversarial}</body>`);
    // Verify the outer head is still closed before body.
    const headCloseIdx = doc.indexOf('</head>');
    const bodyIdx = doc.indexOf('<body>');
    expect(headCloseIdx).toBeLessThan(bodyIdx);
  });

  it('adversarial: <base href> in artifact does not escape head', () => {
    const adversarial = '<base href="https://evil.example.com">';
    const doc = composeIframeDoc(adversarial);
    expect(doc).toContain(`<body>${adversarial}</body>`);
    // The outer base tag is placed in head and points to _blank only.
    expect(doc).toContain('<base target="_blank">');
    // CSP base-uri 'none' blocks any rogue base href anyway.
  });

  it('adversarial: </meta> in artifact does not affect outer head', () => {
    const adversarial = '</meta><style>body{color:red}</style>';
    const doc = composeIframeDoc(adversarial);
    expect(doc).toContain(`<body>${adversarial}</body>`);
  });

  it('adversarial: mixed quotes in artifact do not escape srcdoc binding', () => {
    // Single and double quotes in the artifact HTML. The caller (Vue :srcdoc)
    // handles attribute escaping; composeIframeDoc just ensures the content
    // is placed in body correctly.
    const adversarial = `<p class='a' data-x="b">test</p>`;
    const doc = composeIframeDoc(adversarial);
    expect(doc).toContain(`<body>${adversarial}</body>`);
  });

  it('places <base target="_blank"> in head', () => {
    const doc = composeIframeDoc('<p>test</p>');
    expect(doc).toContain('<base target="_blank">');
    const baseIdx = doc.indexOf('<base target="_blank">');
    const bodyIdx = doc.indexOf('<body>');
    expect(baseIdx).toBeLessThan(bodyIdx);
  });
});
