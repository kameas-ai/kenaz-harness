/**
 * WorkflowRunsSection.test — sidebar rendering + interaction (mission
 * workflows-01KQ8TDG, WP10).
 *
 * The component reads from the singleton workflowRunsStore. We seed
 * state via `ingest()` between mounts, then assert on the rendered
 * markup. A memory router is provided so the click handler's
 * router.push() doesn't throw.
 */

import { describe, it, expect, beforeEach } from 'vitest';
import { mount, flushPromises } from '@vue/test-utils';
import { createMemoryHistory, createRouter } from 'vue-router';
import { defineComponent, h } from 'vue';
import WorkflowRunsSection from '@/components/workflows/WorkflowRunsSection.vue';
import {
  ingest,
  __resetWorkflowRunsStoreForTests,
} from '@/lib/workflowRunsStore';

async function mountSection() {
  const router = createRouter({
    history: createMemoryHistory(),
    routes: [
      {
        path: '/workflows',
        name: 'workflows',
        component: defineComponent({ render: () => h('div') }),
      },
      {
        path: '/',
        component: defineComponent({ render: () => h('div') }),
      },
    ],
  });
  await router.push('/');
  await router.isReady();
  const w = mount(WorkflowRunsSection, {
    global: { plugins: [router] },
  });
  await flushPromises();
  return { w, router };
}

describe('WorkflowRunsSection', () => {
  beforeEach(() => {
    __resetWorkflowRunsStoreForTests();
  });

  it('is hidden when no runs exist', async () => {
    const { w } = await mountSection();
    expect(w.find('[data-testid="workflow-runs-section"]').exists()).toBe(
      false,
    );
  });

  it('renders runs in recent-first order with status pills', async () => {
    ingest({
      runId: 'r-old',
      workflowId: 'wf-plan',
      workflowName: 'Plan',
      phase: 'run_started',
      ts: '2026-05-01T09:00:00.000Z',
    });
    ingest({
      runId: 'r-old',
      workflowId: 'wf-plan',
      workflowName: 'Plan',
      phase: 'run_completed',
      ts: '2026-05-01T09:00:30.000Z',
    });
    ingest({
      runId: 'r-mid',
      workflowId: 'wf-other',
      workflowName: 'Other',
      phase: 'run_started',
      ts: '2026-05-01T10:00:00.000Z',
    });
    ingest({
      runId: 'r-new',
      workflowId: 'wf-x',
      workflowName: 'Latest',
      phase: 'run_started',
      ts: '2026-05-01T11:00:00.000Z',
    });

    const { w } = await mountSection();
    expect(w.find('[data-testid="workflow-runs-section"]').exists()).toBe(
      true,
    );
    const rows = w.findAll('[data-testid^="workflow-run-row-"]');
    expect(rows).toHaveLength(3);
    // recent-first
    expect(rows[0].attributes('data-testid')).toBe('workflow-run-row-r-new');
    expect(rows[1].attributes('data-testid')).toBe('workflow-run-row-r-mid');
    expect(rows[2].attributes('data-testid')).toBe('workflow-run-row-r-old');

    // pill text matches store status
    expect(w.find('[data-testid="workflow-run-pill-r-new"]').text()).toBe(
      'running',
    );
    expect(w.find('[data-testid="workflow-run-pill-r-mid"]').text()).toBe(
      'running',
    );
    expect(w.find('[data-testid="workflow-run-pill-r-old"]').text()).toBe(
      'done',
    );
    // header count reflects the run count
    expect(w.find('[data-testid="workflow-runs-header"]').text()).toContain(
      '(3)',
    );
  });

  it('surfaces the failed step name on a failed run row', async () => {
    ingest({
      runId: 'r-fail',
      workflowId: 'wf-plan',
      workflowName: 'Plan',
      phase: 'run_started',
      ts: '2026-05-01T10:00:00.000Z',
    });
    ingest({
      runId: 'r-fail',
      workflowId: 'wf-plan',
      workflowName: 'Plan',
      phase: 'step_started',
      stepName: 'review',
      stepKind: 'model_turn',
      ts: '2026-05-01T10:00:01.000Z',
    });
    ingest({
      runId: 'r-fail',
      workflowId: 'wf-plan',
      workflowName: 'Plan',
      phase: 'step_failed',
      stepName: 'review',
      stepKind: 'model_turn',
      error: 'cedar denied',
      ts: '2026-05-01T10:00:05.000Z',
    });

    const { w } = await mountSection();
    expect(w.find('[data-testid="workflow-run-pill-r-fail"]').text()).toBe(
      'failed',
    );
    const failedSpan = w.find(
      '[data-testid="workflow-run-failed-step-r-fail"]',
    );
    expect(failedSpan.exists()).toBe(true);
    expect(failedSpan.text()).toContain('review');
  });

  it('expands inline step list when a row is clicked and emits focus event', async () => {
    ingest({
      runId: 'r-click',
      workflowId: 'wf-plan',
      workflowName: 'Plan',
      phase: 'run_started',
      ts: '2026-05-01T10:00:00.000Z',
    });
    ingest({
      runId: 'r-click',
      workflowId: 'wf-plan',
      workflowName: 'Plan',
      phase: 'step_started',
      stepName: 'plan',
      stepKind: 'model_turn',
      ts: '2026-05-01T10:00:01.000Z',
    });
    ingest({
      runId: 'r-click',
      workflowId: 'wf-plan',
      workflowName: 'Plan',
      phase: 'step_completed',
      stepName: 'plan',
      stepKind: 'model_turn',
      ts: '2026-05-01T10:00:05.000Z',
    });

    const { w } = await mountSection();
    expect(
      w.find('[data-testid="workflow-run-steps-r-click"]').exists(),
    ).toBe(false);
    await w
      .find('[data-testid="workflow-run-open-r-click"]')
      .trigger('click');
    expect(
      w.find('[data-testid="workflow-run-steps-r-click"]').exists(),
    ).toBe(true);
    expect(
      w
        .find('[data-testid="workflow-run-step-r-click-plan"]')
        .text(),
    ).toContain('plan');
    const emitted = w.emitted('workflow-run:focus');
    expect(emitted).toBeTruthy();
    expect(emitted![0]).toEqual(['r-click']);

    // toggling collapses it again
    await w
      .find('[data-testid="workflow-run-open-r-click"]')
      .trigger('click');
    expect(
      w.find('[data-testid="workflow-run-steps-r-click"]').exists(),
    ).toBe(false);
  });

  it('collapses the section when the header is clicked', async () => {
    ingest({
      runId: 'r-1',
      workflowId: 'wf',
      workflowName: 'X',
      phase: 'run_started',
      ts: '2026-05-01T10:00:00.000Z',
    });
    const { w } = await mountSection();
    expect(w.find('[data-testid="workflow-runs-list"]').exists()).toBe(true);
    await w.find('[data-testid="workflow-runs-header"]').trigger('click');
    expect(w.find('[data-testid="workflow-runs-list"]').exists()).toBe(
      false,
    );
  });
});
