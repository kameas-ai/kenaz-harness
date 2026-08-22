/**
 * TasksPanel tests — background-task-monitor-01KZNP3C WP05
 *
 * Routes through the typed harnessClient (Tasks_List / Tasks_Abort) —
 * mirrors LogsPanel.spec.ts's HarnessClientKey provide pattern. Previously
 * mocked `@/lib/tasks` directly; that module was deleted by
 * subagent-control-and-background-tasks-01PMZB11 UNIT-11 once TasksPanel
 * (and its sibling components) were the only consumers left and were
 * routed onto the typed client instead.
 */
import { describe, it, expect, vi, beforeEach } from 'vitest';
import { mount, flushPromises } from '@vue/test-utils';
import TasksPanel from '@/views/settings/TasksPanel.vue';
import { createFakeHarnessClient } from '@/lib/harnessClient';
import { HarnessClientKey } from '@/lib/harnessClientContext';
import type { TaskRow } from '@/lib/types';

const runningTask: TaskRow = {
  id: 'task-abc',
  kind: 'bash',
  ownerSessionId: 'sess-1',
  cmd: 'wails dev',
  description: 'Dev server',
  status: 'running',
  exitCode: 0,
  startedAt: new Date().toISOString(),
  ageMs: 5000,
};

const completedTask: TaskRow = {
  id: 'task-def',
  kind: 'bash',
  ownerSessionId: 'sess-1',
  cmd: 'go test ./...',
  description: '',
  status: 'completed',
  exitCode: 0,
  startedAt: new Date(Date.now() - 10_000).toISOString(),
  endedAt: new Date().toISOString(),
  ageMs: 10_000,
};

function buildClient(rows: TaskRow[] = []) {
  const list = vi.fn(async () => [...rows]);
  const abort = vi.fn(async () => undefined as void);
  const client = createFakeHarnessClient({
    Tasks_List: list,
    Tasks_Abort: abort,
  });
  return { client, list, abort };
}

function mountPanel(rows: TaskRow[] = [], props: Record<string, unknown> = {}) {
  const { client, list, abort } = buildClient(rows);
  const wrapper = mount(TasksPanel, {
    props,
    global: { provide: { [HarnessClientKey as symbol]: client } },
  });
  return { wrapper, client, list, abort };
}

describe('TasksPanel', () => {
  beforeEach(() => {
    vi.restoreAllMocks();
  });

  it('renders the panel', async () => {
    const { wrapper } = mountPanel([]);
    await flushPromises();
    expect(wrapper.find('[data-testid="tasks-panel"]').exists()).toBe(true);
  });

  it('shows empty state when there are no tasks', async () => {
    const { wrapper } = mountPanel([]);
    await flushPromises();
    expect(wrapper.find('[data-testid="tasks-empty"]').exists()).toBe(true);
  });

  it('renders running task with abort button', async () => {
    const { wrapper } = mountPanel([runningTask]);
    await flushPromises();

    const row = wrapper.find(`[data-testid="task-row-${runningTask.id}"]`);
    expect(row.exists()).toBe(true);

    expect(row.find('[data-testid="task-status"]').text()).toContain('running');
    expect(row.find('[data-testid="task-abort-btn"]').exists()).toBe(true);
  });

  it('renders completed task without abort button', async () => {
    const { wrapper } = mountPanel([completedTask]);
    await flushPromises();

    const row = wrapper.find(`[data-testid="task-row-${completedTask.id}"]`);
    expect(row.exists()).toBe(true);
    expect(row.find('[data-testid="task-abort-btn"]').exists()).toBe(false);
  });

  it('calls Tasks_Abort and reloads when abort button clicked', async () => {
    const { wrapper, abort, list } = mountPanel([runningTask]);
    await flushPromises();

    // Change mock to return empty after abort.
    list.mockResolvedValue([]);

    await wrapper.find('[data-testid="task-abort-btn"]').trigger('click');
    await flushPromises();

    expect(abort).toHaveBeenCalledWith(runningTask.id);
  });

  it('emits view-task when view output clicked', async () => {
    const { wrapper } = mountPanel([runningTask]);
    await flushPromises();

    await wrapper.find('[data-testid="task-view-btn"]').trigger('click');
    expect(wrapper.emitted('view-task')).toBeTruthy();
    expect(wrapper.emitted('view-task')![0]).toEqual([runningTask.id]);
  });

  it('filters by sessionId when prop provided', async () => {
    const { wrapper } = mountPanel([runningTask, completedTask], { filterSessionId: 'sess-other' });
    await flushPromises();

    // Both tasks belong to sess-1; none should show.
    expect(wrapper.find('[data-testid="tasks-empty"]').exists()).toBe(true);
  });

  it('shows running count when tasks are running', async () => {
    const { wrapper } = mountPanel([runningTask]);
    await flushPromises();

    expect(wrapper.find('[data-testid="running-count"]').exists()).toBe(true);
    expect(wrapper.find('[data-testid="running-count"]').text()).toContain('1 running');
  });

  // AC-12 (second half), tasks.md UNIT-11: the panel renders a row for a
  // LIVE background task — proving it reads Tasks_List rather than a
  // fixture. The backend half of this falsification (revert UNIT-3's
  // BackgroundSpawn assignment => Tasks_List's underlying producer goes
  // empty) is pinned by core/rpc/background_task_wiring_test.go's
  // TestBashBackgroundMode_ProductionWiring_RegistersATaskRow; this test
  // pins the frontend half of the same chain — an empty Tasks_List
  // response renders the empty state, not stale/fixture rows.
  it('renders empty when Tasks_List returns no rows (the BackgroundSpawn-reverted state)', async () => {
    const { wrapper } = mountPanel([]);
    await flushPromises();
    expect(wrapper.find('[data-testid="task-list"]').exists()).toBe(false);
    expect(wrapper.find('[data-testid="tasks-empty"]').exists()).toBe(true);
  });
});
