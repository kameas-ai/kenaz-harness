import { describe, it, expect } from 'vitest';
import { mount, flushPromises } from '@vue/test-utils';
import { createMemoryHistory, createRouter } from 'vue-router';
import ArtifactsView from '@/views/artifacts/ArtifactsView.vue';
import { provideFakeClient } from '@/lib/harnessClientContext';
import type { Artifact, ArtifactFilter } from '@/lib/types';

function makeArtifact(overrides: Partial<Artifact> = {}): Artifact {
  return {
    id: 'art-1',
    sessionId: 'sess-1',
    title: 'item',
    mimeType: 'text/plain',
    contentHash: 'sha256:a',
    byteSize: 10,
    source: 'code_block',
    sourceRef: { messageId: 'm-1' },
    scopeKind: 'session',
    createdAt: '2026-04-26T00:00:00Z',
    ...overrides,
  };
}

async function setup(allArtifacts: Artifact[]) {
  let lastFilter: ArtifactFilter | undefined;
  const router = createRouter({
    history: createMemoryHistory(),
    routes: [{ path: '/artifacts', component: ArtifactsView }],
  });
  await router.push('/artifacts');
  await router.isReady();

  const w = mount(ArtifactsView, {
    global: {
      plugins: [
        router,
        {
          install(app) {
            provideFakeClient(app, {
              artifacts: {
                list: async (filter: any) => {
                  lastFilter = filter;
                  return allArtifacts.filter((a) => {
                    if (filter?.scopeKind && a.scopeKind !== filter.scopeKind) {
                      return false;
                    }
                    if (filter?.source && a.source !== filter.source) {
                      return false;
                    }
                    if (
                      filter?.mimeTypePrefix &&
                      !a.mimeType.startsWith(filter.mimeTypePrefix)
                    ) {
                      return false;
                    }
                    return true;
                  });
                },
                get: async (id: string) => ({
                  artifact: allArtifacts.find((a) => a.id === id) ?? makeArtifact({ id }),
                  bytes: btoa('text'),
                }),
                promote: async (id: string) =>
                  allArtifacts.find((a) => a.id === id) ?? makeArtifact({ id }),
                remove: async () => undefined,
              } as any,
            });
          },
        },
      ],
    },
  });
  await flushPromises();
  return { w, getFilter: () => lastFilter };
}

describe('ArtifactsView (global)', () => {
  it('renders all artifacts in the table by default', async () => {
    const items: Artifact[] = [
      makeArtifact({ id: 'a1', title: 'first' }),
      makeArtifact({
        id: 'a2',
        title: 'second',
        scopeKind: 'project',
        source: 'tool_output',
      }),
    ];
    const { w } = await setup(items);
    expect(w.find('[data-testid="artifacts-row-a1"]').exists()).toBe(true);
    expect(w.find('[data-testid="artifacts-row-a2"]').exists()).toBe(true);
    w.unmount();
  });

  it('filters by scope when the project pill is picked', async () => {
    const items: Artifact[] = [
      makeArtifact({ id: 'a1', scopeKind: 'session' }),
      makeArtifact({
        id: 'a2',
        scopeKind: 'project',
        title: 'project-only',
      }),
    ];
    const { w, getFilter } = await setup(items);
    await w.find('[data-testid="artifacts-pill-scope-project"]').trigger('click');
    await flushPromises();
    expect(getFilter()).toEqual({ scopeKind: 'project' });
    expect(w.find('[data-testid="artifacts-row-a1"]').exists()).toBe(false);
    expect(w.find('[data-testid="artifacts-row-a2"]').exists()).toBe(true);
    w.unmount();
  });

  it('filters by source when the tool_output pill is picked', async () => {
    const items: Artifact[] = [
      makeArtifact({ id: 'a1', source: 'code_block' }),
      makeArtifact({ id: 'a2', source: 'tool_output' }),
      makeArtifact({ id: 'a3', source: 'user_pin' }),
    ];
    const { w, getFilter } = await setup(items);
    await w.find('[data-testid="artifacts-pill-source-tool_output"]').trigger('click');
    await flushPromises();
    expect(getFilter()).toEqual({ source: 'tool_output' });
    expect(w.find('[data-testid="artifacts-row-a1"]').exists()).toBe(false);
    expect(w.find('[data-testid="artifacts-row-a2"]').exists()).toBe(true);
    expect(w.find('[data-testid="artifacts-row-a3"]').exists()).toBe(false);
    w.unmount();
  });

  it('renders empty state when no artifacts match the filter', async () => {
    const { w } = await setup([]);
    expect(w.find('[data-testid="artifacts-empty"]').exists()).toBe(true);
    w.unmount();
  });

  it('opens the preview modal when a row is clicked', async () => {
    const items = [makeArtifact({ id: 'a1' })];
    const { w } = await setup(items);
    await w.find('[data-testid="artifacts-row-a1"]').trigger('click');
    await flushPromises();
    expect(w.find('[data-testid="artifact-preview"]').exists()).toBe(true);
    w.unmount();
  });
});
