/**
 * KnowledgeView — knowledge-home-01DOGF0E WP02 (spec FR-1..FR-3, pin P-1).
 *
 * - /contexts and /memory (and /corpora/*, /memory/<id>, /knowledge) land in
 *   the right Knowledge section, in BOTH entry points' REAL route tables (not
 *   a hand-copied router), with the query string kept (MemoryView reads
 *   ?scopeKind=/?scopeId=). A persisted lastRoute of an old path resolves the
 *   same way because restoreLastRoute is a plain router.replace.
 * - The view renders Curated or Learned by route, mounts the existing view
 *   `embedded`, and states each section's mechanic in one line (FR-3).
 * - The palette offers one Knowledge action plus the two sections, and no
 *   longer offers Contexts/Memory.
 */
import { describe, it, expect, vi, beforeAll } from 'vitest';
import { mount, flushPromises } from '@vue/test-utils';
import { createMemoryHistory, createRouter, type RouteRecordRaw } from 'vue-router';
import { defineComponent, h } from 'vue';
import KnowledgeView from '@/views/knowledge/KnowledgeView.vue';
import { useCommandPalette } from '@/lib/useCommandPalette';

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

describe('Knowledge routes (P-1, FR-2)', () => {
  it.each(['desktop', 'served'] as const)('old paths redirect into the Knowledge sections (%s)', async (which) => {
    const routes = which === 'desktop' ? desktop : served;
    const cases: [string, string, Record<string, string>][] = [
      ['/contexts', '/knowledge/curated', {}],
      ['/memory', '/knowledge/learned', {}],
      ['/memory?scopeKind=project&scopeId=p1', '/knowledge/learned', { scopeKind: 'project', scopeId: 'p1' }],
      ['/memory/chunk-123', '/knowledge/learned', { chunk: 'chunk-123' }],
      ['/corpora/x', '/knowledge/curated', {}],
      ['/knowledge', '/knowledge/curated', {}],
      ['/knowledge/learned', '/knowledge/learned', {}],
    ];
    for (const [from, to, query] of cases) {
      const router = createRouter({ history: createMemoryHistory(), routes });
      await router.push(from);
      await router.isReady();
      expect(router.currentRoute.value.path, `${which} ${from}`).toBe(to);
      expect(router.currentRoute.value.name, `${which} ${from}`).toBe('knowledge');
      expect(router.currentRoute.value.query, `${which} ${from}`).toEqual(query);
    }
  });

  it('a persisted lastRoute of /contexts or /memory resolves via router.replace', async () => {
    for (const [stored, to] of [['/contexts', '/knowledge/curated'], ['/memory', '/knowledge/learned']]) {
      const router = createRouter({ history: createMemoryHistory(), routes: desktop });
      await router.push('/sessions');
      await router.isReady();
      await router.replace(stored);
      expect(router.currentRoute.value.path, stored).toBe(to);
    }
  });

  it('an unknown knowledge section is not silently mapped to a real one', async () => {
    const router = createRouter({ history: createMemoryHistory(), routes: desktop });
    await router.push('/knowledge/elsewhere');
    await router.isReady();
    expect(router.currentRoute.value.name).not.toBe('knowledge');
  });
});

const CuratedStub = defineComponent({
  props: { embedded: Boolean },
  setup: (p) => () => h('div', { 'data-testid': 'curated-stub' }, `embedded=${p.embedded}`),
});
const LearnedStub = defineComponent({
  props: { embedded: Boolean },
  setup: (p) => () => h('div', { 'data-testid': 'learned-stub' }, `embedded=${p.embedded}`),
});

async function mountKnowledge(path: string) {
  const router = createRouter({
    history: createMemoryHistory(),
    routes: [{ path: '/knowledge/:section(curated|learned)', name: 'knowledge', component: KnowledgeView }],
  });
  await router.push(path);
  await router.isReady();
  const w = mount(KnowledgeView, {
    global: {
      plugins: [router],
      stubs: { ContextsView: CuratedStub, MemoryView: LearnedStub },
    },
  });
  await flushPromises();
  return { w, router };
}

describe('KnowledgeView shell (FR-1, FR-3)', () => {
  it('renders Curated on /knowledge/curated and Learned on /knowledge/learned', async () => {
    const { w, router } = await mountKnowledge('/knowledge/curated');
    expect(w.find('[data-testid="curated-stub"]').text()).toBe('embedded=true');
    expect(w.find('[data-testid="learned-stub"]').exists()).toBe(false);
    expect(w.find('[data-testid="knowledge-tab-curated"]').attributes('aria-current')).toBe('page');
    expect(w.find('[data-testid="knowledge-explainer"]').text()).toBe(
      'Files you write; attached at conversation start or read on demand.',
    );
    await router.push('/knowledge/learned');
    await flushPromises();
    expect(w.find('[data-testid="learned-stub"]').text()).toBe('embedded=true');
    expect(w.find('[data-testid="curated-stub"]').exists()).toBe(false);
    expect(w.find('[data-testid="knowledge-tab-learned"]').attributes('aria-current')).toBe('page');
    expect(w.find('[data-testid="knowledge-explainer"]').text()).toBe(
      'Captured from your conversations; retrieved by similarity each turn.',
    );
    w.unmount();
  });

  it('switcher links carry the current query across sections', async () => {
    const { w } = await mountKnowledge('/knowledge/learned?scopeKind=project');
    expect(w.find('[data-testid="knowledge-tab-curated"]').attributes('href')).toContain(
      '/knowledge/curated?scopeKind=project',
    );
    w.unmount();
  });
});

describe('Knowledge palette entries (FR-2)', () => {
  it('offers Knowledge + both sections and no Contexts/Memory action', () => {
    const { actions } = useCommandPalette();
    const ids = actions.value.map((a) => a.id);
    expect(ids).toEqual(expect.arrayContaining(['nav.knowledge', 'nav.knowledge.curated', 'nav.knowledge.learned']));
    expect(ids).not.toContain('nav.contexts');
    expect(ids).not.toContain('nav.memory');
  });
});
