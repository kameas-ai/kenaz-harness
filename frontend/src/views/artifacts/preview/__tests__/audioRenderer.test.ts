/**
 * audioRenderer.test.ts — render tests for AudioRenderer.vue
 * (artifact-preview-binary-rendering-01KQ8TD5 WP02 acceptance).
 */

import { describe, it, expect, vi } from 'vitest';
import { mount } from '@vue/test-utils';
import AudioRenderer from '../renderers/AudioRenderer.vue';

function makeArtifact(overrides = {}) {
  return {
    id: 'art-audio-1',
    sessionId: 'sess-1',
    title: 'test.mp3',
    mimeType: 'audio/mpeg',
    contentHash: 'sha256:aaa',
    byteSize: 500,
    source: 'user_pin' as const,
    sourceRef: { messageId: 'm-1' },
    scopeKind: 'session' as const,
    createdAt: '2026-05-01T00:00:00Z',
    ...overrides,
  };
}

describe('AudioRenderer', () => {
  it('renders <audio controls> without autoplay', () => {
    const ac = new AbortController();
    const w = mount(AudioRenderer, {
      props: {
        artifact: makeArtifact(),
        bytesB64: 'aGVsbG8=',
        sourceUrl: 'blob:fake-audio-url',
        abortSignal: ac.signal,
        onSizeExceeded: vi.fn(),
        onTimeout: vi.fn(),
      },
    });

    const audio = w.find('audio');
    expect(audio.exists()).toBe(true);
    expect(audio.attributes('controls')).toBeDefined();
    expect(audio.attributes('autoplay')).toBeUndefined();
    expect(audio.attributes('src')).toBe('blob:fake-audio-url');
  });

  it('renders a download button', () => {
    const ac = new AbortController();
    const w = mount(AudioRenderer, {
      props: {
        artifact: makeArtifact(),
        bytesB64: 'aGVsbG8=',
        sourceUrl: 'blob:fake-audio-url',
        abortSignal: ac.signal,
        onSizeExceeded: vi.fn(),
        onTimeout: vi.fn(),
      },
    });

    const btn = w.find('[data-testid="audio-renderer-download"]');
    expect(btn.exists()).toBe(true);
    expect(btn.text()).toContain('Download');
  });

  it('does not use raw color literals (privacy CI invariant #4)', () => {
    const ac = new AbortController();
    const w = mount(AudioRenderer, {
      props: {
        artifact: makeArtifact(),
        bytesB64: 'aGVsbG8=',
        sourceUrl: 'blob:fake-audio-url',
        abortSignal: ac.signal,
        onSizeExceeded: vi.fn(),
        onTimeout: vi.fn(),
      },
    });
    expect(w.html()).not.toMatch(/#[0-9a-fA-F]{3,8}\b/);
  });
});
