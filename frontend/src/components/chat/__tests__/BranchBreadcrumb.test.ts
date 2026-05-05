/**
 * BranchBreadcrumb.test.ts — branching-ux-polish-01KQ8TD7 WP04.
 *
 * Tests:
 *  1. Renders nothing when parentSessionId is empty.
 *  2. Renders "Branch of <parent>" when turnNumber is absent.
 *  3. Renders "Branch from turn N of <parent>" when turnNumber is set.
 *  4. Parent title is a clickable link (router-push).
 *  5. Multi-level ancestorCount > 1 shows "..." expand toggle.
 *  6. Deleted parent renders fallback form without link.
 */
import { describe, it, expect, vi } from 'vitest';
import { mount, flushPromises } from '@vue/test-utils';
import { createMemoryHistory, createRouter } from 'vue-router';
import { defineComponent, h } from 'vue';
import BranchBreadcrumb from '@/components/chat/BranchBreadcrumb.vue';

async function mountBreadcrumb(props: Record<string, unknown>) {
  const router = createRouter({
    history: createMemoryHistory(),
    routes: [
      {
        path: '/sessions/:id?',
        name: 'sessions',
        component: defineComponent({ render: () => h('div') }),
      },
    ],
  });
  await router.push('/sessions/child-1');
  await router.isReady();
  const w = mount(BranchBreadcrumb, {
    props,
    global: { plugins: [router] },
  });
  return { w, router };
}

describe('BranchBreadcrumb', () => {
  it('renders nothing when parentSessionId is empty', async () => {
    const { w } = await mountBreadcrumb({
      parentSessionId: '',
      parentTitle: 'Alpha',
    });
    expect(w.find('[data-testid="branch-breadcrumb"]').exists()).toBe(false);
  });

  it('renders "Branch of <parent>" when turnNumber is absent', async () => {
    const { w } = await mountBreadcrumb({
      parentSessionId: 'parent-1',
      parentTitle: 'Alpha session',
    });
    const bc = w.find('[data-testid="branch-breadcrumb"]');
    expect(bc.exists()).toBe(true);
    const label = w.find('[data-testid="branch-breadcrumb-label"]');
    expect(label.text()).toBe('Branch of');
    const link = w.find('[data-testid="branch-breadcrumb-parent-link"]');
    expect(link.exists()).toBe(true);
    expect(link.text()).toContain('Alpha session');
  });

  it('renders "Branch from turn N of <parent>" when turnNumber is set', async () => {
    const { w } = await mountBreadcrumb({
      parentSessionId: 'parent-1',
      parentTitle: 'Alpha session',
      turnNumber: 3,
    });
    const label = w.find('[data-testid="branch-breadcrumb-label"]');
    expect(label.text()).toBe('Branch from turn 3 of');
    expect(w.find('[data-testid="branch-breadcrumb-parent-link"]').exists()).toBe(true);
  });

  it('navigates to parent session on link click', async () => {
    const { w, router } = await mountBreadcrumb({
      parentSessionId: 'parent-abc',
      parentTitle: 'Parent Alpha',
      turnNumber: 2,
    });
    const link = w.find('[data-testid="branch-breadcrumb-parent-link"]');
    await link.trigger('click');
    await flushPromises();
    expect(router.currentRoute.value.path).toBe('/sessions/parent-abc');
  });

  it('shows ... toggle when ancestorCount > 1', async () => {
    const { w } = await mountBreadcrumb({
      parentSessionId: 'parent-1',
      parentTitle: 'Alpha',
      ancestorCount: 3,
    });
    const toggle = w.find('[data-testid="branch-breadcrumb-ancestors-toggle"]');
    expect(toggle.exists()).toBe(true);
    expect(toggle.text()).toBe('...');
    // Popover hidden initially.
    expect(w.find('[data-testid="branch-breadcrumb-ancestor-popover"]').exists()).toBe(false);
    // Click to expand.
    await toggle.trigger('click');
    expect(w.find('[data-testid="branch-breadcrumb-ancestor-popover"]').exists()).toBe(true);
  });

  it('renders deleted-parent fallback without a link', async () => {
    const { w } = await mountBreadcrumb({
      parentSessionId: 'parent-gone',
      parentTitle: 'Old session',
      parentDeleted: true,
    });
    const bc = w.find('[data-testid="branch-breadcrumb"]');
    expect(bc.exists()).toBe(true);
    const label = w.find('[data-testid="branch-breadcrumb-label"]');
    expect(label.text()).toContain('Branch of [parent deleted: Old session]');
    // No link when parent deleted.
    expect(w.find('[data-testid="branch-breadcrumb-parent-link"]').exists()).toBe(false);
  });
});
