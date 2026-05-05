/**
 * markdownIframed.test.ts — tests for markdown routing + iframe sandbox path
 * (artifact-preview-binary-rendering-01KQ8TD5 WP05 acceptance).
 */

import { describe, it, expect, beforeEach } from 'vitest';
import { mount } from '@vue/test-utils';
import MarkdownInlineRenderer from '../renderers/MarkdownInlineRenderer.vue';
import MarkdownIframedRenderer from '../renderers/MarkdownIframedRenderer.vue';
import { markdownToHtml } from '../markdownToHtml';
import { pickRenderer } from '../registry';
import { clearDetectHtmlCache } from '../detectHtml';
import { provideFakeClient } from '@/lib/harnessClientContext';

const withFakeClient = {
  global: {
    plugins: [
      {
        install(app: import('vue').App) {
          provideFakeClient(app);
        },
      },
    ],
  },
};

beforeEach(() => {
  clearDetectHtmlCache();
});

function makeArtifact(overrides = {}) {
  return {
    id: 'art-md-1',
    sessionId: 'sess-1',
    title: 'doc.md',
    mimeType: 'text/markdown',
    contentHash: 'sha256:ccc',
    byteSize: 50,
    source: 'user_pin' as const,
    sourceRef: { messageId: 'm-1' },
    scopeKind: 'session' as const,
    createdAt: '2026-05-01T00:00:00Z',
    ...overrides,
  };
}

describe('Markdown routing', () => {
  it('routes plain markdown (no HTML tags) to markdown-inline', () => {
    const spec = pickRenderer('text/markdown', '# heading\n\nparagraph text', 'hash1');
    expect(spec.kind).toBe('markdown-inline');
  });

  it('routes markdown with <details> to markdown-iframed', () => {
    const spec = pickRenderer('text/markdown', '<details><summary>x</summary>y</details>', 'hash2');
    expect(spec.kind).toBe('markdown-iframed');
  });

  it('routes markdown with <img> to markdown-iframed', () => {
    const spec = pickRenderer('text/markdown', '# title\n<img src="x" />', 'hash3');
    expect(spec.kind).toBe('markdown-iframed');
  });
});

describe('markdownToHtml', () => {
  it('converts # heading to <h1>', () => {
    const html = markdownToHtml('# Hello');
    expect(html).toContain('<h1');
    expect(html).toContain('Hello');
  });

  it('converts **bold** to <strong>', () => {
    const html = markdownToHtml('**bold text**');
    expect(html).toContain('<strong>');
  });

  it('returns a non-empty string for non-empty input', () => {
    expect(markdownToHtml('some text')).toBeTruthy();
  });
});

describe('MarkdownInlineRenderer', () => {
  it('renders markdown via StreamingText (produces heading tags)', () => {
    const ac = new AbortController();
    const w = mount(MarkdownInlineRenderer, {
      ...withFakeClient,
      props: {
        artifact: makeArtifact(),
        bytesB64: btoa('# My heading\n\nParagraph.'),
        sourceUrl: '',
        abortSignal: ac.signal,
        onSizeExceeded: () => {},
        onTimeout: () => {},
      },
    });

    expect(w.find('[data-testid="markdown-inline-renderer"]').exists()).toBe(true);
    // StreamingText delegates to MarkdownBlock which renders <h1>.
    expect(w.html()).toContain('<h1');
  });
});

describe('MarkdownIframedRenderer', () => {
  it('renders an iframe for markdown with embedded HTML', () => {
    const ac = new AbortController();
    const w = mount(MarkdownIframedRenderer, {
      ...withFakeClient,
      props: {
        artifact: makeArtifact(),
        bytesB64: btoa('# heading\n<details>x</details>'),
        sourceUrl: '',
        abortSignal: ac.signal,
        onSizeExceeded: () => {},
        onTimeout: () => {},
      },
    });

    expect(w.find('[data-testid="markdown-iframed-renderer"]').exists()).toBe(true);
    const iframe = w.find('iframe');
    expect(iframe.exists()).toBe(true);
    expect(iframe.attributes('sandbox')).toBe('allow-same-origin');
  });

  it('iframe srcdoc contains compiled markdown heading', () => {
    const ac = new AbortController();
    const w = mount(MarkdownIframedRenderer, {
      ...withFakeClient,
      props: {
        artifact: makeArtifact(),
        bytesB64: btoa('# My Heading\n<details>x</details>'),
        sourceUrl: '',
        abortSignal: ac.signal,
        onSizeExceeded: () => {},
        onTimeout: () => {},
      },
    });

    const srcdoc = w.find('iframe').attributes('srcdoc') ?? '';
    expect(srcdoc).toContain('<h1');
  });
});
