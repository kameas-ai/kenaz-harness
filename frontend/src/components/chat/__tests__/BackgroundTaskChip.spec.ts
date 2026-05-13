/**
 * BackgroundTaskChip tests — background-task-monitor-01KZNP3C WP06
 */
import { describe, it, expect, vi, beforeEach } from 'vitest';
import { mount, flushPromises } from '@vue/test-utils';
import BackgroundTaskChip from '@/components/chat/BackgroundTaskChip.vue';
import type { TaskRow } from '@/lib/types';

vi.mock('@/lib/tasks', () => ({
  tasksList: vi.fn().mockResolvedValue([]),
}));

import { tasksList } from '@/lib/tasks';

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

describe('BackgroundTaskChip', () => {
  beforeEach(() => {
    vi.mocked(tasksList).mockResolvedValue([]);
  });

  it('is hidden when there are no running tasks', async () => {
    vi.mocked(tasksList).mockResolvedValue([]);
    const wrapper = mount(BackgroundTaskChip, {
      props: { sessionId: 'sess-1' },
    });
    await flushPromises();
    expect(wrapper.find('[data-testid="background-task-chip"]').exists()).toBe(false);
  });

  it('shows chip with count when tasks are running for the session', async () => {
    vi.mocked(tasksList).mockResolvedValue([makeTask()]);
    const wrapper = mount(BackgroundTaskChip, {
      props: { sessionId: 'sess-1' },
    });
    await flushPromises();
    const chip = wrapper.find('[data-testid="background-task-chip"]');
    expect(chip.exists()).toBe(true);
    expect(chip.text()).toContain('1 running');
  });

  it('hides chip for tasks belonging to a different session', async () => {
    vi.mocked(tasksList).mockResolvedValue([makeTask({ ownerSessionId: 'sess-other' })]);
    const wrapper = mount(BackgroundTaskChip, {
      props: { sessionId: 'sess-1' },
    });
    await flushPromises();
    expect(wrapper.find('[data-testid="background-task-chip"]').exists()).toBe(false);
  });

  it('emits open-tasks when chip is clicked', async () => {
    vi.mocked(tasksList).mockResolvedValue([makeTask()]);
    const wrapper = mount(BackgroundTaskChip, {
      props: { sessionId: 'sess-1' },
    });
    await flushPromises();
    await wrapper.find('[data-testid="background-task-chip"]').trigger('click');
    expect(wrapper.emitted('open-tasks')).toBeTruthy();
  });

  it('hides chip when task is completed (not running)', async () => {
    vi.mocked(tasksList).mockResolvedValue([makeTask({ status: 'completed' })]);
    const wrapper = mount(BackgroundTaskChip, {
      props: { sessionId: 'sess-1' },
    });
    await flushPromises();
    expect(wrapper.find('[data-testid="background-task-chip"]').exists()).toBe(false);
  });

  it('shows count for multiple running tasks', async () => {
    vi.mocked(tasksList).mockResolvedValue([
      makeTask({ id: 'task-1' }),
      makeTask({ id: 'task-2' }),
      makeTask({ id: 'task-3', status: 'completed' }),
    ]);
    const wrapper = mount(BackgroundTaskChip, {
      props: { sessionId: 'sess-1' },
    });
    await flushPromises();
    expect(wrapper.find('[data-testid="background-task-chip"]').text()).toContain('2 running');
  });
});
