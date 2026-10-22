/**
 * pdfRenderer.test.ts — render tests for PdfRenderer.vue
 * (artifact-preview-binary-rendering-01KQ8TD5 WP03 acceptance).
 *
 * The native <embed> path is tested directly. The PDF.js fallback path is
 * driven through the component with `pdfjs-dist` mocked (jsdom has no real
 * Canvas or Worker), pinning the call contract that the security posture
 * depends on (GHSA-hq66-cqwq-w95j): core API only, enableXfa off, no
 * scripting options, worker loaded from a bundled same-origin URL.
 * Bundle-size verification is a CI gate (check-bundle-size.mjs).
 */

import { describe, it, expect, vi, beforeEach } from 'vitest';
import { mount, flushPromises } from '@vue/test-utils';
import PdfRenderer from '../renderers/PdfRenderer.vue';

const pdfjsMock = vi.hoisted(() => {
  const render = vi.fn(() => ({ promise: Promise.resolve() }));
  const getViewport = vi.fn(() => ({ width: 300, height: 400 }));
  const getPage = vi.fn(async () => ({ getViewport, render }));
  // pdfjs-dist 6: teardown lives on the loading task, not the document.
  const destroy = vi.fn(() => Promise.resolve());
  const getDocument = vi.fn((_params: Record<string, unknown>) => ({
    promise: Promise.resolve({ getPage }),
    destroy,
  }));
  return {
    render,
    getViewport,
    getPage,
    destroy,
    getDocument,
    GlobalWorkerOptions: { workerSrc: '' },
  };
});

vi.mock('pdfjs-dist', () => ({
  getDocument: pdfjsMock.getDocument,
  GlobalWorkerOptions: pdfjsMock.GlobalWorkerOptions,
}));

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

  describe('PDF.js fallback (embed onerror)', () => {
    beforeEach(() => {
      pdfjsMock.getDocument.mockClear();
      pdfjsMock.render.mockClear();
      pdfjsMock.destroy.mockClear();
      pdfjsMock.GlobalWorkerOptions.workerSrc = '';
    });

    // "%PDF-1.7" base64-encoded.
    const PDF_HEADER_B64 = 'JVBERi0xLjc=';

    function mountAndFail(onSizeExceeded = vi.fn()) {
      const ac = new AbortController();
      const w = mount(PdfRenderer, {
        props: {
          artifact: makeArtifact(),
          bytesB64: PDF_HEADER_B64,
          sourceUrl: 'blob:fake-pdf-url',
          abortSignal: ac.signal,
          onSizeExceeded,
          onTimeout: vi.fn(),
        },
      });
      return w;
    }

    it('loads the decoded bytes via the core API with XFA off and no scripting', async () => {
      const onSizeExceeded = vi.fn();
      const w = mountAndFail(onSizeExceeded);
      await w.find('[data-testid="pdf-renderer-embed"]').trigger('error');
      await flushPromises();

      expect(pdfjsMock.getDocument).toHaveBeenCalledTimes(1);
      const params = pdfjsMock.getDocument.mock.calls[0][0];
      expect(Array.from(params.data as Uint8Array)).toEqual(
        Array.from(new TextEncoder().encode('%PDF-1.7')),
      );
      expect(params.enableXfa).toBe(false);
      // The advisory's exploit chain needs the scripting bridge; this
      // renderer must never opt into it.
      expect(params).not.toHaveProperty('enableScripting');
      expect(Object.keys(params).sort()).toEqual(['data', 'enableXfa']);

      expect(pdfjsMock.render).toHaveBeenCalledTimes(1);
      expect(onSizeExceeded).not.toHaveBeenCalled();
      expect(w.find('[data-testid="pdf-renderer-error"]').exists()).toBe(false);
      const canvas = w.find('[data-testid="pdf-renderer-canvas"]');
      expect(canvas.isVisible()).toBe(true);
      expect((canvas.element as HTMLCanvasElement).width).toBe(300);
    });

    it('points the worker at the bundled pdf.worker module, not a remote URL', async () => {
      const w = mountAndFail();
      await w.find('[data-testid="pdf-renderer-embed"]').trigger('error');
      await flushPromises();

      const src = pdfjsMock.GlobalWorkerOptions.workerSrc;
      expect(src).toMatch(/pdf\.worker(\.min)?\.mjs/);
      expect(src).not.toMatch(/^https?:\/\/(?!localhost)/);
    });

    it('surfaces a load failure and still tears down the loading task', async () => {
      pdfjsMock.getDocument.mockImplementationOnce((_params: Record<string, unknown>) => ({
        promise: Promise.reject(new Error('Invalid PDF structure.')),
        destroy: pdfjsMock.destroy,
      }));
      const onSizeExceeded = vi.fn();
      const w = mountAndFail(onSizeExceeded);
      await w.find('[data-testid="pdf-renderer-embed"]').trigger('error');
      await flushPromises();

      expect(w.find('[data-testid="pdf-renderer-error"]').text()).toContain(
        'Invalid PDF structure.',
      );
      expect(onSizeExceeded).toHaveBeenCalledTimes(1);
      w.unmount();
      expect(pdfjsMock.destroy).toHaveBeenCalledTimes(1);
    });

    it('destroys the loading task (document + worker) on unmount', async () => {
      const w = mountAndFail();
      await w.find('[data-testid="pdf-renderer-embed"]').trigger('error');
      await flushPromises();
      w.unmount();
      expect(pdfjsMock.destroy).toHaveBeenCalledTimes(1);
    });
  });
});
