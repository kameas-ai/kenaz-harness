/**
 * videoRenderer.test.ts — render tests for VideoRenderer.vue
 * (artifact-preview-binary-rendering-01KQ8TD5 WP02 acceptance).
 */

import { describe, it, expect, vi } from 'vitest';
import { mount } from '@vue/test-utils';
import VideoRenderer from '../renderers/VideoRenderer.vue';

function makeArtifact(overrides = {}) {
  return {
    id: 'art-video-1',
    sessionId: 'sess-1',
    title: 'clip.mp4',
    mimeType: 'video/mp4',
    contentHash: 'sha256:bbb',
    byteSize: 2000,
    source: 'user_pin' as const,
    sourceRef: { messageId: 'm-1' },
    scopeKind: 'session' as const,
    createdAt: '2026-05-01T00:00:00Z',
    ...overrides,
  };
}

describe('VideoRenderer', () => {
  it('renders <video controls preload="metadata"> without autoplay', () => {
    const ac = new AbortController();
    const w = mount(VideoRenderer, {
      props: {
        artifact: makeArtifact(),
        bytesB64: 'aGVsbG8=',
        sourceUrl: 'blob:fake-video-url',
        abortSignal: ac.signal,
        onSizeExceeded: vi.fn(),
        onTimeout: vi.fn(),
      },
    });

    const video = w.find('video');
    expect(video.exists()).toBe(true);
    expect(video.attributes('controls')).toBeDefined();
    expect(video.attributes('preload')).toBe('metadata');
    expect(video.attributes('autoplay')).toBeUndefined();
    expect(video.attributes('src')).toBe('blob:fake-video-url');
  });

  it('renders a download button', () => {
    const ac = new AbortController();
    const w = mount(VideoRenderer, {
      props: {
        artifact: makeArtifact(),
        bytesB64: 'aGVsbG8=',
        sourceUrl: 'blob:fake-video-url',
        abortSignal: ac.signal,
        onSizeExceeded: vi.fn(),
        onTimeout: vi.fn(),
      },
    });

    const btn = w.find('[data-testid="video-renderer-download"]');
    expect(btn.exists()).toBe(true);
    expect(btn.text()).toContain('Download');
  });

  it('does not use raw color literals (privacy CI invariant #4)', () => {
    const ac = new AbortController();
    const w = mount(VideoRenderer, {
      props: {
        artifact: makeArtifact(),
        bytesB64: 'aGVsbG8=',
        sourceUrl: 'blob:fake-video-url',
        abortSignal: ac.signal,
        onSizeExceeded: vi.fn(),
        onTimeout: vi.fn(),
      },
    });
    expect(w.html()).not.toMatch(/#[0-9a-fA-F]{3,8}\b/);
  });
});
