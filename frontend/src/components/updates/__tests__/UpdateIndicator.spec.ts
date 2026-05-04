/**
 * UpdateIndicator.spec — the Chrome-style dot. We assert:
 *   - hidden when no update is offered
 *   - visible when status.available, with the correct dot state
 *   - clicking opens the menu (UpdateMenu mounts; popover element appears)
 *   - hidden again once the user skips this version
 */

import { describe, it, expect, beforeEach, afterEach, vi } from 'vitest';
import { mount, flushPromises } from '@vue/test-utils';
import UpdateIndicator from '@/components/updates/UpdateIndicator.vue';
import {
  __resetUpdateStoreForTests,
  useUpdateStore,
} from '@/components/updates/useUpdateStore';
import {
  createFakeUpdateClient,
  type UpdateStatus,
} from '@/lib/updateClient';
import { provideFakeClient } from '@/lib/harnessClientContext';

const mountOpts = {
  global: {
    plugins: [(app: { provide: (...a: unknown[]) => void }) => {
      provideFakeClient(app as never);
    }],
  },
};

interface FakeRuntime {
  EventsOn: (topic: string, cb: (payload: unknown) => void) => () => void;
  emit: (topic: string, payload: unknown) => void;
  handlers: Map<string, Set<(payload: unknown) => void>>;
}

function installFakeRuntime(): FakeRuntime {
  const handlers = new Map<string, Set<(payload: unknown) => void>>();
  const rt: FakeRuntime = {
    handlers,
    EventsOn: (topic, cb) => {
      let s = handlers.get(topic);
      if (!s) {
        s = new Set();
        handlers.set(topic, s);
      }
      s.add(cb);
      return () => s!.delete(cb);
    },
    emit: (topic, payload) => {
      const s = handlers.get(topic);
      if (!s) return;
      for (const cb of s) cb(payload);
    },
  };
  (window as unknown as { runtime: FakeRuntime }).runtime = rt;
  return rt;
}

function uninstallRuntime() {
  delete (window as unknown as { runtime?: unknown }).runtime;
}

const noUpdate: UpdateStatus = {
  currentVersion: '0.4.0',
  available: false,
  channel: 'stable',
  downloadState: 'idle',
};

const updateAvailable: UpdateStatus = {
  currentVersion: '0.4.0',
  available: true,
  availableVersion: '0.4.1',
  channel: 'stable',
  downloadState: 'idle',
  notes: 'Polish + bug fixes.',
  releaseUrl: 'https://github.com/kenaz/releases/tag/v0.4.1',
};

describe('UpdateIndicator', () => {
  beforeEach(() => {
    installFakeRuntime();
  });
  afterEach(() => {
    uninstallRuntime();
    __resetUpdateStoreForTests();
    vi.restoreAllMocks();
  });

  it('renders nothing when no update is available', async () => {
    __resetUpdateStoreForTests({
      client: createFakeUpdateClient({
        status: () => Promise.resolve(noUpdate),
      }),
    });
    const w = mount(UpdateIndicator, mountOpts);
    await flushPromises();
    expect(w.find('[data-testid="update-indicator"]').exists()).toBe(false);
  });

  it('renders the dot when status.available is true', async () => {
    __resetUpdateStoreForTests({
      client: createFakeUpdateClient({
        status: () => Promise.resolve(updateAvailable),
      }),
    });
    const w = mount(UpdateIndicator, mountOpts);
    await flushPromises();
    const btn = w.find('[data-testid="update-indicator"]');
    expect(btn.exists()).toBe(true);
    const dot = w.find('[data-testid="update-indicator-dot"]');
    expect(dot.exists()).toBe(true);
    expect(dot.attributes('data-state')).toBe('pulsing');
  });

  it('shows the staged (solid ok) state when downloadState is staged', async () => {
    __resetUpdateStoreForTests({
      client: createFakeUpdateClient({
        status: () =>
          Promise.resolve({ ...updateAvailable, downloadState: 'staged' }),
      }),
    });
    const w = mount(UpdateIndicator, mountOpts);
    await flushPromises();
    const dot = w.find('[data-testid="update-indicator-dot"]');
    expect(dot.attributes('data-state')).toBe('solid');
    expect(dot.classes()).toContain('bg-signal-ok');
  });

  it('shows the failed state when downloadState is failed', async () => {
    __resetUpdateStoreForTests({
      client: createFakeUpdateClient({
        status: () =>
          Promise.resolve({ ...updateAvailable, downloadState: 'failed' }),
      }),
    });
    const w = mount(UpdateIndicator, mountOpts);
    await flushPromises();
    const dot = w.find('[data-testid="update-indicator-dot"]');
    expect(dot.attributes('data-state')).toBe('failed');
    expect(dot.classes()).toContain('bg-signal-danger');
  });

  it('opens the update menu when clicked', async () => {
    __resetUpdateStoreForTests({
      client: createFakeUpdateClient({
        status: () => Promise.resolve(updateAvailable),
      }),
    });
    const w = mount(UpdateIndicator, mountOpts);
    await flushPromises();
    expect(w.find('[data-testid="update-menu-popover"]').exists()).toBe(false);
    await w.find('[data-testid="update-indicator"]').trigger('click');
    expect(w.find('[data-testid="update-menu-popover"]').exists()).toBe(true);
    expect(w.text()).toContain('Kenaz 0.4.1 available');
  });

  it('responds to a broker push that flips status to available', async () => {
    __resetUpdateStoreForTests({
      client: createFakeUpdateClient({
        status: () => Promise.resolve(noUpdate),
      }),
    });
    const rt = installFakeRuntime();
    const w = mount(UpdateIndicator, mountOpts);
    await flushPromises();
    expect(w.find('[data-testid="update-indicator"]').exists()).toBe(false);

    rt.emit('update:available', updateAvailable);
    await flushPromises();

    expect(w.find('[data-testid="update-indicator"]').exists()).toBe(true);
  });

  it('hides the dot once the user has skipped this version', async () => {
    __resetUpdateStoreForTests({
      client: createFakeUpdateClient({
        status: () => Promise.resolve(updateAvailable),
      }),
    });
    const w = mount(UpdateIndicator, mountOpts);
    await flushPromises();
    expect(w.find('[data-testid="update-indicator"]').exists()).toBe(true);

    const store = useUpdateStore();
    await store.skipVersion('0.4.1');
    await flushPromises();

    expect(w.find('[data-testid="update-indicator"]').exists()).toBe(false);
  });
});
