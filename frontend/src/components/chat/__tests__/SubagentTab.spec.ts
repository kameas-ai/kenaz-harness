/**
 * SubagentTab.spec.ts — subagent-control-and-background-tasks-01PMZB11
 * UNIT-10 (AC-12, first half).
 *
 * SubagentTab.vue had zero importers before this mission — UNIT-10
 * mounts it from SessionsView.vue, gated on a real
 * `activeSubagentBranch` computed derived from BranchSidebar's actual
 * Branches_List rows (UNIT-9's wire fields), not a placeholder.
 *
 * This file tests the component in isolation:
 *  1. Each of the four emits (abort/steer/pause/resume) fires exactly
 *     once with the branch id when its control is used — the mutation
 *     AC-12 names ("remove the handler binding") would leave these red.
 *  2. The status pill's RENDERED TEXT reflects each of SubagentStatus's
 *     six values (not just that a prop was passed through) — the
 *     `?role=`-shape trap CLAUDE.md and this mission's tasks.md call
 *     out: a component that renders blank for an unmatched value looks
 *     identical to one that renders the value until you actually read
 *     the DOM.
 *  3. The budget meter's rendered text reflects real tokensUsed /
 *     budgetTokens values passed on the branch prop, not a hardcoded
 *     placeholder.
 */
import { describe, it, expect, vi, afterEach } from 'vitest';
import { mount } from '@vue/test-utils';
import SubagentTab from '@/components/chat/SubagentTab.vue';
import type { SubagentBranch, SubagentStatus } from '@/lib/types';

function makeBranch(overrides: Partial<SubagentBranch> = {}): SubagentBranch {
  return {
    id: 'br-1',
    parentSessionId: 'parent-1',
    childSessionId: 'child-1',
    kind: 'fork',
    status: 'active',
    title: 'Reviewer worker',
    createdAt: '2026-01-01T00:00:00Z',
    updatedAt: '2026-01-01T00:00:00Z',
    isSubagent: true,
    profileId: 'reviewer',
    subagentStatus: 'running',
    tokensUsed: 1200,
    budgetTokens: 5000,
    elapsedS: 90,
    budgetTimeS: 600,
    ...overrides,
  };
}

// window.confirm is called by the Abort button's confirmAbort() guard.
const originalConfirm = window.confirm;
afterEach(() => {
  window.confirm = originalConfirm;
});

describe('SubagentTab', () => {
  it('emits abort with the branch id when Abort is clicked and confirmed', async () => {
    window.confirm = vi.fn(() => true);
    const w = mount(SubagentTab, {
      props: { branch: makeBranch({ subagentStatus: 'running' }), childSessionId: 'child-1' },
    });
    await w.find('[data-testid="subagent-abort-btn"]').trigger('click');
    expect(w.emitted('abort')).toEqual([['br-1']]);
  });

  it('emits pause with the branch id when Pause is clicked (running state)', async () => {
    const w = mount(SubagentTab, {
      props: { branch: makeBranch({ subagentStatus: 'running' }), childSessionId: 'child-1' },
    });
    expect(w.find('[data-testid="subagent-pause-btn"]').exists()).toBe(true);
    expect(w.find('[data-testid="subagent-resume-btn"]').exists()).toBe(false);
    await w.find('[data-testid="subagent-pause-btn"]').trigger('click');
    expect(w.emitted('pause')).toEqual([['br-1']]);
  });

  it('emits resume with the branch id when Resume is clicked (paused state)', async () => {
    const w = mount(SubagentTab, {
      props: { branch: makeBranch({ subagentStatus: 'paused' }), childSessionId: 'child-1' },
    });
    expect(w.find('[data-testid="subagent-resume-btn"]').exists()).toBe(true);
    expect(w.find('[data-testid="subagent-pause-btn"]').exists()).toBe(false);
    await w.find('[data-testid="subagent-resume-btn"]').trigger('click');
    expect(w.emitted('resume')).toEqual([['br-1']]);
  });

  it('emits steer with the branch id and message text when Send is clicked', async () => {
    const w = mount(SubagentTab, {
      props: { branch: makeBranch({ subagentStatus: 'running' }), childSessionId: 'child-1' },
    });
    const input = w.find('[data-testid="subagent-steer-input"]');
    await input.setValue('please also check the tests');
    await w.find('[data-testid="subagent-steer-send"]').trigger('click');
    expect(w.emitted('steer')).toEqual([['br-1', 'please also check the tests']]);
  });

  it('does not emit abort when the confirm dialog is dismissed', async () => {
    window.confirm = vi.fn(() => false);
    const w = mount(SubagentTab, {
      props: { branch: makeBranch({ subagentStatus: 'running' }), childSessionId: 'child-1' },
    });
    await w.find('[data-testid="subagent-abort-btn"]').trigger('click');
    expect(w.emitted('abort')).toBeUndefined();
  });

  // AC-12: "The status pill renders each of SubagentStatus's six values.
  // A pill that renders blank for an unmatched value is the ?role= shape
  // again." Asserts the RENDERED TEXT, not the prop.
  const statusLabels: Record<SubagentStatus, string> = {
    running: 'Running',
    'awaiting-merge': 'Awaiting merge',
    paused: 'Paused',
    complete: 'Complete',
    error: 'Error',
    aborted: 'Aborted',
  };
  for (const [status, label] of Object.entries(statusLabels) as [SubagentStatus, string][]) {
    it(`renders the "${label}" pill text for subagentStatus=${status}`, () => {
      const w = mount(SubagentTab, {
        props: { branch: makeBranch({ subagentStatus: status }), childSessionId: 'child-1' },
      });
      const pill = w.find('[data-testid="subagent-status-br-1"]');
      expect(pill.exists()).toBe(true);
      expect(pill.text()).toBe(label);
    });
  }

  it('renders the budget meter off real tokensUsed/budgetTokens values, not a placeholder', () => {
    const w = mount(SubagentTab, {
      props: {
        branch: makeBranch({ subagentStatus: 'running', tokensUsed: 2500, budgetTokens: 5000 }),
        childSessionId: 'child-1',
      },
    });
    const meter = w.find('[data-testid="subagent-budget-meter"]');
    expect(meter.text()).toContain('2.5k');
    expect(meter.text()).toContain('5.0k');
    // 2500/5000 = 50% — the token bar's data-testid encodes the computed
    // percentage, so this fails if the component stops actually dividing
    // tokensUsed/budgetTokens and instead renders a fixed value.
    expect(w.find('[data-testid="token-bar-50"]').exists()).toBe(true);
  });

  it('locks the composer and shows the terminal notice for a complete branch', () => {
    const w = mount(SubagentTab, {
      props: { branch: makeBranch({ subagentStatus: 'complete' }), childSessionId: 'child-1' },
    });
    expect(w.find('[data-testid="subagent-composer-disabled"]').exists()).toBe(true);
    expect(w.find('[data-testid="subagent-steer-input"]').exists()).toBe(false);
    expect(w.find('[data-testid="subagent-pause-btn"]').exists()).toBe(false);
    expect(w.find('[data-testid="subagent-resume-btn"]').exists()).toBe(false);
    // Abort is hidden once terminal too (isComplete guards it).
    expect(w.find('[data-testid="subagent-abort-btn"]').exists()).toBe(false);
  });
});
