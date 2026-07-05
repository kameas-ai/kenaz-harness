import { describe, it, expect, vi } from 'vitest';
import { mount, flushPromises } from '@vue/test-utils';
import IngestModal from '@/views/corpora/IngestModal.vue';
import { createFakeHarnessClient } from '@/lib/harnessClient';
import { HarnessClientKey } from '@/lib/harnessClientContext';
import type { Corpus, CorpusIngestStatus } from '@/lib/types';

function makeCorpus(): Corpus {
  return {
    id: 'c-a',
    name: 'alpha',
    scope: 'global',
    createdAt: '2026-04-26T00:00:00Z',
    updatedAt: '2026-04-26T00:00:00Z',
  };
}

interface MountOpts {
  ingestImpl?: () => Promise<CorpusIngestStatus>;
  jobStatusImpl?: () => Promise<CorpusIngestStatus>;
}

function mountWith(opts: MountOpts = {}) {
  const ingestPath = vi.fn(
    opts.ingestImpl ??
      (async () => ({
        jobId: 'job-1',
        corpusId: 'c-a',
        state: 'pending',
        path: '/x',
        filesTotal: 0,
        filesDone: 0,
        filesSkip: 0,
        chunksTotal: 0,
        startedAt: '',
        updatedAt: '',
      })),
  );
  const jobStatus = vi.fn(
    opts.jobStatusImpl ??
      (async () => ({
        jobId: 'job-1',
        corpusId: 'c-a',
        state: 'completed',
        path: '/x',
        filesTotal: 1,
        filesDone: 1,
        filesSkip: 0,
        chunksTotal: 3,
        startedAt: '',
        updatedAt: '',
      })),
  );

  const client = createFakeHarnessClient({
    corpus: {
      listCorpora: async () => [],
      createCorpus: async () => makeCorpus(),
      getCorpus: async () => makeCorpus(),
      deleteCorpus: async () => undefined,
      listFiles: async () => [],
      listChunks: async () => [],
      ingestPath,
      jobStatus,
      retrieve: async () => ({ results: [], dropped: 0 }),
    // eslint-disable-next-line @typescript-eslint/no-explicit-any
    } as any,
  });

  const wrapper = mount(IngestModal, {
    props: { corpus: makeCorpus() },
    global: {
      provide: { [HarnessClientKey as symbol]: client },
    },
  });
  return { wrapper, ingestPath, jobStatus };
}

describe('IngestModal', () => {
  it('rejects empty path', async () => {
    const { wrapper, ingestPath } = mountWith();
    await flushPromises();
    await wrapper.get('[data-testid="corpus-ingest-submit"]').trigger('click');
    await flushPromises();
    expect(ingestPath).not.toHaveBeenCalled();
    expect(wrapper.find('[data-testid="corpus-ingest-error"]').exists()).toBe(
      true,
    );
  });

  it('forwards path + recursive + chunkLines to ingestPath', async () => {
    const { wrapper, ingestPath } = mountWith();
    await flushPromises();
    await wrapper.get('[data-testid="corpus-ingest-path"]').setValue('/abs/path');
    await wrapper
      .get('[data-testid="corpus-ingest-chunk-lines"]')
      .setValue(80);
    await wrapper.get('[data-testid="corpus-ingest-submit"]').trigger('click');
    await flushPromises();
    expect(ingestPath).toHaveBeenCalledWith(
      'c-a',
      '/abs/path',
      expect.objectContaining({ recursive: true, chunkLines: 80 }),
    );
  });

  it('emits complete when the polled status reaches completed', async () => {
    const { wrapper } = mountWith({
      jobStatusImpl: async () => ({
        jobId: 'job-1',
        corpusId: 'c-a',
        state: 'completed',
        path: '/x',
        filesTotal: 1,
        filesDone: 1,
        filesSkip: 0,
        chunksTotal: 3,
        startedAt: '',
        updatedAt: '',
      }),
    });
    await flushPromises();
    await wrapper.get('[data-testid="corpus-ingest-path"]').setValue('/x');
    await wrapper.get('[data-testid="corpus-ingest-submit"]').trigger('click');
    await flushPromises();
    // Allow the setTimeout-driven poll to settle.
    await new Promise((r) => setTimeout(r, 300));
    await flushPromises();
    expect(wrapper.emitted('complete')).toBeTruthy();
  });

  it('emits close when the cancel button fires', async () => {
    const { wrapper } = mountWith();
    await flushPromises();
    await wrapper.get('button.text-ink-dim').trigger('click');
    expect(wrapper.emitted('close')).toBeTruthy();
  });
});
