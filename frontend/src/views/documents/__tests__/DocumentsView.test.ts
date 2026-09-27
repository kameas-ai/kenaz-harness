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

function deferred<T>() {
  let resolve!: (v: T) => void;
  let reject!: (e: unknown) => void;
  const promise = new Promise<T>((res, rej) => {
    resolve = res;
    reject = rej;
  });
  return { promise, resolve, reject };
}

async function setup(opts: {
  sessions?: Session[] | Error;
  createSession?: (name: string) => Promise<Session>;
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
                create:
                  opts.createSession ??
                  (async () => {
                    throw new Error('create not stubbed');
                  }),
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

describe('DocumentsView — creating a session without a model', () => {
  it('creates a session from the empty state and starts working in it', async () => {
    const pending = deferred<Session>();
    const createSession = vi.fn(() => pending.promise);
    const list = vi.fn(async () => []);
    const { w, router } = await setup({ sessions: [], createSession, documents: { list } });

    await w.find('[data-testid="documents-new-session-name"]').setValue('Team handbook');
    await w.find('[data-testid="documents-create-session"]').trigger('submit');
    await flushPromises();
    expect(createSession).toHaveBeenCalledWith('Team handbook');
    const button = w.find('[data-testid="documents-create-session"]');
    expect(button.text()).toBe('Creating…');
    expect(button.attributes('disabled')).toBeDefined();

    pending.resolve({ ...SESSION, id: 'sess-new', name: 'Team handbook' });
    await flushPromises();
    expect(w.find('[data-testid="documents-no-sessions"]').exists()).toBe(false);
    expect((w.find('[data-testid="documents-session-select"]').element as HTMLSelectElement).value).toBe('sess-new');
    expect(list).toHaveBeenCalledWith('sess-new');
    expect(router.currentRoute.value.query.session).toBe('sess-new');
    expect(w.find('[data-testid="documents-new"]').attributes('disabled')).toBeUndefined();
    expect(w.find('[data-testid="documents-empty"]').exists()).toBe(true);
  });

  it('shows why a session could not be created and lets the user retry', async () => {
    const createSession = vi
      .fn()
      .mockRejectedValueOnce(new Error('servedTransport: Sessions_Create: database is locked'))
      .mockResolvedValueOnce({ ...SESSION, id: 'sess-2' });
    const { w } = await setup({ sessions: [], createSession, documents: {} });

    await w.find('[data-testid="documents-create-session"]').trigger('submit');
    await flushPromises();
    expect(createSession).toHaveBeenCalledWith('Documents');
    expect(w.find('[data-testid="documents-create-session-error"]').text()).toContain('database is locked');
    expect(w.find('[data-testid="documents-create-session"]').attributes('disabled')).toBeUndefined();

    await w.find('[data-testid="documents-create-session"]').trigger('submit');
    await flushPromises();
    expect(w.find('[data-testid="documents-no-sessions"]').exists()).toBe(false);
  });

  it('offers a New session action when sessions already exist', async () => {
    const createSession = vi.fn(async () => ({ ...SESSION, id: 'sess-3', name: 'Documents' }));
    const list = vi.fn(async () => []);
    const { w } = await setup({ createSession, documents: { list } });
    await w.find('[data-testid="documents-create-session-inline"]').trigger('click');
    await flushPromises();
    expect((w.find('[data-testid="documents-session-select"]').element as HTMLSelectElement).value).toBe('sess-3');
    expect(list).toHaveBeenLastCalledWith('sess-3');
  });
});

describe('DocumentsView — stale responses', () => {
  const SESSION_B = { ...SESSION, id: 'sess-b', name: 'Other', updatedAt: '2026-08-01T00:00:00Z' } as Session;
  const docA = record({ id: '01K000000000000000000000AA', title: 'Only in A' });
  const docB = record({ id: '01K000000000000000000000BB', title: 'Only in B' });

  it('drops a slow list for the previous session after switching', async () => {
    const slowA = deferred<DocumentSummary[]>();
    let firstA = true;
    const list = vi.fn((sid: string) => {
      if (sid === 'sess-1' && firstA) {
        firstA = false;
        return slowA.promise;
      }
      return Promise.resolve(sid === 'sess-b' ? [summary(docB)] : [summary(docA)]);
    });
    const { w } = await setup({ sessions: [SESSION, SESSION_B], documents: { list } });

    await w.find('[data-testid="documents-session-select"]').setValue('sess-b');
    await flushPromises();
    expect(w.find(`[data-testid="documents-open-${docB.id}"]`).exists()).toBe(true);

    slowA.resolve([summary(docA)]);
    await flushPromises();
    expect(w.find(`[data-testid="documents-open-${docA.id}"]`).exists()).toBe(false);
    expect(w.find(`[data-testid="documents-open-${docB.id}"]`).exists()).toBe(true);
  });

  it('shows the document opened last even if an earlier open resolves later', async () => {
    const doc1 = record({ id: '01K0000000000000000000001A', title: 'First' });
    const doc2 = record({ id: '01K0000000000000000000002A', title: 'Second' });
    const slow1 = deferred<DocumentRecord>();
    const get = vi.fn((_sid: string, id: string) => (id === doc1.id ? slow1.promise : Promise.resolve(doc2)));
    const { w } = await setup({ documents: { list: async () => [summary(doc1), summary(doc2)], get } });

    await w.find(`[data-testid="documents-open-${doc1.id}"]`).trigger('click');
    await w.find(`[data-testid="documents-open-${doc2.id}"]`).trigger('click');
    await flushPromises();
    expect(w.find('[data-testid="documents-reader-title"]').text()).toBe('Second');

    slow1.resolve(doc1);
    await flushPromises();
    expect(w.find('[data-testid="documents-reader-title"]').text()).toBe('Second');
  });

  it('does not let a late save replace the document the user moved on to', async () => {
    const other = record({ id: '01K0000000000000000000003A', title: 'Moved on' });
    const pendingSave = deferred<DocumentRecord>();
    const update = vi.fn(() => pendingSave.promise);
    const get = vi.fn(async (_sid: string, id: string) => (id === other.id ? other : docA));
    const { w } = await setup({
      documents: { list: async () => [summary(docA), summary(other)], get, update },
    });

    await w.find(`[data-testid="documents-open-${docA.id}"]`).trigger('click');
    await flushPromises();
    await w.find('[data-testid="documents-edit"]').trigger('click');
    await w.find('[data-testid="documents-source"]').setValue('<p>edit of A</p>');
    await w.find('[data-testid="documents-save"]').trigger('click');
    await w.find(`[data-testid="documents-open-${other.id}"]`).trigger('click');
    await flushPromises();
    expect(w.find('[data-testid="documents-reader-title"]').text()).toBe('Moved on');

    pendingSave.resolve(record({ ...docA, version: 3, body: '<p>edit of A</p>' }));
    await flushPromises();
    expect(update).toHaveBeenCalledWith('sess-1', docA.id, docA.version, '<p>edit of A</p>');
    expect(w.find('[data-testid="documents-reader-title"]').text()).toBe('Moved on');
    expect(w.find('[data-testid="documents-editor"]').exists()).toBe(false);
  });

  it('does not show a late version-conflict banner over a newly started document', async () => {
    const pendingSave = deferred<DocumentRecord>();
    const { w } = await setup({
      documents: {
        list: async () => [summary(docA)],
        get: async () => docA,
        update: () => pendingSave.promise,
      },
    });
    await w.find(`[data-testid="documents-open-${docA.id}"]`).trigger('click');
    await flushPromises();
    await w.find('[data-testid="documents-edit"]').trigger('click');
    await w.find('[data-testid="documents-source"]').setValue('<p>mine</p>');
    await w.find('[data-testid="documents-save"]').trigger('click');
    await w.find('[data-testid="documents-new"]').trigger('click');
    await w.find('[data-testid="documents-source"]').setValue('fresh draft');

    pendingSave.reject(new Error('documents: version_conflict: changed'));
    await flushPromises();
    expect(w.find('[data-testid="documents-conflict"]').exists()).toBe(false);
    expect((w.find('[data-testid="documents-source"]').element as HTMLTextAreaElement).value).toBe('fresh draft');
    expect(w.find('[data-testid="documents-save"]').attributes('disabled')).toBeUndefined();
  });

  it('drops a site build that resolves after switching session', async () => {
    const pendingBuild = deferred<KnowledgeSiteBuild>();
    const list = vi.fn(async (sid: string) => (sid === 'sess-b' ? [summary(docB)] : [summary(docA)]));
    const { w } = await setup({
      sessions: [SESSION, SESSION_B],
      documents: { list, buildSite: () => pendingBuild.promise },
    });
    await w.find(`[data-testid="documents-select-${docA.id}"]`).setValue(true);
    await w.find('[data-testid="documents-site-slug"]').setValue('kb-a');
    await w.find('[data-testid="documents-build-site"]').trigger('click');
    await w.find('[data-testid="documents-session-select"]').setValue('sess-b');
    await flushPromises();

    pendingBuild.resolve({
      siteDir: '/workspace/documents-exports/kb-a',
      publicDir: '/workspace/documents-exports/kb-a/public',
      bundle: '/workspace/documents-exports/kb-a.tar.gz',
      bundleSha256: 'x',
      documents: 1,
      warnings: [],
      published: false,
    });
    await flushPromises();
    expect(w.find('[data-testid="documents-site-result"]').exists()).toBe(false);
    expect(w.find('[data-testid="documents-build-site"]').text()).toBe('Build site');
  });
});
