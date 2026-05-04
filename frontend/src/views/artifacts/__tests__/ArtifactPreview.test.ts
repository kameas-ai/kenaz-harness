import { describe, it, expect, vi } from 'vitest';
import { mount, flushPromises } from '@vue/test-utils';
import ArtifactPreview from '@/views/artifacts/ArtifactPreview.vue';
import type { Artifact, ArtifactWithBytes } from '@/lib/types';
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

function makeArtifact(overrides: Partial<Artifact> = {}): Artifact {
  return {
    id: 'art-1',
    sessionId: 'sess-1',
    title: 'preview.txt',
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

function payloadFor(
  artifact: Artifact,
  text = 'hello',
): ArtifactWithBytes {
  return { artifact, bytes: btoa(text) };
}

describe('ArtifactPreview', () => {
  it('renders the title and mime header', () => {
    const a = makeArtifact();
    const w = mount(ArtifactPreview, {
      ...withFakeClient,
      props: { open: true, payload: payloadFor(a) },
    });
    expect(w.find('[data-testid="artifact-preview"]').exists()).toBe(true);
    expect(w.text()).toContain('preview.txt');
    expect(w.text()).toContain('text/plain');
  });

  it('renders text/plain inside a <pre>', () => {
    const a = makeArtifact({ mimeType: 'text/plain' });
    const w = mount(ArtifactPreview, {
      ...withFakeClient,
      props: { open: true, payload: payloadFor(a, 'plain code') },
    });
    expect(w.find('[data-testid="artifact-preview-text"]').exists()).toBe(true);
    expect(w.find('[data-testid="artifact-preview-text"]').text()).toContain('plain code');
  });

  it('renders text/markdown via the marked-backed StreamingText', () => {
    const a = makeArtifact({ mimeType: 'text/markdown' });
    const w = mount(ArtifactPreview, {
      ...withFakeClient,
      props: { open: true, payload: payloadFor(a, '# Heading\n\nbody') },
    });
    expect(w.find('[data-testid="artifact-preview-markdown"]').exists()).toBe(true);
    // marked should have produced an <h1>
    expect(w.find('[data-testid="artifact-preview-markdown"]').html()).toContain('<h1');
  });

  it('renders text/html in a sandboxed iframe with no allow flags by default', () => {
    const a = makeArtifact({ mimeType: 'text/html' });
    const w = mount(ArtifactPreview, {
      ...withFakeClient,
      props: { open: true, payload: payloadFor(a, '<p>html</p>') },
    });
    const iframe = w.find('iframe');
    expect(iframe.exists()).toBe(true);
    // Default sandbox is empty (most-restrictive).
    expect(iframe.attributes('sandbox')).toBe('');
    expect(w.find('[data-testid="artifact-preview-iframe-sandboxed"]').exists()).toBe(true);
    expect(w.find('[data-testid="artifact-preview-run-scripts"]').exists()).toBe(true);
  });

  it('toggling Run scripts adds allow-scripts to the sandbox', async () => {
    const a = makeArtifact({ mimeType: 'text/html' });
    const w = mount(ArtifactPreview, {
      ...withFakeClient,
      props: { open: true, payload: payloadFor(a, '<p>html</p>') },
    });
    await w.find('[data-testid="artifact-preview-run-scripts"]').trigger('click');
    const iframe = w.find('iframe');
    expect(iframe.attributes('sandbox')).toBe('allow-scripts');
    expect(w.find('[data-testid="artifact-preview-scripts-banner"]').exists()).toBe(true);
    expect(
      w.find('[data-testid="artifact-preview-iframe-sandboxed-scripts"]').exists(),
    ).toBe(true);
  });

  it('renders image/* via an <img> tag with a data: URL', () => {
    const a = makeArtifact({ mimeType: 'image/png' });
    const w = mount(ArtifactPreview, {
      ...withFakeClient,
      props: { open: true, payload: { artifact: a, bytes: 'aGVsbG8=' } },
    });
    expect(w.find('[data-testid="artifact-preview-image"]').exists()).toBe(true);
    const img = w.find('img');
    expect(img.exists()).toBe(true);
    expect(img.attributes('src')).toContain('data:image/png;base64,aGVsbG8=');
  });

  it('renders a "Preview not available" fallback for unknown mime types', () => {
    const a = makeArtifact({ mimeType: 'application/zip' });
    const w = mount(ArtifactPreview, {
      ...withFakeClient,
      props: { open: true, payload: payloadFor(a, 'binary') },
    });
    expect(w.find('[data-testid="artifact-preview-other"]').exists()).toBe(true);
    expect(w.text()).toContain('Preview not available');
  });

  it('Promote button calls onPromote with project scope when project context exists', async () => {
    const a = makeArtifact();
    const updated = { ...a, scopeKind: 'project' as const, projectId: 'proj-1' };
    const onPromote = vi.fn(async () => updated);
    const w = mount(ArtifactPreview, {
      ...withFakeClient,
      props: {
        open: true,
        payload: payloadFor(a),
        projectId: 'proj-1',
        onPromote,
      },
    });
    await w.find('[data-testid="artifact-preview-promote"]').trigger('click');
    await w.find('[data-testid="artifact-preview-promote-confirm-btn"]').trigger('click');
    await flushPromises();
    expect(onPromote).toHaveBeenCalledWith('art-1', 'project', 'proj-1');
    expect(w.emitted('promoted')).toBeTruthy();
  });

  it('Promote button is disabled when scope is already project (no global tier in v1)', () => {
    const a = makeArtifact({ scopeKind: 'project' });
    const w = mount(ArtifactPreview, {
      ...withFakeClient,
      props: { open: true, payload: payloadFor(a), projectId: 'proj-1' },
    });
    const btn = w.find('[data-testid="artifact-preview-promote"]');
    expect(btn.attributes('disabled')).toBeDefined();
    expect(btn.attributes('title')).toContain('global');
  });

  it('Delete button calls onDelete after confirmation', async () => {
    const a = makeArtifact();
    const onDelete = vi.fn(async () => undefined);
    const w = mount(ArtifactPreview, {
      ...withFakeClient,
      props: { open: true, payload: payloadFor(a), onDelete },
    });
    await w.find('[data-testid="artifact-preview-delete"]').trigger('click');
    await w.find('[data-testid="artifact-preview-delete-confirm-btn"]').trigger('click');
    await flushPromises();
    expect(onDelete).toHaveBeenCalledWith('art-1');
    expect(w.emitted('deleted')).toBeTruthy();
  });

  it('Download button calls artifacts.saveAs with id and suggested filename', async () => {
    const a = makeArtifact({
      mimeType: 'text/plain',
      title: 'preview.txt',
      sourceRef: { messageId: 'm-1' },
    });
    const saveAs = vi.fn(async () => '/tmp/preview.txt');
    const w = mount(ArtifactPreview, {
      global: {
        plugins: [
          {
            install(app: import('vue').App) {
              provideFakeClient(app, {
                artifacts: {
                  list: async () => [],
                  get: async (id) => ({
                    artifact: { ...a, id },
                    bytes: btoa('hello'),
                  }),
                  promote: async () => a,
                  remove: async () => undefined,
                  saveAs,
                },
              });
            },
          },
        ],
      },
      props: { open: true, payload: payloadFor(a, 'hello') },
    });
    await w.find('[data-testid="artifact-preview-download"]').trigger('click');
    await flushPromises();
    expect(saveAs).toHaveBeenCalledWith('art-1', 'preview.txt');
  });

  it('Copy button writes decoded text to the clipboard for text mimes', async () => {
    const a = makeArtifact({ mimeType: 'text/plain' });
    const writeText = vi.fn(async () => undefined);
    const original = navigator.clipboard;
    Object.defineProperty(navigator, 'clipboard', {
      value: { writeText },
      configurable: true,
    });
    try {
      const w = mount(ArtifactPreview, {
        ...withFakeClient,
        props: { open: true, payload: payloadFor(a, 'hello') },
      });
      await w.find('[data-testid="artifact-preview-copy"]').trigger('click');
      await flushPromises();
      expect(writeText).toHaveBeenCalledWith('hello');
    } finally {
      Object.defineProperty(navigator, 'clipboard', {
        value: original,
        configurable: true,
      });
    }
  });

  it('uses no raw color literal (privacy CI invariant #4)', () => {
    const a = makeArtifact();
    const w = mount(ArtifactPreview, {
      ...withFakeClient,
      props: { open: true, payload: payloadFor(a) },
    });
    expect(w.html()).not.toMatch(/#[0-9a-fA-F]{3,8}\b/);
  });
});
