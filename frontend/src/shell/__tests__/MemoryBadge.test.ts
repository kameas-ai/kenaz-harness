/**
 * MemoryBadge — v0.5.6 memory-trust-signals unit tests.
 *
 * Covers:
 *   1. Shows "…" while loading (chunkCount null).
 *   2. Shows global count from healthSnapshot when no projectId.
 *   3. Shows project-scoped count from listChunks when projectId supplied.
 *   4. Formats count correctly: 0, 845, 1.2k, 12k.
 *   5. Click with non-zero count navigates to /memory.
 *   6. Click with non-zero count + projectId navigates to /memory?project=<id>.
 *   7. Click with zero count opens the onboarding modal.
 *   8. Zero-modal "Got it" button closes the modal.
 *   9. Tooltip text references chunk count.
 *  10. Polls again after 30 s.
 */
import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest';
import { mount, flushPromises } from '@vue/test-utils';
import { createMemoryHistory, createRouter } from 'vue-router';
import { defineComponent, h } from 'vue';
import MemoryBadge from '@/shell/MemoryBadge.vue';
import { provideFakeClient } from '@/lib/harnessClientContext';

const memRoute = {
  path: '/memory',
  component: defineComponent({ render: () => h('div', 'memory') }),
};
const rootRoute = {
  path: '/',
  component: defineComponent({ render: () => h('div', 'root') }),
};

async function makeRouter() {
  const router = createRouter({
    history: createMemoryHistory(),
    routes: [rootRoute, memRoute],
  });
  await router.push('/');
  await router.isReady();
  return router;
}

async function mountBadge(opts: {
  total?: number;
  projectId?: string;
  projectChunks?: number;
} = {}) {
  const total = opts.total ?? 0;
  const projectChunks = opts.projectChunks ?? 0;

  const healthSnapshot = vi.fn().mockResolvedValue({
    counts: { total, raw: 0, narrative: 0, longTermPromoted: 0, embedded: 0, unembedded: 0 },
    activity: { captured: 0, pruned: 0, promoted: 0 },
    embedder: { kind: 'noop', model: '', dimensions: 0 },
    capturedAt: new Date().toISOString(),
  });
  const listChunks = vi.fn().mockResolvedValue(
    Array.from({ length: projectChunks }, (_, i) => ({ id: `chunk-${i}` })),
  );

  const router = await makeRouter();

  const w = mount(MemoryBadge, {
    props: { projectId: opts.projectId },
    global: {
      plugins: [
        router,
        {
          install(app) {
            provideFakeClient(app, {
              memory: { healthSnapshot, listChunks } as any,
            });
          },
        },
      ],
    },
  });
  await flushPromises();
  return { w, router, healthSnapshot, listChunks };
}

describe('MemoryBadge (v0.5.6)', () => {
  beforeEach(() => {
    vi.useFakeTimers();
  });
  afterEach(() => {
    vi.useRealTimers();
  });

  it('shows "…" before data loads', () => {
    // Mount without flushPromises to test the pending state.
    const router = createRouter({ history: createMemoryHistory(), routes: [rootRoute, memRoute] });
    const w = mount(MemoryBadge, {
      global: {
        plugins: [
          router,
          {
            install(app) {
              provideFakeClient(app, {
                memory: {
                  healthSnapshot: vi.fn().mockReturnValue(new Promise(() => {})),
                  listChunks: vi.fn().mockReturnValue(new Promise(() => {})),
                } as any,
              });
            },
          },
        ],
      },
    });
    const count = w.find('[data-testid="memory-badge-count"]');
    expect(count.text()).toBe('…');
  });

  it('shows global total from healthSnapshot when no projectId', async () => {
    const { w } = await mountBadge({ total: 12840 });
    expect(w.find('[data-testid="memory-badge-count"]').text()).toBe('12k');
  });

  it('calls listChunks with project filter when projectId is set', async () => {
    const { listChunks } = await mountBadge({ projectId: 'proj-abc', projectChunks: 7 });
    expect(listChunks).toHaveBeenCalledWith({ scopeKind: 'project', scopeId: 'proj-abc' });
  });

  it('shows project-scoped count from listChunks', async () => {
    const { w } = await mountBadge({ projectId: 'proj-abc', projectChunks: 7 });
    expect(w.find('[data-testid="memory-badge-count"]').text()).toBe('7');
  });

  it.each([
    [0, '0'],
    [845, '845'],
    [1200, '1.2k'],
    [12000, '12k'],
  ])('formats count %i as "%s"', async (total, expected) => {
    const { w } = await mountBadge({ total });
    expect(w.find('[data-testid="memory-badge-count"]').text()).toBe(expected);
  });

  it('click with non-zero count navigates to /memory', async () => {
    const { w, router } = await mountBadge({ total: 100 });
    await w.find('[data-testid="memory-badge"]').trigger('click');
    await flushPromises();
    expect(router.currentRoute.value.path).toBe('/memory');
  });

  it('click with non-zero count + projectId navigates to /memory?project=<id>', async () => {
    const { w, router } = await mountBadge({ projectId: 'proj-xyz', projectChunks: 5 });
    await w.find('[data-testid="memory-badge"]').trigger('click');
    await flushPromises();
    expect(router.currentRoute.value.path).toBe('/memory');
    expect(router.currentRoute.value.query).toMatchObject({ project: 'proj-xyz' });
  });

  it('click with zero count opens the onboarding modal', async () => {
    const { w } = await mountBadge({ total: 0 });
    expect(w.find('[data-testid="memory-badge-zero-modal"]').exists()).toBe(false);
    await w.find('[data-testid="memory-badge"]').trigger('click');
    await flushPromises();
    expect(w.find('[data-testid="memory-badge-zero-modal"]').exists()).toBe(true);
  });

  it('"Got it" button closes the zero-memories modal', async () => {
    const { w } = await mountBadge({ total: 0 });
    await w.find('[data-testid="memory-badge"]').trigger('click');
    await flushPromises();
    await w.find('[data-testid="memory-badge-zero-modal-ok"]').trigger('click');
    await flushPromises();
    expect(w.find('[data-testid="memory-badge-zero-modal"]').exists()).toBe(false);
  });

  it('tooltip text mentions the chunk count', async () => {
    const { w } = await mountBadge({ total: 500 });
    const badge = w.find('[data-testid="memory-badge"]');
    expect(badge.attributes('title')).toContain('500');
  });

  it('polls healthSnapshot again after 30 s', async () => {
    const { healthSnapshot } = await mountBadge({ total: 10 });
    expect(healthSnapshot).toHaveBeenCalledTimes(1);
    vi.advanceTimersByTime(30_000);
    await flushPromises();
    expect(healthSnapshot).toHaveBeenCalledTimes(2);
  });
});
