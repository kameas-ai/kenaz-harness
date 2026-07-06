/**
 * UpdateMenu.spec — covers the platform-aware primary CTA, the
 * Skip/Later/Settings actions, and the download-progress affordance.
 *
 * The menu is mounted directly here (not via the indicator) so we can stage
 * each store state explicitly without orchestrating a click first.
 */

import { describe, it, expect, beforeEach, afterEach, vi } from 'vitest';
import { mount, flushPromises } from '@vue/test-utils';
import UpdateMenu from '@/components/updates/UpdateMenu.vue';
import {
  __resetUpdateStoreForTests,
  useUpdateStore,
} from '@/components/updates/useUpdateStore';
import {
  createFakeUpdateClient,
  type UpdateClient,
  type UpdateStatus,
} from '@/lib/updateClient';
import { _resetPlatformCache } from '@/lib/shortcuts/platform';
import { provideFakeClient } from '@/lib/harnessClientContext';

const mountOpts = {
  global: {
    plugins: [(app: { provide: (...a: unknown[]) => void }) => {
      provideFakeClient(app as never);
    }],
  },
};

const baseStatus: UpdateStatus = {
  currentVersion: '0.4.0',
  available: true,
  availableVersion: '0.4.1',
  channel: 'stable',
  downloadState: 'idle',
  notes: 'Polish + bug fixes.',
  releaseUrl: 'https://github.com/kenaz/releases/tag/v0.4.1',
};

function setPlatform(p: 'mac' | 'win' | 'linux') {
  _resetPlatformCache();
  if (typeof localStorage !== 'undefined') {
    localStorage.setItem('kenaz_shortcut_platform', p);
  }
}

async function bootStoreWith(
  status: UpdateStatus,
  seed?: Partial<UpdateClient>,
): Promise<void> {
  __resetUpdateStoreForTests({
    client: createFakeUpdateClient({
      status: () => Promise.resolve(status),
      ...seed,
    }),
  });
  const store = useUpdateStore();
  await store.ensureBoot();
}

describe('UpdateMenu', () => {
  beforeEach(() => {
    _resetPlatformCache();
  });
  afterEach(() => {
    if (typeof localStorage !== 'undefined') {
      localStorage.removeItem('kenaz_shortcut_platform');
    }
    _resetPlatformCache();
    __resetUpdateStoreForTests();
    vi.restoreAllMocks();
  });

  it('shows "Install & Restart" on Mac', async () => {
    setPlatform('mac');
    await bootStoreWith(baseStatus);
    const w = mount(UpdateMenu, mountOpts);
    await flushPromises();
    expect(w.find('[data-testid="update-menu-primary"]').text()).toBe(
      'Install & Restart',
    );
  });

  it('shows "Install & Restart" on Linux', async () => {
    setPlatform('linux');
    await bootStoreWith(baseStatus);
    const w = mount(UpdateMenu, mountOpts);
    await flushPromises();
    expect(w.find('[data-testid="update-menu-primary"]').text()).toBe(
      'Install & Restart',
    );
  });

  it('shows "Install & Restart" on Windows (helper updater path)', async () => {
    // v0.4.0 ships kenaz-updater.exe alongside the main binary, so the
    // primary CTA is the same as Mac/Linux. The helper handles the
    // wait-rename-relaunch dance outside the harness process.
    setPlatform('win');
    await bootStoreWith(baseStatus);
    const w = mount(UpdateMenu, mountOpts);
    await flushPromises();
    expect(w.find('[data-testid="update-menu-primary"]').text()).toBe(
      'Install & Restart',
    );
  });

  it('renders the headline + notes for the offered version', async () => {
    setPlatform('mac');
    await bootStoreWith(baseStatus);
    const w = mount(UpdateMenu, mountOpts);
    await flushPromises();
    expect(w.find('[data-testid="update-menu-headline"]').text()).toContain(
      '0.4.1',
    );
    expect(w.find('[data-testid="update-menu-notes"]').text()).toContain(
      'Polish',
    );
  });

  it('renders the download progress bar while downloading', async () => {
    setPlatform('mac');
    await bootStoreWith({
      ...baseStatus,
      downloadState: 'downloading',
      downloadProgress: 0.42,
    });
    const w = mount(UpdateMenu, mountOpts);
    await flushPromises();
    const progress = w.find('[data-testid="update-menu-progress"]');
    expect(progress.exists()).toBe(true);
    expect(w.text()).toContain('42%');
    const fill = w.find('[data-testid="update-menu-progress-fill"]');
    expect((fill.element as HTMLElement).style.width).toBe('42%');
  });

  it('renders the "ready to apply" hint when staged', async () => {
    setPlatform('mac');
    await bootStoreWith({ ...baseStatus, downloadState: 'staged' });
    const w = mount(UpdateMenu, mountOpts);
    await flushPromises();
    expect(w.find('[data-testid="update-menu-staged"]').exists()).toBe(true);
  });

  it('shows the failed banner when the last attempt failed', async () => {
    setPlatform('mac');
    await bootStoreWith({ ...baseStatus, downloadState: 'failed' });
    const w = mount(UpdateMenu, mountOpts);
    await flushPromises();
    expect(w.find('[data-testid="update-menu-failed"]').exists()).toBe(true);
    expect(w.find('[data-testid="update-menu-primary"]').text()).toBe('Retry');
  });

  it('emits close when Later is clicked', async () => {
    setPlatform('mac');
    await bootStoreWith(baseStatus);
    const w = mount(UpdateMenu, mountOpts);
    await flushPromises();
    await w.find('[data-testid="update-menu-later"]').trigger('click');
    expect(w.emitted('close')).toBeTruthy();
  });

  it('persists Skip via the bridge AND closes', async () => {
    setPlatform('mac');
    const skipFn = vi.fn().mockResolvedValue(undefined);
    await bootStoreWith(baseStatus, { skipVersion: skipFn });
    const w = mount(UpdateMenu, mountOpts);
    await flushPromises();
    await w.find('[data-testid="update-menu-skip"]').trigger('click');
    await flushPromises();
    expect(skipFn).toHaveBeenCalledWith('0.4.1');
    expect(w.emitted('close')).toBeTruthy();
    expect(localStorage.getItem('kenaz_update_skipped_version')).toBe('0.4.1');
  });

  it('emits navigate-settings when Settings is clicked', async () => {
    setPlatform('mac');
    await bootStoreWith(baseStatus);
    const w = mount(UpdateMenu, mountOpts);
    await flushPromises();
    await w.find('[data-testid="update-menu-settings"]').trigger('click');
    expect(w.emitted('navigate-settings')).toBeTruthy();
    expect(w.emitted('close')).toBeTruthy();
  });

  it('drives the primary CTA: starts download then applies', async () => {
    setPlatform('mac');
    const download = vi.fn().mockResolvedValue(undefined);
    const apply = vi.fn().mockResolvedValue(undefined);
    await bootStoreWith(baseStatus, {
      startDownload: download,
      apply,
    });
    const w = mount(UpdateMenu, mountOpts);
    await flushPromises();
    await w.find('[data-testid="update-menu-primary"]').trigger('click');
    await flushPromises();
    expect(download).toHaveBeenCalledTimes(1);
    expect(apply).toHaveBeenCalledTimes(1);
    expect(w.emitted('close')).toBeTruthy();
  });

  it('skips the download call when the binary is already staged', async () => {
    setPlatform('mac');
    const download = vi.fn().mockResolvedValue(undefined);
    const apply = vi.fn().mockResolvedValue(undefined);
    await bootStoreWith(
      { ...baseStatus, downloadState: 'staged' },
      { startDownload: download, apply },
    );
    const w = mount(UpdateMenu, mountOpts);
    await flushPromises();
    await w.find('[data-testid="update-menu-primary"]').trigger('click');
    await flushPromises();
    expect(download).not.toHaveBeenCalled();
    expect(apply).toHaveBeenCalledTimes(1);
  });
});
