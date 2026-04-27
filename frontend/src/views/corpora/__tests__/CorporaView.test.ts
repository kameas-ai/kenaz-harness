import { describe, it, expect, vi } from 'vitest';
import { mount, flushPromises } from '@vue/test-utils';
import { createFakeHarnessClient } from '@/lib/harnessClient';
import { HarnessClientKey } from '@/lib/harnessClientContext';
import type { Corpus } from '@/lib/types';

vi.mock('vue-router', () => ({
  useRoute: () => ({ params: {}, query: {} }),
  useRouter: () => ({ push: vi.fn() }),
}));

import CorporaView from '@/views/corpora/CorporaView.vue';

interface MountOpts {
  corpora?: Corpus[];
  createImpl?: () => Promise<Corpus>;
  deleteImpl?: () => Promise<void>;
}

function makeCorpus(over: Partial<Corpus>): Corpus {
  return {
    id: 'c1',
    name: 'Wiki',
    scope: 'global',
    createdAt: '2026-04-26T00:00:00Z',
    updatedAt: '2026-04-26T00:00:00Z',
    ...over,
  };
}

function mountWith(opts: MountOpts = {}) {
  const corpora = opts.corpora ?? [];
  const listCorpora = vi.fn(async () => corpora);
  const createCorpus = vi.fn(
    opts.createImpl ??
      (async () => makeCorpus({ id: 'new', name: 'fresh' })),
  );
  const deleteCorpus = vi.fn(opts.deleteImpl ?? (async () => undefined));

  const client = createFakeHarnessClient({
    corpus: {
      listCorpora,
      createCorpus,
      getCorpus: async (id) => makeCorpus({ id }),
      deleteCorpus,
      listFiles: async () => [],
      listChunks: async () => [],
      ingestPath: async (id, path) => ({
        jobId: 'j',
        corpusId: id,
        state: 'pending',
        path,
        filesTotal: 0,
        filesDone: 0,
        filesSkip: 0,
        chunksTotal: 0,
        startedAt: '',
        updatedAt: '',
      }),
      jobStatus: async () => ({
        jobId: 'j',
        corpusId: 'c1',
        state: 'completed',
        path: '',
        filesTotal: 0,
        filesDone: 0,
        filesSkip: 0,
        chunksTotal: 0,
        startedAt: '',
        updatedAt: '',
      }),
      retrieve: async () => ({ results: [], dropped: 0 }),
    },
  });

  const wrapper = mount(CorporaView, {
    global: {
      provide: { [HarnessClientKey as symbol]: client },
      stubs: {
        CanvasHead: { template: '<div class="canvas-head"><slot name="trailing" /></div>' },
        IngestModal: { template: '<div data-testid="ingest-modal-stub" />' },
        RouterLink: { template: '<a><slot /></a>' },
      },
    },
  });
  return { wrapper, listCorpora, createCorpus, deleteCorpus };
}

describe('CorporaView', () => {
  it('renders the empty state when no corpora exist', async () => {
    const { wrapper, listCorpora } = mountWith();
    await flushPromises();
    expect(listCorpora).toHaveBeenCalled();
    expect(wrapper.find('[data-testid="corpora-empty"]').exists()).toBe(true);
  });

  it('renders one row per corpus with delete + ingest affordances', async () => {
    const corpora = [
      makeCorpus({ id: 'c-a', name: 'alpha' }),
      makeCorpus({ id: 'c-b', name: 'beta', scope: 'project', scopeId: 'p1' }),
    ];
    const { wrapper } = mountWith({ corpora });
    await flushPromises();
    expect(wrapper.find('[data-testid="corpus-row-c-a"]').exists()).toBe(true);
    expect(wrapper.find('[data-testid="corpus-row-c-b"]').exists()).toBe(true);
    expect(wrapper.find('[data-testid="corpus-delete-c-a"]').exists()).toBe(
      true,
    );
    expect(wrapper.find('[data-testid="corpus-ingest-c-a"]').exists()).toBe(
      true,
    );
    expect(wrapper.html()).toContain('alpha');
    expect(wrapper.html()).toContain('beta');
  });

  it('opens the create modal and submits a CreateRequest', async () => {
    const { wrapper, createCorpus } = mountWith();
    await flushPromises();
    await wrapper.get('[data-testid="corpora-create"]').trigger('click');
    expect(wrapper.find('[data-testid="corpora-create-modal"]').exists()).toBe(
      true,
    );
    await wrapper
      .get('[data-testid="corpora-create-name"]')
      .setValue('My Wiki');
    await wrapper.get('[data-testid="corpora-create-submit"]').trigger('click');
    await flushPromises();
    expect(createCorpus).toHaveBeenCalledWith(
      expect.objectContaining({ name: 'My Wiki', scope: 'global' }),
    );
  });

  it('rejects empty name on create', async () => {
    const { wrapper, createCorpus } = mountWith();
    await flushPromises();
    await wrapper.get('[data-testid="corpora-create"]').trigger('click');
    await wrapper.get('[data-testid="corpora-create-submit"]').trigger('click');
    await flushPromises();
    expect(createCorpus).not.toHaveBeenCalled();
  });

  it('confirms before deleting and forwards the corpus id', async () => {
    const corpora = [makeCorpus({ id: 'c-a', name: 'alpha' })];
    const { wrapper, deleteCorpus } = mountWith({ corpora });
    await flushPromises();
    const confirmSpy = vi
      .spyOn(window, 'confirm')
      .mockImplementation(() => true);
    await wrapper.get('[data-testid="corpus-delete-c-a"]').trigger('click');
    await flushPromises();
    expect(confirmSpy).toHaveBeenCalled();
    expect(deleteCorpus).toHaveBeenCalledWith('c-a');
    confirmSpy.mockRestore();
  });
});
