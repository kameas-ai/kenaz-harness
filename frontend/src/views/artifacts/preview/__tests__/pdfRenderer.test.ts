/**
 * pdfRenderer.test.ts — render tests for PdfRenderer.vue
 * (artifact-preview-binary-rendering-01KQ8TD5 WP03 acceptance).
 *
 * Tests verify the native <embed> path; PDF.js polyfill path is not tested
 * here because it requires a real Canvas + worker, which jsdom/happy-dom
 * cannot provide. Bundle-size verification is a CI gate (check-bundle-size.mjs).
 */

import { describe, it, expect, vi } from 'vitest';
import { mount } from '@vue/test-utils';
import PdfRenderer from '../renderers/PdfRenderer.vue';

function makeArtifact(overrides = {}) {
  return {
    id: 'art-pdf-1',
    sessionId: 'sess-1',
    title: 'report.pdf',
    mimeType: 'application/pdf',
    contentHash: 'sha256:ddd',
    byteSize: 10000,
    source: 'user_pin' as const,
    sourceRef: { messageId: 'm-1' },
    scopeKind: 'session' as const,
    createdAt: '2026-05-01T00:00:00Z',
    ...overrides,
  };
}

describe('PdfRenderer', () => {
  it('renders a native <embed type="application/pdf"> initially', () => {
    const ac = new AbortController();
    const w = mount(PdfRenderer, {
      props: {
        artifact: makeArtifact(),
        bytesB64: 'JVBER', // minimal PDF-like base64
        sourceUrl: 'blob:fake-pdf-url',
        abortSignal: ac.signal,
        onSizeExceeded: vi.fn(),
        onTimeout: vi.fn(),
      },
    });

    const embed = w.find('[data-testid="pdf-renderer-embed"]');
    expect(embed.exists()).toBe(true);
    expect(embed.attributes('type')).toBe('application/pdf');
    expect(embed.attributes('src')).toBe('blob:fake-pdf-url');
  });

  it('does not show canvas or error before onerror fires', () => {
    const ac = new AbortController();
    const w = mount(PdfRenderer, {
      props: {
        artifact: makeArtifact(),
        bytesB64: 'JVBER',
        sourceUrl: 'blob:fake-pdf-url',
        abortSignal: ac.signal,
        onSizeExceeded: vi.fn(),
        onTimeout: vi.fn(),
      },
    });

    expect(w.find('[data-testid="pdf-renderer-canvas"]').exists()).toBe(false);
    expect(w.find('[data-testid="pdf-renderer-error"]').exists()).toBe(false);
    expect(w.find('[data-testid="pdf-renderer-loading"]').exists()).toBe(false);
  });

  it('does not use raw color literals (privacy CI invariant #4)', () => {
    const ac = new AbortController();
    const w = mount(PdfRenderer, {
      props: {
        artifact: makeArtifact(),
        bytesB64: 'JVBER',
        sourceUrl: 'blob:fake-pdf-url',
        abortSignal: ac.signal,
        onSizeExceeded: vi.fn(),
        onTimeout: vi.fn(),
      },
    });
    expect(w.html()).not.toMatch(/#[0-9a-fA-F]{3,8}\b/);
  });
});
