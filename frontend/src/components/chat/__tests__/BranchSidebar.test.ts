import { describe, it, expect, vi } from 'vitest';
import { mount, flushPromises } from '@vue/test-utils';
import BranchSidebar from '@/components/chat/BranchSidebar.vue';
import { provideFakeClient } from '@/lib/harnessClientContext';
import type { HarnessClient } from '@/lib/harnessClient';
import type { Branch } from '@/lib/types';

function makeBranch(overrides: Partial<Branch> = {}): Branch {
  return {
    id: 'br-1',
    parentSessionId: 'p1',
    childSessionId: 'c1',
    kind: 'fork',
    status: 'active',
    title: 'Side question',
    modelId: 'claude-haiku-4',
    createdAt: '2026-04-25T00:00:00Z',
    updatedAt: '2026-04-25T00:00:00Z',
    ...overrides,
  };
}

function mountSidebar(parentSessionId: string, seed: Partial<HarnessClient> = {}) {
  return mount(BranchSidebar, {
    props: { parentSessionId },
    global: {
      plugins: [
        {
          install(app) {
            provideFakeClient(app, seed);
          },
        },
      ],
    },
  });
}

describe('BranchSidebar', () => {
  it('collapses to a narrow rail when no branches exist', async () => {
    const w = mountSidebar('p1');
    await flushPromises();
    // Empty state collapses to a narrow strip with only a "+ Fork"
    // button and a vertical "Fork" label — the full sidebar copy
    // (and its data-testid) only mounts once at least one branch exists.
    expect(
      w.find('[data-testid="branch-sidebar-collapsed"]').exists(),
    ).toBe(true);
    expect(w.find('[data-testid="branch-sidebar"]').exists()).toBe(false);
    expect(w.find('[data-testid="branch-create-btn"]').exists()).toBe(true);
  });

  it('renders one row per branch with status + model', async () => {
    const branches: Branch[] = [
      makeBranch({ id: 'br-a', title: 'Q1' }),
      makeBranch({ id: 'br-b', title: 'Q2', status: 'merged' }),
    ];
    const w = mountSidebar('p1', {
      branches: {
        list: vi.fn().mockResolvedValue(branches),
        create: vi.fn(),
        createExplicit: vi.fn(),
        status: vi.fn(),
        merge: vi.fn(),
        abandon: vi.fn(),
        recommendModel: vi.fn(),
        proposeReintegrationSummary: vi.fn(),
        commitReintegration: vi.fn(),
        setAdvisorDismissed: vi.fn(),
        listWithBranchTree: vi.fn(),
      },
    });
    await flushPromises();
    expect(w.find('[data-testid="branch-row-br-a"]').exists()).toBe(true);
    expect(w.find('[data-testid="branch-row-br-b"]').exists()).toBe(true);
    expect(w.find('[data-testid="branch-status-br-a"]').text()).toBe('Active');
    expect(w.find('[data-testid="branch-status-br-b"]').text()).toBe('Merged');
  });

  it('emits create when the + Fork button is clicked', async () => {
    const w = mountSidebar('p1');
    await flushPromises();
    await w.find('[data-testid="branch-create-btn"]').trigger('click');
    expect(w.emitted('create')).toBeTruthy();
  });

  it('emits open with the child session id', async () => {
    const branches: Branch[] = [makeBranch({ id: 'br-a', childSessionId: 'child-x' })];
    const w = mountSidebar('p1', {
      branches: {
        list: vi.fn().mockResolvedValue(branches),
        create: vi.fn(),
        createExplicit: vi.fn(),
        status: vi.fn(),
        merge: vi.fn(),
        abandon: vi.fn(),
        recommendModel: vi.fn(),
        proposeReintegrationSummary: vi.fn(),
        commitReintegration: vi.fn(),
        setAdvisorDismissed: vi.fn(),
        listWithBranchTree: vi.fn(),
      },
    });
    await flushPromises();
    await w.find('[data-testid="branch-open-br-a"]').trigger('click');
    expect(w.emitted('open')?.[0]).toEqual(['child-x']);
  });

  it('calls merge and refreshes on click', async () => {
    const branches: Branch[] = [makeBranch({ id: 'br-a' })];
    const merge = vi.fn().mockResolvedValue(undefined);
    const list = vi.fn().mockResolvedValue(branches);
    const w = mountSidebar('p1', {
      branches: {
        list,
        create: vi.fn(),
        createExplicit: vi.fn(),
        status: vi.fn(),
        merge,
        abandon: vi.fn(),
        recommendModel: vi.fn(),
        proposeReintegrationSummary: vi.fn(),
        commitReintegration: vi.fn(),
        setAdvisorDismissed: vi.fn(),
        listWithBranchTree: vi.fn(),
      },
    });
    await flushPromises();
    await w.find('[data-testid="branch-merge-br-a"]').trigger('click');
    await flushPromises();
    expect(merge).toHaveBeenCalledWith('br-a');
    expect(list).toHaveBeenCalledTimes(2); // initial + after merge
  });

  it('calls abandon and refreshes on click', async () => {
    const branches: Branch[] = [makeBranch({ id: 'br-a' })];
    const abandon = vi.fn().mockResolvedValue(undefined);
    const list = vi.fn().mockResolvedValue(branches);
    const w = mountSidebar('p1', {
      branches: {
        list,
        create: vi.fn(),
        createExplicit: vi.fn(),
        status: vi.fn(),
        merge: vi.fn(),
        abandon,
        recommendModel: vi.fn(),
        proposeReintegrationSummary: vi.fn(),
        commitReintegration: vi.fn(),
        setAdvisorDismissed: vi.fn(),
        listWithBranchTree: vi.fn(),
      },
    });
    await flushPromises();
    await w.find('[data-testid="branch-abandon-br-a"]').trigger('click');
    await flushPromises();
    expect(abandon).toHaveBeenCalledWith('br-a');
    expect(list).toHaveBeenCalledTimes(2);
  });
});
