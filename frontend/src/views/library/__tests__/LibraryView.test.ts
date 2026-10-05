/**
 * LibraryView — artifacts-as-units-01DOGF0C WP06 (spec FR-7, pin P-7).
 *
 * - /artifacts and /documents land in the right Library view, in BOTH entry
 *   points' REAL route tables (not a hand-copied router), with the query
 *   string kept (DocumentsView reads ?session=); a persisted lastRoute of an
 *   old path resolves the same way because restoreLastRoute is a plain
 *   router.replace.
 * - The Library renders Captured or Authored by route, and its one search
 *   box filters the visible view by title.
 */
import { describe, it, expect, vi, beforeAll } from 'vitest';
import { mount, flushPromises } from '@vue/test-utils';
import { createMemoryHistory, createRouter, type RouteRecordRaw } from 'vue-router';
import { defineComponent, h } from 'vue';
import LibraryView from '@/views/library/LibraryView.vue';
import ArtifactsView from '@/views/artifacts/ArtifactsView.vue';
import { provideFakeClient } from '@/lib/harnessClientContext';
import { filterByTitle, matchesTitle } from '@/views/library/titleFilter';
import type { Artifact } from '@/lib/types';

vi.mock('@/App.vue', () => ({ default: { name: 'AppStub', render: () => null } }));
vi.mock('@/lib/harnessClient', async (importOriginal) => {
  const actual = await importOriginal<typeof import('@/lib/harnessClient')>();
  const make = () => actual.createFakeHarnessClient();
  return { ...actual, createHarnessClient: make, createServedHarnessClient: make };
});

let desktop: RouteRecordRaw[];
let served: RouteRecordRaw[];

beforeAll(async () => {
  document.body.innerHTML = '<div id="app"></div>';
  desktop = (await import('../../../main')).routes;
  document.body.innerHTML = '<div id="app"></div>';
  served = (await import('../../../main-served')).routes;
});

describe('Library routes (P-7)', () => {
  it.each(['desktop', 'served'] as const)('old paths redirect into the Library views (%s)', async (which) => {
    const routes = which === 'desktop' ? desktop : served;
    const cases: [string, string, Record<string, string>][] = [
      ['/artifacts', '/library/captured', {}],
      ['/documents', '/library/authored', {}],
      ['/documents?session=sess-42', '/library/authored', { session: 'sess-42' }],
      ['/library', '/library/captured', {}],
    ];
    for (const [from, to, query] of cases) {
      const router = createRouter({ history: createMemoryHistory(), routes });
      await router.push(from);
      await router.isReady();
      expect(router.currentRoute.value.path, `${which} ${from}`).toBe(to);
      expect(router.currentRoute.value.name, `${which} ${from}`).toBe('library');
      expect(router.currentRoute.value.query, `${which} ${from}`).toEqual(query);
    }
  });

  it('an unknown library view is not silently mapped to a real one', async () => {
    const router = createRouter({ history: createMemoryHistory(), routes: desktop });
    await router.push('/library/elsewhere');
    await router.isReady();
    expect(router.currentRoute.value.name).not.toBe('library');
  });
});

describe('titleFilter', () => {
  it('matches case-insensitively and treats a blank query as match-all', () => {
    expect(matchesTitle('Quarterly Report', 'report')).toBe(true);
    expect(matchesTitle('Quarterly Report', '  ')).toBe(true);
    expect(matchesTitle('Quarterly Report', undefined)).toBe(true);
    expect(matchesTitle('Quarterly Report', 'memo')).toBe(false);
    expect(filterByTitle([{ title: 'a b' }, { title: 'c' }], 'B')).toEqual([{ title: 'a b' }]);
  });
});

const CapturedStub = defineComponent({
  props: { embedded: Boolean, query: String },
  setup: (p) => () => h('div', { 'data-testid': 'captured-stub' }, `${p.embedded}|${p.query}`),
});
const AuthoredStub = defineComponent({
  props: { embedded: Boolean, query: String },
  setup: (p) => () => h('div', { 'data-testid': 'authored-stub' }, `${p.embedded}|${p.query}`),
});

async function mountLibrary(path: string) {
  const router = createRouter({
    history: createMemoryHistory(),
    routes: [{ path: '/library/:view(captured|authored)', name: 'library', component: LibraryView }],
  });
  await router.push(path);
  await router.isReady();
  const w = mount(LibraryView, {
    global: {
      plugins: [router],
      stubs: { ArtifactsView: CapturedStub, DocumentsView: AuthoredStub },
    },
  });
  await flushPromises();
  return { w, router };
}

describe('LibraryView shell', () => {
  it('renders Captured on /library/captured and Authored on /library/authored', async () => {
    const { w, router } = await mountLibrary('/library/captured');
    expect(w.find('[data-testid="captured-stub"]').exists()).toBe(true);
    expect(w.find('[data-testid="authored-stub"]').exists()).toBe(false);
    expect(w.find('[data-testid="library-tab-captured"]').attributes('aria-current')).toBe('page');
    await router.push('/library/authored');
    await flushPromises();
    expect(w.find('[data-testid="authored-stub"]').exists()).toBe(true);
    expect(w.find('[data-testid="captured-stub"]').exists()).toBe(false);
    expect(w.find('[data-testid="library-tab-authored"]').attributes('aria-current')).toBe('page');
    w.unmount();
  });

  it('passes embedded + the shared search query to the active view', async () => {
    const { w } = await mountLibrary('/library/authored');
    await w.find('[data-testid="library-search"]').setValue('plan');
    expect(w.find('[data-testid="authored-stub"]').text()).toBe('true|plan');
    w.unmount();
  });
});

describe('Captured view under the Library search', () => {
  function art(id: string, title: string, extra: Partial<Artifact> = {}): Artifact {
    return {
      id, title, sessionId: 's', mimeType: 'text/plain', contentHash: 'h', byteSize: 1,
      source: 'code_block', sourceRef: { messageId: 'm' }, scopeKind: 'session',
      createdAt: '2026-10-04T00:00:00Z', ...extra,
    };
  }

  it('filters rows by title, hides its own page head, and offers global + model_output pills', async () => {
    const items = [art('a1', 'Quarterly report'), art('a2', 'Logo render', { source: 'model_output', scopeKind: 'global' })];
    const router = createRouter({ history: createMemoryHistory(), routes: [{ path: '/', component: ArtifactsView }] });
    await router.push('/');
    await router.isReady();
    const w = mount(ArtifactsView, {
      props: { embedded: true, query: 'quarterly' },
      global: {
        plugins: [router, { install(app) { provideFakeClient(app, { artifacts: { list: async () => items } as any }); } }],
      },
    });
    await flushPromises();
    expect(w.find('[data-testid="artifacts-row-a1"]').exists()).toBe(true);
    expect(w.find('[data-testid="artifacts-row-a2"]').exists()).toBe(false);
    expect(w.text()).not.toContain('Code blocks, tool outputs, and pinned snippets');
    expect(w.find('[data-testid="artifacts-pill-scope-global"]').exists()).toBe(true);
    expect(w.find('[data-testid="artifacts-pill-source-model_output"]').exists()).toBe(true);
    await w.setProps({ query: '' });
    expect(w.find('[data-testid="artifacts-row-a2"]').exists()).toBe(true);
    w.unmount();
  });
});
