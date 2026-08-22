/**
 * SettingsView.tasks.spec.ts — the Tasks sub-tab mounts and shows real
 * task data (subagent-control-and-background-tasks-01PMZB11 UNIT-11,
 * AC-12 second half).
 *
 * Goes through the real parent (SettingsView, reached via ?tab=tasks
 * exactly as SettingsTabs.vue's nav entry does) rather than mounting
 * TasksPanel directly — mirrors SettingsView.branchAdvisor.spec.ts's
 * rationale: a regression that un-mounts the panel again fails here even
 * if the component-level spec stays green.
 *
 * FALSIFICATION (tasks.md UNIT-11 AC-12): "revert UNIT-3's BackgroundSpawn
 * assignment [in core/rpc/builtins_wiring.go]. The panel must render
 * empty and the test must fail." That revert makes
 * core/rpc/background_task_wiring_test.go's
 * TestBashBackgroundMode_ProductionWiring_RegistersATaskRow fail — Go's
 * Tasks_List-backing Registry.List() goes empty for a real background
 * task (pinned there, re-verified by hand for this unit — see the
 * mission report). This file pins the matching frontend half of the same
 * chain: an empty Tasks_List response renders ONLY the empty state, never
 * a stale/fixture row — so the two tests compose into the full backend
 * -to-UI proof without needing a second Wails-booted integration harness.
 */
import { describe, it, expect, vi } from 'vitest';
import { mount, flushPromises } from '@vue/test-utils';

vi.mock('vue-router', async (importOriginal) => {
  const actual = await importOriginal<typeof import('vue-router')>();
  return {
    ...actual,
    useRoute: () => ({ query: { tab: 'tasks' }, path: '/settings' }),
    useRouter: () => undefined,
  };
});

import SettingsView from '@/views/settings/SettingsView.vue';
import { createFakeHarnessClient } from '@/lib/harnessClient';
import { HarnessClientKey } from '@/lib/harnessClientContext';
import type { Settings, TaskRow } from '@/lib/types';

const liveTask: TaskRow = {
  id: 'task-live-9f3a',
  kind: 'bash',
  ownerSessionId: 'sess-1',
  cmd: 'echo unit3_output_capture_marker_9f3a',
  description: 'UNIT-11 e2e-shape probe',
  status: 'running',
  exitCode: 0,
  startedAt: new Date().toISOString(),
  ageMs: 500,
};

function provide(tasksListResult: TaskRow[] = []) {
  const settings: Settings = {
    schemaVersion: 1,
    lastRoute: '/sessions',
    theme: 'dark',
    accent: 'default',
    windowSize: { width: 1280, height: 800 },
  };
  const list = vi.fn(async () => [...tasksListResult]);
  const client = createFakeHarnessClient({
    settings: {
      get: async () => settings,
      set: vi.fn().mockResolvedValue(undefined),
      loadRoute: async () => settings.lastRoute,
      saveRoute: async () => undefined,
      logRouteChange: async () => undefined,
      loadTheme: async () => settings.theme,
      saveTheme: async () => undefined,
      getMemory: async () => false,
      setMemory: async () => undefined,
      getWebFetchEnabled: async () => false,
      setWebFetchEnabled: async () => undefined,
    } as any,
    appInfo: async () => ({
      build: '0.1.0-test',
      commit: 'abcdef0',
      buildTime: '2026-04-25T00:00:00Z',
      goVersion: 'go1.23.0',
      platform: 'darwin/arm64',
      windowSize: settings.windowSize,
    }),
    Tasks_List: list,
  });
  return { client, list };
}

describe('SettingsView — Tasks sub-tab (UNIT-11, AC-12)', () => {
  it('mounts TasksPanel through the real ?tab=tasks click path and shows the rail sub-title', async () => {
    const { client } = provide();
    const w = mount(SettingsView, {
      global: { provide: { [HarnessClientKey as symbol]: client } },
    });
    await flushPromises();

    expect(w.find('[data-testid="settings-tasks-pane"]').exists()).toBe(true);
    expect(w.find('[data-testid="tasks-panel"]').exists()).toBe(true);
    // Sanity: a different sub-tab's pane is NOT also rendered.
    expect(w.find('[data-testid="settings-scheduledchats-pane"]').exists()).toBe(false);
  });

  it('renders a row for a live background task', async () => {
    const { client } = provide([liveTask]);
    const w = mount(SettingsView, {
      global: { provide: { [HarnessClientKey as symbol]: client } },
    });
    await flushPromises();

    const row = w.find(`[data-testid="task-row-${liveTask.id}"]`);
    expect(row.exists()).toBe(true);
    expect(row.text()).toContain('UNIT-11 e2e-shape probe');
  });

  it('renders EMPTY when Tasks_List returns zero rows — the BackgroundSpawn-reverted shape', async () => {
    const { client } = provide([]);
    const w = mount(SettingsView, {
      global: { provide: { [HarnessClientKey as symbol]: client } },
    });
    await flushPromises();

    expect(w.find('[data-testid="task-list"]').exists()).toBe(false);
    expect(w.find('[data-testid="tasks-empty"]').exists()).toBe(true);
  });

  it('clicking "View output" swaps the pane to TaskOutputViewer for that task, and Back returns to the list', async () => {
    const { client } = provide([liveTask]);
    const w = mount(SettingsView, {
      global: { provide: { [HarnessClientKey as symbol]: client } },
    });
    await flushPromises();

    await w.find('[data-testid="task-view-btn"]').trigger('click');
    await flushPromises();

    const viewer = w.find('[data-testid="task-output-viewer"]');
    expect(viewer.exists()).toBe(true);
    expect(viewer.attributes('data-task-id')).toBe(liveTask.id);
    expect(w.find('[data-testid="tasks-panel"]').exists()).toBe(false);

    await w.find('[data-testid="tasks-viewer-back-btn"]').trigger('click');
    await flushPromises();

    expect(w.find('[data-testid="tasks-panel"]').exists()).toBe(true);
    expect(w.find('[data-testid="task-output-viewer"]').exists()).toBe(false);
  });
});
