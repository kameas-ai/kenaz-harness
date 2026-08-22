/**
 * BackgroundTaskChip tests — background-task-monitor-01KZNP3C WP06
 *
 * Routes through the typed harnessClient (Tasks_ListBySession) — see
 * TasksPanel.spec.ts's header comment for why this replaced mocking
 * `@/lib/tasks` directly (deleted by
 * subagent-control-and-background-tasks-01PMZB11 UNIT-11).
 */
import { describe, it, expect, vi, beforeEach } from 'vitest';
import { mount, flushPromises } from '@vue/test-utils';
import BackgroundTaskChip from '@/components/chat/BackgroundTaskChip.vue';
import { createFakeHarnessClient } from '@/lib/harnessClient';
import { HarnessClientKey } from '@/lib/harnessClientContext';
import type { TaskRow } from '@/lib/types';

const makeTask = (overrides: Partial<TaskRow> = {}): TaskRow => ({
  id: 'task-1',
  kind: 'bash',
  ownerSessionId: 'sess-1',
  cmd: 'wails dev',
  description: 'Dev server',
  status: 'running',
  exitCode: 0,
  startedAt: new Date().toISOString(),
  ageMs: 1000,
  ...overrides,
});

function buildClient(rows: TaskRow[] = []) {
  const listBySession = vi.fn(async (_sessionId: string) => [...rows]);
  const client = createFakeHarnessClient({
    Tasks_ListBySession: listBySession,
  });
  return { client, listBySession };
}

function mountChip(rows: TaskRow[], sessionId = 'sess-1') {
  const { client, listBySession } = buildClient(rows);
  const wrapper = mount(BackgroundTaskChip, {
    props: { sessionId },
    global: { provide: { [HarnessClientKey as symbol]: client } },
  });
  return { wrapper, client, listBySession };
}

describe('BackgroundTaskChip', () => {
  beforeEach(() => {
    vi.restoreAllMocks();
  });

  it('is hidden when there are no running tasks', async () => {
    const { wrapper } = mountChip([]);
    await flushPromises();
    expect(wrapper.find('[data-testid="background-task-chip"]').exists()).toBe(false);
  });

  it('shows chip with count when tasks are running for the session', async () => {
    const { wrapper } = mountChip([makeTask()]);
    await flushPromises();
    const chip = wrapper.find('[data-testid="background-task-chip"]');
    expect(chip.exists()).toBe(true);
    expect(chip.text()).toContain('1 running');
  });

  it('scopes the query to the given sessionId via Tasks_ListBySession', async () => {
    const { listBySession } = mountChip([makeTask()], 'sess-42');
    await flushPromises();
    expect(listBySession).toHaveBeenCalledWith('sess-42');
  });

  it('emits open-tasks when chip is clicked', async () => {
    const { wrapper } = mountChip([makeTask()]);
    await flushPromises();
    await wrapper.find('[data-testid="background-task-chip"]').trigger('click');
    expect(wrapper.emitted('open-tasks')).toBeTruthy();
  });

  it('hides chip when task is completed (not running)', async () => {
    const { wrapper } = mountChip([makeTask({ status: 'completed' })]);
    await flushPromises();
    expect(wrapper.find('[data-testid="background-task-chip"]').exists()).toBe(false);
  });

  it('shows count for multiple running tasks', async () => {
    const { wrapper } = mountChip([
      makeTask({ id: 'task-1' }),
      makeTask({ id: 'task-2' }),
      makeTask({ id: 'task-3', status: 'completed' }),
    ]);
    await flushPromises();
    expect(wrapper.find('[data-testid="background-task-chip"]').text()).toContain('2 running');
  });
});
