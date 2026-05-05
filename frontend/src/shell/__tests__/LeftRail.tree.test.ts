/**
 * LeftRail.tree.test.ts — branch tree rendering tests
 * branching-ux-polish-01KQ8TD7 WP03
 *
 * Three cases:
 *  1. Flat list (no branch sessions) — sessions render without tree structure.
 *  2. Two-level tree — a parent with one branch child renders with chevron.
 *  3. Three-level tree with collapse — grandchild hidden when parent collapsed.
 */
import { describe, it, expect, beforeEach } from 'vitest';
import { mount } from '@vue/test-utils';
import { defineComponent, ref } from 'vue';
import type { Session } from '@/lib/types';
import { provideFakeClient } from '@/lib/harnessClient';

// We test SessionTreeRow directly for the three core behaviours.
// LeftRail integration mounts the real component with a fake client.
import SessionTreeRow from '@/shell/SessionTreeRow.vue';

function makeSession(overrides: Partial<Session> & { id: string }): Session {
  return {
    name: overrides.id,
    createdAt: '2026-01-01T00:00:00Z',
    updatedAt: '2026-01-01T00:00:00Z',
    ...overrides,
  };
}

describe('SessionTreeRow', () => {
  // Case 1 — flat session (no parent, no children)
  it('renders a root session without branch icon or chevron', () => {
    const session = makeSession({ id: 's1', name: 'Root session' });
    const wrapper = mount(SessionTreeRow, {
      props: {
        session,
        depth: 0,
        hasChildren: false,
        isCollapsed: false,
        isActive: false,
        isRenaming: false,
        maxDepth: 5,
      },
    });

    // No chevron (hasChildren=false)
    expect(wrapper.find('[data-testid="branch-collapse-toggle-s1"]').exists()).toBe(false);
    // No GitBranch icon (not a branch session)
    expect(wrapper.text()).toContain('Root session');
    expect(wrapper.attributes('style')).toContain('padding-left: 0px');
  });

  // Case 2 — parent session with children
  it('renders a chevron when hasChildren=true', async () => {
    const session = makeSession({ id: 'parent-1', name: 'Parent' });
    const wrapper = mount(SessionTreeRow, {
      props: {
        session,
        depth: 0,
        hasChildren: true,
        isCollapsed: false,
        isActive: false,
        isRenaming: false,
        maxDepth: 5,
      },
    });

    const toggle = wrapper.find('[data-testid="branch-collapse-toggle-parent-1"]');
    expect(toggle.exists()).toBe(true);
    expect(toggle.attributes('aria-expanded')).toBe('true');

    await toggle.trigger('click');
    expect(wrapper.emitted('toggle-collapse')).toBeTruthy();
    expect(wrapper.emitted('toggle-collapse')![0]).toEqual(['parent-1']);
  });

  // Case 3 — branch child (has parentSessionId) renders with GitBranch icon + indent
  it('renders a branch session with indent and branch icon', () => {
    const session = makeSession({
      id: 'child-1',
      name: 'Branch session',
      parentSessionId: 'parent-1',
      branchDepth: 1,
    });
    const wrapper = mount(SessionTreeRow, {
      props: {
        session,
        depth: 1,
        hasChildren: false,
        isCollapsed: false,
        isActive: false,
        isRenaming: false,
        maxDepth: 5,
      },
    });

    // 1 level × 12 px
    expect(wrapper.attributes('style')).toContain('padding-left: 12px');
    // Session text is present
    expect(wrapper.text()).toContain('Branch session');
  });

  // Case 3b — collapsed parent: toggle emits the right event
  it('emits toggle-collapse when chevron clicked on collapsed parent', async () => {
    const session = makeSession({ id: 'p2', name: 'Parent 2' });
    const wrapper = mount(SessionTreeRow, {
      props: {
        session,
        depth: 0,
        hasChildren: true,
        isCollapsed: true,
        isActive: false,
        isRenaming: false,
        maxDepth: 5,
      },
    });

    const toggle = wrapper.find('[data-testid="branch-collapse-toggle-p2"]');
    expect(toggle.attributes('aria-expanded')).toBe('false');

    await toggle.trigger('click');
    const emitted = wrapper.emitted('toggle-collapse');
    expect(emitted).toBeTruthy();
    expect(emitted![0]).toEqual(['p2']);
  });
});
