import { describe, it, expect, vi } from 'vitest';
import { mount, flushPromises } from '@vue/test-utils';
import { createFakeHarnessClient } from '@/lib/harnessClient';
import { HarnessClientKey } from '@/lib/harnessClientContext';
import type { Corpus, CorpusChunk, CorpusFile } from '@/lib/types';

vi.mock('vue-router', () => ({
  useRoute: () => ({ params: { id: 'c-a' }, query: {} }),
  useRouter: () => ({ push: vi.fn() }),
}));

// Imported AFTER the mock so the SUT picks up the stubbed module.
import CorpusDetail from '@/views/corpora/CorpusDetail.vue';

function makeCorpus(): Corpus {
  return {
    id: 'c-a',
    name: 'alpha',
    scope: 'global',
    createdAt: '2026-04-26T00:00:00Z',
    updatedAt: '2026-04-26T00:00:00Z',
  };
}

function makeFile(over: Partial<CorpusFile>): CorpusFile {
  return {
    id: 'f-1',
    corpusId: 'c-a',
    path: 'a.md',
    sha256: 'deadbeef',
    fileSize: 100,
    lineCount: 10,
    ingestedAt: '2026-04-26T00:00:00Z',
    ...over,
  };
}

function makeChunk(over: Partial<CorpusChunk>): CorpusChunk {
  return {
    id: 'ch-1',
    corpusId: 'c-a',
    fileId: 'f-1',
    chunkSeq: 0,
    text: 'hello world',
    provenance: {
      filePath: 'a.md',
      lineStart: 1,
      lineEnd: 3,
      sha256: 'deadbeef',
    },
    createdAt: '2026-04-26T00:00:00Z',
    ...over,
  };
}

interface MountOpts {
  files?: CorpusFile[];
  chunks?: CorpusChunk[];
}

function mountWith(opts: MountOpts = {}) {
  const files = opts.files ?? [];
  const chunks = opts.chunks ?? [];
  const listFiles = vi.fn(async () => files);
  const listChunks = vi.fn(async () => chunks);
  const getCorpus = vi.fn(async () => makeCorpus());

  const client = createFakeHarnessClient({
    corpus: {
      listCorpora: async () => [],
      createCorpus: async () => makeCorpus(),
      getCorpus,
      deleteCorpus: async () => undefined,
      listFiles,
      listChunks,
      ingestPath: async () => ({
        jobId: 'j',
        corpusId: 'c-a',
        state: 'completed',
        path: '',
        filesTotal: 0,
        filesDone: 0,
        filesSkip: 0,
        chunksTotal: 0,
        startedAt: '',
        updatedAt: '',
      }),
      jobStatus: async () => ({
        jobId: 'j',
        corpusId: 'c-a',
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

  const wrapper = mount(CorpusDetail, {
    global: {
      provide: { [HarnessClientKey as symbol]: client },
      stubs: {
        CanvasHead: { template: '<div class="canvas-head"><slot name="trailing" /></div>' },
        RouterLink: { template: '<a><slot /></a>' },
      },
    },
  });
  return { wrapper, listFiles, listChunks, getCorpus };
}

describe('CorpusDetail', () => {
  it('renders empty state when corpus has no files', async () => {
    const { wrapper, listFiles, getCorpus } = mountWith();
    await flushPromises();
    expect(getCorpus).toHaveBeenCalledWith('c-a');
    expect(listFiles).toHaveBeenCalledWith('c-a');
    expect(wrapper.find('[data-testid="corpus-detail-empty"]').exists()).toBe(
      true,
    );
  });

  it('renders one row per file and auto-selects the first', async () => {
    const files = [
      makeFile({ id: 'f-1', path: 'first.md' }),
      makeFile({ id: 'f-2', path: 'second.md' }),
    ];
    const chunks = [makeChunk({ id: 'ch-1', text: 'first chunk text' })];
    const { wrapper, listChunks } = mountWith({ files, chunks });
    await flushPromises();
    expect(wrapper.find('[data-testid="corpus-file-f-1"]').exists()).toBe(
      true,
    );
    expect(wrapper.find('[data-testid="corpus-file-f-2"]').exists()).toBe(
      true,
    );
    expect(listChunks).toHaveBeenCalledWith('c-a', 'f-1');
    expect(wrapper.find('[data-testid="corpus-chunk-ch-1"]').exists()).toBe(
      true,
    );
    expect(wrapper.html()).toContain('first chunk text');
  });

  it('switches the chunk pane when the user picks a different file', async () => {
    const files = [
      makeFile({ id: 'f-1', path: 'first.md' }),
      makeFile({ id: 'f-2', path: 'second.md' }),
    ];
    const { wrapper, listChunks } = mountWith({ files });
    await flushPromises();
    listChunks.mockClear();
    await wrapper.get('[data-testid="corpus-file-f-2"]').trigger('click');
    await flushPromises();
    expect(listChunks).toHaveBeenCalledWith('c-a', 'f-2');
  });
});
