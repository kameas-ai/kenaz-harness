/**
 * TaskOutputViewer tests — background-task-monitor-01KZNP3C WP05.
 *
 * This component had NO test file and no importer anywhere in the tree
 * before subagent-control-and-background-tasks-01PMZB11 UNIT-11 mounted
 * it from SettingsView.vue's Tasks pane (TasksPanel's "View output"
 * button). Routes through the typed harnessClient (Tasks_Get /
 * Tasks_Tail) — see TasksPanel.spec.ts's header comment.
 */
import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest';
import { mount, flushPromises } from '@vue/test-utils';
import TaskOutputViewer from '@/views/settings/TaskOutputViewer.vue';
import { createFakeHarnessClient } from '@/lib/harnessClient';
import { HarnessClientKey } from '@/lib/harnessClientContext';
import type { LineRow, TaskRow } from '@/lib/types';

const runningTask: TaskRow = {
  id: 'task-live',
  kind: 'bash',
  ownerSessionId: 'sess-1',
  cmd: 'echo hi',
  description: '',
  status: 'running',
  exitCode: 0,
  startedAt: new Date().toISOString(),
  ageMs: 100,
};

const completedTask: TaskRow = { ...runningTask, id: 'task-done', status: 'completed' };

function makeLine(offset: number, text: string, stream: 'stdout' | 'stderr' = 'stdout'): LineRow {
  return { stream, text, offset, at: new Date().toISOString() };
}

function mountViewer(task: TaskRow, lines: LineRow[]) {
  const get = vi.fn(async () => task);
  const tail = vi.fn(async (_id: string, fromOffset: number) =>
    lines.filter((l) => l.offset >= fromOffset),
  );
  const client = createFakeHarnessClient({
    Tasks_Get: get,
    Tasks_Tail: tail,
  });
  const wrapper = mount(TaskOutputViewer, {
    props: { taskId: task.id },
    global: { provide: { [HarnessClientKey as symbol]: client } },
  });
  return { wrapper, get, tail };
}

describe('TaskOutputViewer', () => {
  beforeEach(() => {
    vi.useFakeTimers();
  });
  afterEach(() => {
    vi.useRealTimers();
  });

  it('renders the viewer container scoped to the given taskId', async () => {
    const { wrapper } = mountViewer(runningTask, []);
    await flushPromises();
    const el = wrapper.find('[data-testid="task-output-viewer"]');
    expect(el.exists()).toBe(true);
    expect(el.attributes('data-task-id')).toBe(runningTask.id);
  });

  it('shows real output lines from Tasks_Tail — proves it reads a live producer, not a fixture', async () => {
    const lines = [makeLine(0, 'unit11_falsification_marker')];
    const { wrapper, tail } = mountViewer(runningTask, lines);
    await flushPromises();
    expect(tail).toHaveBeenCalledWith(runningTask.id, 0);
    expect(wrapper.find('[data-testid="viewer-output"]').text()).toContain(
      'unit11_falsification_marker',
    );
  });

  it('shows "(no output yet)" when Tasks_Tail returns zero lines', async () => {
    const { wrapper } = mountViewer(runningTask, []);
    await flushPromises();
    expect(wrapper.find('[data-testid="viewer-output"]').text()).toContain('(no output yet)');
  });

  it('shows the running status label for a running task', async () => {
    const { wrapper } = mountViewer(runningTask, []);
    await flushPromises();
    expect(wrapper.find('[data-testid="viewer-status"]').text()).toContain('running');
  });

  it('shows the completed status label and stops polling for a terminal task', async () => {
    const { wrapper, tail } = mountViewer(completedTask, [makeLine(0, 'done')]);
    await flushPromises();
    expect(wrapper.find('[data-testid="viewer-status"]').text()).toContain('completed');
    tail.mockClear();
    await vi.advanceTimersByTimeAsync(2000);
    expect(tail).not.toHaveBeenCalled();
  });

  it('polls for new lines while the task is still running', async () => {
    const lines = [makeLine(0, 'first')];
    const { wrapper, tail } = mountViewer(runningTask, lines);
    await flushPromises();
    expect(wrapper.find('[data-testid="viewer-output"]').text()).toContain('first');

    lines.push(makeLine(1, 'second'));
    await vi.advanceTimersByTimeAsync(500);
    await flushPromises();
    expect(tail).toHaveBeenCalledWith(runningTask.id, 1);
    expect(wrapper.find('[data-testid="viewer-output"]').text()).toContain('second');
  });

  it('re-fetches from offset 0 when taskId changes', async () => {
    const { wrapper, get, tail } = mountViewer(runningTask, [makeLine(0, 'first-task-line')]);
    await flushPromises();

    get.mockResolvedValue(completedTask);
    tail.mockImplementation(async (_id: string, fromOffset: number) =>
      fromOffset === 0 ? [makeLine(0, 'second-task-line')] : [],
    );
    await wrapper.setProps({ taskId: completedTask.id });
    await flushPromises();

    expect(tail).toHaveBeenCalledWith(completedTask.id, 0);
    expect(wrapper.find('[data-testid="viewer-output"]').text()).toContain('second-task-line');
    expect(wrapper.find('[data-testid="viewer-output"]').text()).not.toContain('first-task-line');
  });
});
