import { describe, it, expect, vi, beforeEach } from 'vitest';
import { mount, flushPromises } from '@vue/test-utils';
import { createMemoryHistory, createRouter } from 'vue-router';
import type {
  DocumentRecord,
  DocumentSummary,
  KnowledgeSiteBuild,
  Session,
} from '@/lib/types';

const servedFlag = vi.hoisted(() => ({ value: false }));
vi.mock('@/lib/useServedMode', async (importOriginal) => {
  const actual = await importOriginal<typeof import('@/lib/useServedMode')>();
  return { ...actual, isServedMode: () => servedFlag.value };
});

import DocumentsView from '@/views/documents/DocumentsView.vue';
import { provideFakeClient } from '@/lib/harnessClientContext';
import type { DocumentsClient } from '@/lib/harnessClient';

const SESSION: Session = {
  id: 'sess-1',
  name: 'Runbooks',
  createdAt: '2026-09-01T00:00:00Z',
  updatedAt: '2026-09-02T00:00:00Z',
} as Session;

function record(overrides: Partial<DocumentRecord> = {}): DocumentRecord {
  return {
    id: '01K0000000000000000000000A',
    title: 'On-call runbook',
    scope: 'session',
    version: 2,
    contentSha256: 'abc',
    byteSize: 20,
    createdAt: '2026-09-01T00:00:00Z',
    updatedAt: '2026-09-02T00:00:00Z',
    body: '<h1>On-call</h1><p>Page primary.</p>',
    ...overrides,
  };
}

function summary(r: DocumentRecord): DocumentSummary {
  const { body: _body, ...rest } = r;
  return rest;
}

const flushPreview = async () => {
  await new Promise((r) => setTimeout(r, 350));
  await flushPromises();
};

async function setup(opts: {
  sessions?: Session[] | Error;
  documents: Partial<DocumentsClient>;
}) {
  const router = createRouter({
    history: createMemoryHistory(),
    routes: [{ path: '/documents', component: DocumentsView }],
  });
  await router.push('/documents');
  await router.isReady();

  const documents: DocumentsClient = {
    list: async () => [],
    get: async () => record(),
    create: async () => record(),
    update: async () => record(),
    preview: async (body: string) => ({ html: body, sanitized: false, byteSize: body.length }),
    buildSite: async () => {
      throw new Error('documents: unavailable: not in this test');
    },
    exportsDir: async () => ({ dir: '/workspace/documents-exports' }),
    ...opts.documents,
  };

  const w = mount(DocumentsView, {
    global: {
      plugins: [
        router,
        {
          install(app) {
            provideFakeClient(app, {
              sessions: {
                list: async () => {
                  if (opts.sessions instanceof Error) throw opts.sessions;
                  return opts.sessions ?? [SESSION];
                },
              } as never,
              documents,
            });
          },
        },
      ],
    },
  });
  await flushPromises();
  return { w, router };
}

beforeEach(() => {
  servedFlag.value = false;
});

describe('DocumentsView — empty and error states', () => {
  it('explains that documents need a session when there are none', async () => {
    const { w } = await setup({ sessions: [], documents: {} });
    expect(w.find('[data-testid="documents-no-sessions"]').exists()).toBe(true);
  });

  it('shows a session load failure instead of an empty list', async () => {
    const { w } = await setup({ sessions: new Error('network down'), documents: {} });
    expect(w.find('[data-testid="documents-sessions-error"]').text()).toContain('network down');
  });

  it('shows the empty state for a session with no documents', async () => {
    const list = vi.fn(async () => []);
    const { w } = await setup({ documents: { list } });
    expect(list).toHaveBeenCalledWith('sess-1');
    expect(w.find('[data-testid="documents-empty"]').exists()).toBe(true);
  });

  it('shows the contract message when listing fails', async () => {
    const { w } = await setup({
      documents: {
        list: async () => {
          throw new Error('servedTransport: Documents_List: documents: no_session: no session with that id exists');
        },
      },
    });
    const err = w.find('[data-testid="documents-list-error"]');
    expect(err.text()).toBe('no session with that id exists');
  });

  it('shows why a document could not be opened', async () => {
    const doc = record();
    const { w } = await setup({
      documents: {
        list: async () => [summary(doc)],
        get: async () => {
          throw new Error('documents: document_not_found: no document with that id is visible in this session');
        },
      },
    });
    await w.find(`[data-testid="documents-open-${doc.id}"]`).trigger('click');
    await flushPromises();
    expect(w.find('[data-testid="documents-open-error"]').text()).toBe(
      'no document with that id is visible in this session',
    );
    expect(w.find('[data-testid="documents-reader"]').exists()).toBe(false);
  });
});

describe('DocumentsView — read, create, edit', () => {
  it('opens a document in the sandboxed reader', async () => {
    const doc = record();
    const get = vi.fn(async () => doc);
    const { w } = await setup({ documents: { list: async () => [summary(doc)], get } });
    await w.find(`[data-testid="documents-open-${doc.id}"]`).trigger('click');
    await flushPromises();
    expect(get).toHaveBeenCalledWith('sess-1', doc.id);
    expect(w.find('[data-testid="documents-reader-title"]').text()).toBe('On-call runbook');
    const frame = w.find('[data-testid="documents-reader"] iframe');
    expect(frame.attributes('sandbox')).toBe('');
    expect(frame.attributes('srcdoc')).toContain('Page primary.');
  });

  it('creates a Markdown document through the server preview and saves the converted HTML', async () => {
    const created = record({ id: '01K0000000000000000000000B', title: 'Launch brief', version: 0 });
    let listed: DocumentSummary[] = [];
    const preview = vi.fn(async (body: string) => ({
      html: body.replace(/<script>.*<\/script>/, ''),
      sanitized: body.includes('<script>'),
      byteSize: body.length,
    }));
    const create = vi.fn(async () => {
      listed = [summary(created)];
      return created;
    });
    const { w } = await setup({ documents: { list: async () => listed, preview, create } });

    await w.find('[data-testid="documents-new"]').trigger('click');
    await w.find('[data-testid="documents-title"]').setValue('Launch brief');
    await w.find('[data-testid="documents-source"]').setValue('# Launch\n\nGA **Oct 1**\n\n<script>x()</script>');
    await flushPreview();

    expect(preview).toHaveBeenCalled();
    const sent = preview.mock.calls.at(-1)![0];
    expect(sent).toContain('<h1>Launch</h1>');
    expect(sent).toContain('<strong>Oct 1</strong>');
    expect(w.find('[data-testid="documents-sanitized-note"]').exists()).toBe(true);

    await w.find('[data-testid="documents-save"]').trigger('click');
    await flushPromises();
    expect(create).toHaveBeenCalledWith('sess-1', 'Launch brief', sent);
    expect(w.find('[data-testid="documents-reader-title"]').text()).toBe('Launch brief');
    expect(w.find(`[data-testid="documents-open-${created.id}"]`).exists()).toBe(true);
  });

  it('shows a create validation error verbatim and stays in the editor', async () => {
    const { w } = await setup({
      documents: {
        create: async () => {
          throw new Error('documents: invalid_title: title must be 1-200 characters with no control characters');
        },
      },
    });
    await w.find('[data-testid="documents-new"]').trigger('click');
    await w.find('[data-testid="documents-source"]').setValue('hello');
    await w.find('[data-testid="documents-save"]').trigger('click');
    await flushPromises();
    expect(w.find('[data-testid="documents-save-error"]').text()).toContain('title must be');
    expect(w.find('[data-testid="documents-editor"]').exists()).toBe(true);
  });

  it('reports a version conflict, keeps the edits, and saves over the latest only when asked', async () => {
    const v2 = record({ version: 2 });
    const v3 = record({ version: 3, body: '<p>someone else</p>' });
    let getCalls = 0;
    const get = vi.fn(async () => (getCalls++ === 0 ? v2 : v3));
    const update = vi
      .fn()
      .mockRejectedValueOnce(
        new Error('servedTransport: Documents_Update: documents: version_conflict: this document changed since you opened it; reload to see the current version'),
      )
      .mockResolvedValueOnce(record({ version: 4, body: '<p>mine</p>' }));
    const { w } = await setup({ documents: { list: async () => [summary(v2)], get, update } });

    await w.find(`[data-testid="documents-open-${v2.id}"]`).trigger('click');
    await flushPromises();
    await w.find('[data-testid="documents-edit"]').trigger('click');
    await w.find('[data-testid="documents-source"]').setValue('<p>mine</p>');
    await w.find('[data-testid="documents-save"]').trigger('click');
    await flushPromises();

    expect(update).toHaveBeenNthCalledWith(1, 'sess-1', v2.id, 2, '<p>mine</p>');
    const banner = w.find('[data-testid="documents-conflict"]');
    expect(banner.text()).toContain('opened version 2');
    expect(banner.text()).toContain('now version 3');
    expect(banner.text()).toContain('Nothing was saved');
    expect((w.find('[data-testid="documents-source"]').element as HTMLTextAreaElement).value).toBe('<p>mine</p>');
    expect(w.find('[data-testid="documents-save"]').attributes('disabled')).toBeDefined();

    await w.find('[data-testid="documents-conflict-keep"]').trigger('click');
    await w.find('[data-testid="documents-save"]').trigger('click');
    await flushPromises();
    expect(update).toHaveBeenNthCalledWith(2, 'sess-1', v2.id, 3, '<p>mine</p>');
    expect(w.find('[data-testid="documents-conflict"]').exists()).toBe(false);
    expect(w.find('[data-testid="documents-reader"]').exists()).toBe(true);
  });

  it('can discard local edits and reload the latest version after a conflict', async () => {
    const v2 = record({ version: 2 });
    const v3 = record({ version: 3, body: '<p>someone else</p>' });
    let getCalls = 0;
    const get = vi.fn(async () => (getCalls++ === 0 ? v2 : v3));
    const update = vi.fn(async () => {
      throw new Error('documents: version_conflict: changed');
    });
    const { w } = await setup({ documents: { list: async () => [summary(v2)], get, update } });
    await w.find(`[data-testid="documents-open-${v2.id}"]`).trigger('click');
    await flushPromises();
    await w.find('[data-testid="documents-edit"]').trigger('click');
    await w.find('[data-testid="documents-source"]').setValue('<p>mine</p>');
    await w.find('[data-testid="documents-save"]').trigger('click');
    await flushPromises();
    await w.find('[data-testid="documents-conflict-reload"]').trigger('click');
    await flushPromises();
    expect((w.find('[data-testid="documents-source"]').element as HTMLTextAreaElement).value).toBe('<p>someone else</p>');
    expect(w.find('[data-testid="documents-editor"]').text()).toContain('editing from version 3');
  });
});

describe('DocumentsView — knowledge site', () => {
  const doc = record();
  const built: KnowledgeSiteBuild = {
    siteDir: "/workspace/documents-exports/team-kb",
    publicDir: "/workspace/documents-exports/team-kb/public",
    bundle: "/workspace/documents-exports/team-kb.tar.gz",
    bundleSha256: 'f00d',
    documents: 1,
    warnings: [{ documentId: doc.id, warnings: ['embedded image omitted'] }],
    published: false,
  };

  async function selectAndName(w: Awaited<ReturnType<typeof setup>>['w'], slug: string) {
    await w.find(`[data-testid="documents-select-${doc.id}"]`).setValue(true);
    await w.find('[data-testid="documents-site-slug"]').setValue(slug);
  }

  it('needs a selection and a valid name before it can build', async () => {
    const { w } = await setup({ documents: { list: async () => [summary(doc)] } });
    const button = () => w.find('[data-testid="documents-build-site"]');
    expect(button().attributes('disabled')).toBeDefined();
    await selectAndName(w, 'Bad--Name');
    expect(button().attributes('disabled')).toBeDefined();
    expect(w.find('[data-testid="documents-site-panel"]').text()).toContain('single hyphens');
    await w.find('[data-testid="documents-site-slug"]').setValue('team-kb');
    expect(button().attributes('disabled')).toBeUndefined();
    expect(w.find('[data-testid="documents-site-panel"]').text()).toContain('/workspace/documents-exports/team-kb');
  });

  it('builds the selected documents and shows real paths and serving instructions', async () => {
    const buildSite = vi.fn(async () => built);
    const { w } = await setup({ documents: { list: async () => [summary(doc)], buildSite } });
    await selectAndName(w, 'team-kb');
    await w.find('[data-testid="documents-site-title"]').setValue('Team KB');
    await w.find('[data-testid="documents-build-site"]').trigger('click');
    await flushPromises();

    expect(buildSite).toHaveBeenCalledWith('sess-1', 'team-kb', 'Team KB', [doc.id]);
    expect(w.find('[data-testid="documents-site-not-published"]').text()).toBe('Not published. Nothing was uploaded.');
    expect(w.find('[data-testid="documents-site-dir"]').text()).toBe(built.siteDir);
    expect(w.find('[data-testid="documents-site-public"]').text()).toBe(built.publicDir);
    expect(w.find('[data-testid="documents-site-bundle"]').text()).toBe(built.bundle);
    expect(w.find('[data-testid="documents-serve-local"]').text()).toBe(
      "python3 -m http.server 8080 --bind 127.0.0.1 --directory '/workspace/documents-exports/team-kb/public'",
    );
    expect(w.find('[data-testid="documents-serve-bundle"]').text()).toContain(
      "tar -xzf '/workspace/documents-exports/team-kb.tar.gz' -C team-kb",
    );
    expect(w.find('[data-testid="documents-site-warnings"]').text()).toContain('On-call runbook: embedded image omitted');
    // Desktop build: no workbench-mount note.
    expect(w.find('[data-testid="documents-serve-workbench"]').exists()).toBe(false);
  });

  it('explains the /workspace mapping only in a workbench build writing under /workspace', async () => {
    servedFlag.value = true;
    const { w } = await setup({ documents: { list: async () => [summary(doc)], buildSite: async () => built } });
    await selectAndName(w, 'team-kb');
    await w.find('[data-testid="documents-build-site"]').trigger('click');
    await flushPromises();
    expect(w.find('[data-testid="documents-serve-workbench"]').text()).toContain('documents-exports/team-kb');
  });

  it('shows a build failure without a result', async () => {
    const { w } = await setup({
      documents: {
        list: async () => [summary(doc)],
        buildSite: async () => {
          throw new Error(
            'documents: export_target_in_use: a different folder already uses that site name in the exports directory; pick another name',
          );
        },
      },
    });
    await selectAndName(w, 'team-kb');
    await w.find('[data-testid="documents-build-site"]').trigger('click');
    await flushPromises();
    expect(w.find('[data-testid="documents-site-error"]').text()).toContain('pick another name');
    expect(w.find('[data-testid="documents-site-result"]').exists()).toBe(false);
  });
});
