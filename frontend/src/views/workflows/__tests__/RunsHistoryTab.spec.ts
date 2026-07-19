/**
 * RunsHistoryTab.spec.ts — mission 01NBUG04
 *
 * Verifies that the Runs tab:
 *   FR-001  Lists actual executions (name, run id, status pill, started-at)
 *   FR-002  Live updates via workflowRunsStore — a newly ingested run appears
 *           without manual refresh
 *   FR-003  Scheduled subsection is present and labeled "Scheduled"
 *   FR-005  Empty state invites running from the Library tab
 *   FR-006  Clicking a row expands inline per-step breakdown with failure
 *           reasons
 */

import { describe, it, expect, beforeEach } from 'vitest';
import { mount, flushPromises } from '@vue/test-utils';
import { nextTick } from 'vue';
import RunsHistoryTab from '../RunsHistoryTab.vue';
import {
  ingest,
  __resetWorkflowRunsStoreForTests,
} from '@/lib/workflowRunsStore';
import { createFakeWorkflowsClient } from '@/lib/workflowsClient';

// ScheduledInbox makes async calls; stub it for focused unit tests so we
// don't need to wire a full client contract for every test case.
import { vi } from 'vitest';
vi.mock('../ScheduledInbox.vue', () => ({
  default: {
    name: 'ScheduledInboxStub',
    template: '<div data-testid="scheduled-inbox-stub" />',
  },
}));

// ── helpers ────────────────────────────────────────────────────────────────

function fakeClient() {
  return createFakeWorkflowsClient({
    scheduleList: vi.fn().mockResolvedValue([]),
  });
}

async function mountTab() {
  const w = mount(RunsHistoryTab, {
    props: { client: fakeClient() },
  });
  await flushPromises();
  return w;
}

// ── fixture events ─────────────────────────────────────────────────────────

const T0 = '2026-07-01T10:00:00.000Z';
const T1 = '2026-07-01T10:00:05.000Z';
const T2 = '2026-07-01T10:00:10.000Z';

describe('RunsHistoryTab', () => {
  beforeEach(() => {
    __resetWorkflowRunsStoreForTests();
  });

  // ── FR-005 empty state ─────────────────────────────────────────────

  it('FR-005: shows library-directed empty state when no runs exist', async () => {
    const w = await mountTab();
    const empty = w.find('[data-testid="runs-history-empty"]');
    expect(empty.exists()).toBe(true);
    expect(empty.text()).toContain('Library');
    expect(empty.text()).toContain('Run workflow');
  });

  // ── FR-001 list rendering ──────────────────────────────────────────

  it('FR-001: renders recipe name, run id, status pill, and started-at', async () => {
    ingest({
      runId: 'run-alpha',
      workflowId: 'plan_implement_review',
      workflowName: 'Plan → Implement → Review',
      phase: 'run_started',
      ts: T0,
    });
    ingest({
      runId: 'run-alpha',
      workflowId: 'plan_implement_review',
      workflowName: 'Plan → Implement → Review',
      phase: 'run_completed',
      ts: T1,
    });

    const w = await mountTab();

    // Row renders
    expect(w.find('[data-testid="runs-history-row-run-alpha"]').exists()).toBe(true);

    // Recipe name
    expect(w.find('[data-testid="runs-history-name-run-alpha"]').text()).toContain(
      'Plan → Implement → Review',
    );

    // Run id
    expect(w.find('[data-testid="runs-history-runid-run-alpha"]').text()).toBe('run-alpha');

    // Status pill says 'succeeded' (status === 'done')
    expect(w.find('[data-testid="runs-history-pill-run-alpha"]').text()).toBe('succeeded');

    // started-at field present
    expect(w.find('[data-testid="runs-history-started-run-alpha"]').exists()).toBe(true);
  });

  it('FR-001: shows "running" pill for in-progress run', async () => {
    ingest({
      runId: 'run-live',
      workflowId: 'wf',
      workflowName: 'Live',
      phase: 'run_started',
      ts: T0,
    });

    const w = await mountTab();
    expect(w.find('[data-testid="runs-history-pill-run-live"]').text()).toBe('running');
  });

  it('FR-001: shows "failed" pill for failed run', async () => {
    ingest({
      runId: 'run-bad',
      workflowId: 'wf',
      workflowName: 'Bad',
      phase: 'run_started',
      ts: T0,
    });
    ingest({
      runId: 'run-bad',
      workflowId: 'wf',
      workflowName: 'Bad',
      phase: 'run_failed',
      ts: T1,
    });

    const w = await mountTab();
    expect(w.find('[data-testid="runs-history-pill-run-bad"]').text()).toBe('failed');
  });

  it('FR-001: renders runs in most-recent-first order', async () => {
    ingest({ runId: 'r-old', workflowId: 'w', workflowName: 'W', phase: 'run_started', ts: '2026-07-01T08:00:00.000Z' });
    ingest({ runId: 'r-mid', workflowId: 'w', workflowName: 'W', phase: 'run_started', ts: '2026-07-01T09:00:00.000Z' });
    ingest({ runId: 'r-new', workflowId: 'w', workflowName: 'W', phase: 'run_started', ts: '2026-07-01T10:00:00.000Z' });

    const w = await mountTab();
    const rows = w.findAll('[data-testid^="runs-history-row-"]');
    expect(rows.length).toBe(3);
    expect(rows[0].attributes('data-testid')).toBe('runs-history-row-r-new');
    expect(rows[1].attributes('data-testid')).toBe('runs-history-row-r-mid');
    expect(rows[2].attributes('data-testid')).toBe('runs-history-row-r-old');
  });

  // ── FR-002 live update ─────────────────────────────────────────────

  it('FR-002: a newly ingested run appears live without remounting', async () => {
    const w = await mountTab();

    // No runs initially → empty state
    expect(w.find('[data-testid="runs-history-empty"]').exists()).toBe(true);

    // Ingest a run event after mount
    ingest({
      runId: 'run-live-new',
      workflowId: 'wf',
      workflowName: 'Freshly started',
      phase: 'run_started',
      ts: T0,
    });
    await nextTick();

    // Empty state gone; row appears
    expect(w.find('[data-testid="runs-history-empty"]').exists()).toBe(false);
    expect(w.find('[data-testid="runs-history-row-run-live-new"]').exists()).toBe(true);
  });

  it('FR-002: run status updates to terminal without remounting', async () => {
    ingest({
      runId: 'run-transition',
      workflowId: 'wf',
      workflowName: 'Transitioning',
      phase: 'run_started',
      ts: T0,
    });
    const w = await mountTab();

    // Initially running
    expect(w.find('[data-testid="runs-history-pill-run-transition"]').text()).toBe('running');

    // Engine completes the run
    ingest({
      runId: 'run-transition',
      workflowId: 'wf',
      workflowName: 'Transitioning',
      phase: 'run_completed',
      ts: T1,
    });
    await nextTick();

    expect(w.find('[data-testid="runs-history-pill-run-transition"]').text()).toBe('succeeded');
  });

  // ── FR-003 scheduled subsection ───────────────────────────────────

  it('FR-003: renders a clearly-labeled "Scheduled" sub-section', async () => {
    const w = await mountTab();
    const section = w.find('[data-testid="runs-scheduled-section"]');
    expect(section.exists()).toBe(true);
    const heading = w.find('[data-testid="runs-scheduled-heading"]');
    expect(heading.text().toLowerCase()).toContain('scheduled');
  });

  it('FR-003: Scheduled subsection is distinct from Execution History section', async () => {
    const w = await mountTab();
    // Both named sections exist independently
    expect(w.find('[data-testid="runs-history-section"]').exists()).toBe(true);
    expect(w.find('[data-testid="runs-scheduled-section"]').exists()).toBe(true);
  });

  // ── FR-006 per-step breakdown ──────────────────────────────────────

  it('FR-006: clicking a run row expands the inline per-step breakdown', async () => {
    ingest({
      runId: 'run-expand',
      workflowId: 'wf',
      workflowName: 'Expandable',
      phase: 'run_started',
      ts: T0,
    });
    ingest({
      runId: 'run-expand',
      workflowId: 'wf',
      workflowName: 'Expandable',
      phase: 'step_started',
      stepName: 'plan',
      stepKind: 'model_turn',
      ts: T0,
    });
    ingest({
      runId: 'run-expand',
      workflowId: 'wf',
      workflowName: 'Expandable',
      phase: 'step_completed',
      stepName: 'plan',
      stepKind: 'model_turn',
      ts: T1,
    });
    ingest({
      runId: 'run-expand',
      workflowId: 'wf',
      workflowName: 'Expandable',
      phase: 'run_completed',
      ts: T2,
    });

    const w = await mountTab();

    // Breakdown not visible before click
    expect(w.find('[data-testid="runs-history-steps-run-expand"]').exists()).toBe(false);

    // Click to expand
    await w.find('[data-testid="runs-history-open-run-expand"]').trigger('click');
    await nextTick();

    const steps = w.find('[data-testid="runs-history-steps-run-expand"]');
    expect(steps.exists()).toBe(true);

    // Step row is rendered
    expect(w.find('[data-testid="runs-history-step-run-expand-plan"]').exists()).toBe(true);
    expect(w.find('[data-testid="runs-history-step-status-run-expand-plan"]').text()).toBe('done');
  });

  it('FR-006: clicking expanded row collapses it', async () => {
    ingest({
      runId: 'run-collapse',
      workflowId: 'wf',
      workflowName: 'Collapsible',
      phase: 'run_started',
      ts: T0,
    });
    ingest({
      runId: 'run-collapse',
      workflowId: 'wf',
      workflowName: 'Collapsible',
      phase: 'step_started',
      stepName: 'implement',
      stepKind: 'model_turn',
      ts: T0,
    });

    const w = await mountTab();

    // Expand
    await w.find('[data-testid="runs-history-open-run-collapse"]').trigger('click');
    await nextTick();
    expect(w.find('[data-testid="runs-history-steps-run-collapse"]').exists()).toBe(true);

    // Collapse
    await w.find('[data-testid="runs-history-open-run-collapse"]').trigger('click');
    await nextTick();
    expect(w.find('[data-testid="runs-history-steps-run-collapse"]').exists()).toBe(false);
  });

  it('FR-006: failure reason surfaces on the failed step row', async () => {
    ingest({
      runId: 'run-fail',
      workflowId: 'wf',
      workflowName: 'Failing',
      phase: 'run_started',
      ts: T0,
    });
    ingest({
      runId: 'run-fail',
      workflowId: 'wf',
      workflowName: 'Failing',
      phase: 'step_started',
      stepName: 'review',
      stepKind: 'model_turn',
      ts: T0,
    });
    ingest({
      runId: 'run-fail',
      workflowId: 'wf',
      workflowName: 'Failing',
      phase: 'step_failed',
      stepName: 'review',
      stepKind: 'model_turn',
      error: 'cedar policy denied',
      ts: T1,
    });

    const w = await mountTab();

    // Failed step name visible on row summary
    expect(
      w.find('[data-testid="runs-history-failed-step-run-fail"]').exists(),
    ).toBe(true);
    expect(
      w.find('[data-testid="runs-history-failed-step-run-fail"]').text(),
    ).toContain('review');

    // Expand to see step detail with error
    await w.find('[data-testid="runs-history-open-run-fail"]').trigger('click');
    await nextTick();

    const errEl = w.find('[data-testid="runs-history-step-error-run-fail-review"]');
    expect(errEl.exists()).toBe(true);
    expect(errEl.text()).toContain('cedar policy denied');
  });

  it('FR-006: shows "no step detail" when run has no steps', async () => {
    ingest({
      runId: 'run-nosteps',
      workflowId: 'wf',
      workflowName: 'Empty steps',
      phase: 'run_completed',
      ts: T0,
    });

    const w = await mountTab();
    await w.find('[data-testid="runs-history-open-run-nosteps"]').trigger('click');
    await nextTick();

    expect(
      w.find('[data-testid="runs-history-steps-empty-run-nosteps"]').exists(),
    ).toBe(true);
  });
});
