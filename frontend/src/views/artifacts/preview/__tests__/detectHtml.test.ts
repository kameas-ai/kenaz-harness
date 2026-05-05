/**
 * detectHtml.test.ts — unit tests for the HTML detection utility
 * (artifact-preview-binary-rendering-01KQ8TD5 WP01 acceptance).
 */

import { describe, it, expect, beforeEach } from 'vitest';
import { containsHtml, containsHtmlCached, clearDetectHtmlCache } from '../detectHtml';

beforeEach(() => {
  clearDetectHtmlCache();
});

describe('containsHtml', () => {
  it('returns false for plain markdown', () => {
    expect(containsHtml('# heading\n\nsome paragraph')).toBe(false);
  });

  it('returns false for empty string', () => {
    expect(containsHtml('')).toBe(false);
  });

  it('returns false for markdown with only asterisks and brackets', () => {
    expect(containsHtml('**bold** [link](url) _italic_')).toBe(false);
  });

  it('returns true when source contains <details>', () => {
    expect(containsHtml('hi <details>x</details>')).toBe(true);
  });

  it('returns true when source contains <img>', () => {
    expect(containsHtml('<img src="x" />')).toBe(true);
  });

  it('returns true when source contains <script>', () => {
    expect(containsHtml('<script>alert(1)</script>')).toBe(true);
  });

  it('returns true when source contains <iframe>', () => {
    expect(containsHtml('<iframe src="x" />')).toBe(true);
  });

  it('returns true for summary tag', () => {
    expect(containsHtml('<summary>Click me</summary>')).toBe(true);
  });

  it('returns true for uppercase tags', () => {
    expect(containsHtml('<DIV>test</DIV>')).toBe(true);
  });

  it('returns false for lone angle brackets without tag', () => {
    expect(containsHtml('a < b and c > d')).toBe(false);
  });
});

describe('containsHtmlCached', () => {
  it('returns consistent result for the same hash', () => {
    const r1 = containsHtmlCached('hash1', 'plain text');
    const r2 = containsHtmlCached('hash1', 'different text that has <div>');
    // Second call uses cache keyed on 'hash1' → should return first result
    expect(r1).toBe(false);
    expect(r2).toBe(false); // cached from first call
  });

  it('returns new result for different hash', () => {
    const r1 = containsHtmlCached('hash1', 'plain text');
    const r2 = containsHtmlCached('hash2', '<div>html</div>');
    expect(r1).toBe(false);
    expect(r2).toBe(true);
  });

  it('clears cache on clearDetectHtmlCache', () => {
    containsHtmlCached('hash1', 'plain text');
    clearDetectHtmlCache();
    // After clearing, re-evaluating with same hash but different source
    const r = containsHtmlCached('hash1', '<div>html</div>');
    expect(r).toBe(true);
  });
});
