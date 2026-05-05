/**
 * imageRenderer.test.ts — render tests for ImageRenderer.vue
 * (artifact-preview-binary-rendering-01KQ8TD5 WP02 acceptance).
 */

import { describe, it, expect, vi } from 'vitest';
import { mount } from '@vue/test-utils';
import ImageRenderer from '../renderers/ImageRenderer.vue';

function makeArtifact(overrides = {}) {
  return {
    id: 'art-1',
    sessionId: 'sess-1',
    title: 'test.png',
    mimeType: 'image/png',
    contentHash: 'sha256:abc',
    byteSize: 100,
    source: 'user_pin' as const,
    sourceRef: { messageId: 'm-1' },
    scopeKind: 'session' as const,
    createdAt: '2026-05-01T00:00:00Z',
    ...overrides,
  };
}

function makeAbortController() {
  return new AbortController();
}

describe('ImageRenderer', () => {
  it('renders an <img> with the provided sourceUrl', () => {
    const ac = makeAbortController();
    const w = mount(ImageRenderer, {
      props: {
        artifact: makeArtifact(),
        bytesB64: 'aGVsbG8=',
        sourceUrl: 'blob:fake-image-url',
        abortSignal: ac.signal,
        onSizeExceeded: vi.fn(),
        onTimeout: vi.fn(),
      },
    });

    const img = w.find('img');
    expect(img.exists()).toBe(true);
    expect(img.attributes('src')).toBe('blob:fake-image-url');
  });

  it('img has max-h-[80vh] class by default (not zoomed)', () => {
    const ac = makeAbortController();
    const w = mount(ImageRenderer, {
      props: {
        artifact: makeArtifact(),
        bytesB64: 'aGVsbG8=',
        sourceUrl: 'blob:fake-image-url',
        abortSignal: ac.signal,
        onSizeExceeded: vi.fn(),
        onTimeout: vi.fn(),
      },
    });

    const img = w.find('img');
    expect(img.classes()).toContain('max-h-[80vh]');
  });

  it('calls onSizeExceeded when img fires onerror', async () => {
    const onSizeExceeded = vi.fn();
    const ac = makeAbortController();
    const w = mount(ImageRenderer, {
      props: {
        artifact: makeArtifact(),
        bytesB64: 'aGVsbG8=',
        sourceUrl: 'blob:fake-image-url',
        abortSignal: ac.signal,
        onSizeExceeded,
        onTimeout: vi.fn(),
      },
    });

    await w.find('img').trigger('error');
    expect(onSizeExceeded).toHaveBeenCalledOnce();
  });

  it('clicking the image toggles zoom (removes max-h-[80vh])', async () => {
    const ac = makeAbortController();
    const w = mount(ImageRenderer, {
      props: {
        artifact: makeArtifact(),
        bytesB64: 'aGVsbG8=',
        sourceUrl: 'blob:fake-image-url',
        abortSignal: ac.signal,
        onSizeExceeded: vi.fn(),
        onTimeout: vi.fn(),
      },
    });

    const img = w.find('img');
    expect(img.classes()).toContain('max-h-[80vh]');

    await img.trigger('click');
    // After zoom, max-h constraint is removed.
    expect(img.classes()).not.toContain('max-h-[80vh]');
  });

  it('uses artifact title as alt text', () => {
    const ac = makeAbortController();
    const w = mount(ImageRenderer, {
      props: {
        artifact: makeArtifact({ title: 'My Image' }),
        bytesB64: 'aGVsbG8=',
        sourceUrl: 'blob:fake-image-url',
        abortSignal: ac.signal,
        onSizeExceeded: vi.fn(),
        onTimeout: vi.fn(),
      },
    });

    expect(w.find('img').attributes('alt')).toBe('My Image');
  });

  it('does not use raw color literals (privacy CI invariant #4)', () => {
    const ac = makeAbortController();
    const w = mount(ImageRenderer, {
      props: {
        artifact: makeArtifact(),
        bytesB64: 'aGVsbG8=',
        sourceUrl: 'blob:fake-image-url',
        abortSignal: ac.signal,
        onSizeExceeded: vi.fn(),
        onTimeout: vi.fn(),
      },
    });
    expect(w.html()).not.toMatch(/#[0-9a-fA-F]{3,8}\b/);
  });
});
