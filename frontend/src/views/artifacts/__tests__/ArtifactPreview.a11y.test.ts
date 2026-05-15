/**
 * ArtifactPreview — axe-core accessibility assertions
 * (accessibility-audit-01KQ8TDA WP02)
 *
 * Mounts ArtifactPreview with a text/plain artifact and asserts no axe-core
 * violations. color-contrast disabled: happy-dom returns 0/0 ratio
 * (needs_manual_verification in a real browser).
 */
import { describe, it, expect } from 'vitest';
import { mount, flushPromises } from '@vue/test-utils';
import ArtifactPreview from '@/views/artifacts/ArtifactPreview.vue';
import type { Artifact, ArtifactWithBytes } from '@/lib/types';
import { provideFakeClient } from '@/lib/harnessClientContext';
import { axe } from 'vitest-axe';

/** Axe options shared by all tests in this file. */
const axeOptions = {
  rules: {
    // happy-dom does not compute CSS, so color-contrast always reports 0/0.
    // needs_manual_verification: check contrast in dark + light themes with
    // a real browser.
    'color-contrast': { enabled: false },
    // region — component tree mounts without Shell landmark wrapper in tests.
    region: { enabled: false },
  },
};

function makeArtifact(overrides: Partial<Artifact> = {}): Artifact {
  return {
    id: 'art-a11y',
    sessionId: 'sess-1',
    title: 'hello.txt',
    mimeType: 'text/plain',
    contentHash: 'sha256:abc',
    byteSize: 5,
    source: 'user_pin',
    sourceRef: { messageId: 'm-1' },
    scopeKind: 'session',
    createdAt: '2026-04-26T00:00:00Z',
    ...overrides,
  };
}

function makePayload(artifact: Artifact, text = 'hello'): ArtifactWithBytes {
  return { artifact, bytes: btoa(text) };
}

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

describe('ArtifactPreview — a11y (axe-core)', () => {
  it('has no axe violations when rendering a text/plain artifact', async () => {
    const a = makeArtifact();
    const w = mount(ArtifactPreview, {
      ...withFakeClient,
      props: { open: true, payload: makePayload(a) },
    });
    await flushPromises();
    const results = await axe(w.element, axeOptions);
    // @ts-expect-error — toHaveNoViolations added via test-setup.ts extend
    expect(results).toHaveNoViolations();
  });

  it('has no axe violations when rendering a text/markdown artifact', async () => {
    const a = makeArtifact({ mimeType: 'text/markdown' });
    const w = mount(ArtifactPreview, {
      ...withFakeClient,
      props: { open: true, payload: makePayload(a, '# Hello\n\nWorld.') },
    });
    await flushPromises();
    const results = await axe(w.element, axeOptions);
    // @ts-expect-error — toHaveNoViolations added via test-setup.ts extend
    expect(results).toHaveNoViolations();
  });
});
