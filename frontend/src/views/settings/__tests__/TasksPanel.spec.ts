/**
 * TasksPanel tests — background-task-monitor-01KZNP3C WP05
 */
import { describe, it, expect, vi, beforeEach } from 'vitest';
import { mount, flushPromises } from '@vue/test-utils';
import TasksPanel from '@/views/settings/TasksPanel.vue';
import type { TaskRow } from '@/lib/types';

// Mock the tasks module so we don't need a Wails runtime.
vi.mock('@/lib/tasks', () => ({
  tasksList: vi.fn().mockResolvedValue([]),
  tasksAbort: vi.fn().mockResolvedValue(undefined),
  tasksGet: vi.fn().mockResolvedValue(null),
  tasksTail: vi.fn().mockResolvedValue([]),
}));

import { tasksList, tasksAbort } from '@/lib/tasks';

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

describe('TasksPanel', () => {
  beforeEach(() => {
    vi.mocked(tasksList).mockResolvedValue([]);
    vi.mocked(tasksAbort).mockResolvedValue(undefined);
  });

  it('renders the panel', async () => {
    const wrapper = mount(TasksPanel);
    await flushPromises();
    expect(wrapper.find('[data-testid="tasks-panel"]').exists()).toBe(true);
  });

  it('shows empty state when there are no tasks', async () => {
    vi.mocked(tasksList).mockResolvedValue([]);
    const wrapper = mount(TasksPanel);
    await flushPromises();
    expect(wrapper.find('[data-testid="tasks-empty"]').exists()).toBe(true);
  });

  it('renders running task with abort button', async () => {
    vi.mocked(tasksList).mockResolvedValue([runningTask]);
    const wrapper = mount(TasksPanel);
    await flushPromises();

    const row = wrapper.find(`[data-testid="task-row-${runningTask.id}"]`);
    expect(row.exists()).toBe(true);

    expect(row.find('[data-testid="task-status"]').text()).toContain('running');
    expect(row.find('[data-testid="task-abort-btn"]').exists()).toBe(true);
  });

  it('renders completed task without abort button', async () => {
    vi.mocked(tasksList).mockResolvedValue([completedTask]);
    const wrapper = mount(TasksPanel);
    await flushPromises();

    const row = wrapper.find(`[data-testid="task-row-${completedTask.id}"]`);
    expect(row.exists()).toBe(true);
    expect(row.find('[data-testid="task-abort-btn"]').exists()).toBe(false);
  });

  it('calls tasksAbort and reloads when abort button clicked', async () => {
    vi.mocked(tasksList).mockResolvedValue([runningTask]);
    const wrapper = mount(TasksPanel);
    await flushPromises();

    // Change mock to return empty after abort.
    vi.mocked(tasksList).mockResolvedValue([]);

    await wrapper.find('[data-testid="task-abort-btn"]').trigger('click');
    await flushPromises();

    expect(tasksAbort).toHaveBeenCalledWith(runningTask.id);
  });

  it('emits view-task when view output clicked', async () => {
    vi.mocked(tasksList).mockResolvedValue([runningTask]);
    const wrapper = mount(TasksPanel);
    await flushPromises();

    await wrapper.find('[data-testid="task-view-btn"]').trigger('click');
    expect(wrapper.emitted('view-task')).toBeTruthy();
    expect(wrapper.emitted('view-task')![0]).toEqual([runningTask.id]);
  });

  it('filters by sessionId when prop provided', async () => {
    vi.mocked(tasksList).mockResolvedValue([runningTask, completedTask]);
    const wrapper = mount(TasksPanel, {
      props: { filterSessionId: 'sess-other' },
    });
    await flushPromises();

    // Both tasks belong to sess-1; none should show.
    expect(wrapper.find('[data-testid="tasks-empty"]').exists()).toBe(true);
  });

  it('shows running count when tasks are running', async () => {
    vi.mocked(tasksList).mockResolvedValue([runningTask]);
    const wrapper = mount(TasksPanel);
    await flushPromises();

    expect(wrapper.find('[data-testid="running-count"]').exists()).toBe(true);
    expect(wrapper.find('[data-testid="running-count"]').text()).toContain('1 running');
  });
});
