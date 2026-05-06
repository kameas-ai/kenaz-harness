/**
 * unknownBinaryRenderer.test.ts — integration tests for UnknownBinaryRenderer.vue
 * (artifact-preview-binary-rendering-01KQ8TD5, v0.5.x audit).
 *
 * Covers FR-008 (unknown-mime fallback) and FR-009 (cap-exceeded fallback):
 *   - Renders the correct reason text for size / time / no-preview cases.
 *   - Shows MIME type and formatted byte size.
 *   - "Open with system app" button calls artifacts.saveAs.
 *   - No raw colour literals (privacy CI invariant #4).
 */

import { describe, it, expect, vi } from 'vitest';
import { mount } from '@vue/test-utils';
import UnknownBinaryRenderer from '../renderers/UnknownBinaryRenderer.vue';
import { provideFakeClient } from '@/lib/harnessClientContext';

function makeArtifact(overrides: Record<string, unknown> = {}) {
  return {
    id: 'art-bin-1',
    sessionId: 'sess-1',
    title: 'archive.zip',
    mimeType: 'application/zip',
    contentHash: 'sha256:aaa',
    byteSize: 2_097_152, // 2 MiB
    source: 'user_pin' as const,
    sourceRef: { messageId: 'm-1' },
    scopeKind: 'session' as const,
    createdAt: '2026-05-01T00:00:00Z',
    ...overrides,
  };
}

function mountRenderer(
  capReason: 'size' | 'time' | null = null,
  artifactOverrides: Record<string, unknown> = {},
  saveAsMock = vi.fn().mockResolvedValue(undefined),
) {
  const ac = new AbortController();
  const artifact = makeArtifact(artifactOverrides);
  return mount(UnknownBinaryRenderer, {
    props: {
      artifact,
      bytesB64: '',
      sourceUrl: '',
      abortSignal: ac.signal,
      onSizeExceeded: vi.fn(),
      onTimeout: vi.fn(),
      capReason,
    },
    global: {
      plugins: [
        {
          install(app) {
            provideFakeClient(app, {
              artifacts: {
                saveAs: saveAsMock,
              } as any,
            });
          },
        },
      ],
    },
  });
}

describe('UnknownBinaryRenderer (FR-008/FR-009, v0.5.x audit)', () => {
  // ── Cap-reason messages ───────────────────────────────────────────────

  it('shows size-cap message when capReason is "size"', () => {
    const w = mountRenderer('size');
    expect(w.find('[data-testid="unknown-binary-cap-size"]').exists()).toBe(true);
    expect(w.find('[data-testid="unknown-binary-cap-size"]').text()).toContain('5 MB');
  });

  it('shows time-cap message when capReason is "time"', () => {
    const w = mountRenderer('time');
    expect(w.find('[data-testid="unknown-binary-cap-time"]').exists()).toBe(true);
    expect(w.find('[data-testid="unknown-binary-cap-time"]').text()).toContain('timed out');
  });

  it('shows generic no-preview message when capReason is null', () => {
    const w = mountRenderer(null);
    expect(w.find('[data-testid="unknown-binary-no-preview"]').exists()).toBe(true);
    expect(w.find('[data-testid="unknown-binary-no-preview"]').text()).toContain('Preview not available');
  });

  // ── File metadata display ─────────────────────────────────────────────

  it('displays the MIME type and formatted byte size', () => {
    const w = mountRenderer(null, { mimeType: 'application/zip', byteSize: 2_097_152 });
    const html = w.html();
    expect(html).toContain('application/zip');
    // 2 MiB formatted
    expect(html).toContain('2.0 MiB');
  });

  it('formats byte sizes under 1 KiB as plain bytes', () => {
    const w = mountRenderer(null, { byteSize: 512 });
    expect(w.html()).toContain('512 B');
  });

  it('formats byte sizes in KiB range', () => {
    const w = mountRenderer(null, { byteSize: 10_240 });
    expect(w.html()).toContain('10.0 KiB');
  });

  // ── Open externally ───────────────────────────────────────────────────

  it('renders the "Open with system app" button', () => {
    const w = mountRenderer(null);
    expect(w.find('[data-testid="unknown-binary-open-external"]').exists()).toBe(true);
  });

  it('calls artifacts.saveAs with artifact id and title when button is clicked', async () => {
    const saveAs = vi.fn().mockResolvedValue(undefined);
    const w = mountRenderer(null, { id: 'art-xyz', title: 'archive.zip' }, saveAs);
    await w.find('[data-testid="unknown-binary-open-external"]').trigger('click');
    // saveAs is async — flush microtasks
    await new Promise((r) => setTimeout(r, 0));
    expect(saveAs).toHaveBeenCalledWith('art-xyz', 'archive.zip');
  });

  it('uses sourceRef.filename as the suggested name when available', async () => {
    const saveAs = vi.fn().mockResolvedValue(undefined);
    const w = mountRenderer(
      null,
      { id: 'art-fn', sourceRef: { messageId: 'm-1', filename: 'data.csv' } },
      saveAs,
    );
    await w.find('[data-testid="unknown-binary-open-external"]').trigger('click');
    await new Promise((r) => setTimeout(r, 0));
    expect(saveAs).toHaveBeenCalledWith('art-fn', 'data.csv');
  });

  // ── All three cap states are mutually exclusive in the DOM ────────────

  it('does not render size-cap text when capReason is "time"', () => {
    const w = mountRenderer('time');
    expect(w.find('[data-testid="unknown-binary-cap-size"]').exists()).toBe(false);
    expect(w.find('[data-testid="unknown-binary-no-preview"]').exists()).toBe(false);
  });

  it('does not render time-cap or no-preview text when capReason is "size"', () => {
    const w = mountRenderer('size');
    expect(w.find('[data-testid="unknown-binary-cap-time"]').exists()).toBe(false);
    expect(w.find('[data-testid="unknown-binary-no-preview"]').exists()).toBe(false);
  });

  // ── Privacy CI invariant #4 ───────────────────────────────────────────

  it('uses no raw colour literals (privacy CI invariant #4)', () => {
    const w = mountRenderer('size');
    expect(w.html()).not.toMatch(/#[0-9a-fA-F]{3,8}\b/);
  });
});
