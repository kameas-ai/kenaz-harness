/**
 * registry.test.ts — unit tests for the renderer registry
 * (artifact-preview-binary-rendering-01KQ8TD5 WP01 acceptance).
 */

import { describe, it, expect, beforeEach } from 'vitest';
import { pickRenderer } from '../registry';
import { clearDetectHtmlCache } from '../detectHtml';

beforeEach(() => {
  clearDetectHtmlCache();
});

describe('pickRenderer', () => {
  it('routes image/png to image kind', () => {
    expect(pickRenderer('image/png', '', 'h1').kind).toBe('image');
  });

  it('routes image/jpeg to image kind', () => {
    expect(pickRenderer('image/jpeg', '', 'h2').kind).toBe('image');
  });

  it('routes image/svg+xml to image kind', () => {
    expect(pickRenderer('image/svg+xml', '', 'h3').kind).toBe('image');
  });

  it('routes image/gif to image kind', () => {
    expect(pickRenderer('image/gif', '', 'h4').kind).toBe('image');
  });

  it('routes image/webp to image kind', () => {
    expect(pickRenderer('image/webp', '', 'h5').kind).toBe('image');
  });

  it('routes application/pdf to pdf kind', () => {
    expect(pickRenderer('application/pdf', '', 'h6').kind).toBe('pdf');
  });

  it('routes audio/mpeg to audio kind', () => {
    expect(pickRenderer('audio/mpeg', '', 'h7').kind).toBe('audio');
  });

  it('routes audio/wav to audio kind', () => {
    expect(pickRenderer('audio/wav', '', 'h8').kind).toBe('audio');
  });

  it('routes video/mp4 to video kind', () => {
    expect(pickRenderer('video/mp4', '', 'h9').kind).toBe('video');
  });

  it('routes video/webm to video kind', () => {
    expect(pickRenderer('video/webm', '', 'ha').kind).toBe('video');
  });

  it('routes text/html to html kind', () => {
    expect(pickRenderer('text/html', '', 'hb').kind).toBe('html');
  });

  it('routes text/markdown without HTML to markdown-inline', () => {
    expect(pickRenderer('text/markdown', '# heading\n\nparagraph', 'hc').kind).toBe('markdown-inline');
  });

  it('routes text/markdown with HTML to markdown-iframed', () => {
    expect(pickRenderer('text/markdown', '# hi\n<details>x</details>', 'hd').kind).toBe('markdown-iframed');
  });

  it('routes text/plain to text kind', () => {
    expect(pickRenderer('text/plain', '', 'he').kind).toBe('text');
  });

  it('routes application/json to text kind', () => {
    expect(pickRenderer('application/json', '', 'hf').kind).toBe('text');
  });

  it('routes application/xml to text kind', () => {
    expect(pickRenderer('application/xml', '', 'hg').kind).toBe('text');
  });

  it('routes application/x-yaml to text kind', () => {
    expect(pickRenderer('application/x-yaml', '', 'hh').kind).toBe('text');
  });

  it('routes application/yaml to text kind', () => {
    expect(pickRenderer('application/yaml', '', 'hi').kind).toBe('text');
  });

  it('routes application/octet-stream to unknown-binary', () => {
    expect(pickRenderer('application/octet-stream', '', 'hj').kind).toBe('unknown-binary');
  });

  it('routes application/zip to unknown-binary', () => {
    expect(pickRenderer('application/zip', '', 'hk').kind).toBe('unknown-binary');
  });

  it('routes empty mime to unknown-binary', () => {
    expect(pickRenderer('', '', 'hl').kind).toBe('unknown-binary');
  });

  it('handles mime type with charset suffix', () => {
    expect(pickRenderer('text/plain; charset=utf-8', '', 'hm').kind).toBe('text');
  });

  it('image renderer enforces byte cap (enforceCap=true)', () => {
    expect(pickRenderer('image/png', '', 'hn').enforceCap).toBe(true);
  });

  it('html renderer does not enforce cap in parent (enforceCap=false)', () => {
    expect(pickRenderer('text/html', '', 'ho').enforceCap).toBe(false);
  });

  it('markdown-inline renderer does not enforce cap (enforceCap=false)', () => {
    expect(pickRenderer('text/markdown', 'plain markdown', 'hp').enforceCap).toBe(false);
  });

  it('unknown-binary renderer does not enforce cap (enforceCap=false)', () => {
    expect(pickRenderer('application/zip', '', 'hq').enforceCap).toBe(false);
  });

  it('each spec includes a component', () => {
    const mimes = [
      'image/png',
      'application/pdf',
      'audio/mpeg',
      'video/mp4',
      'text/html',
      'text/markdown',
      'text/plain',
      'application/zip',
    ];
    for (const m of mimes) {
      const spec = pickRenderer(m, '', m);
      expect(spec.component, `${m} should have a component`).toBeTruthy();
    }
  });
});
