/**
 * LockdownBanner — fleet-emergency-lockdown-01NDFSEX12 WP05.
 *
 * Covers:
 *   1. Hidden when lockdown is inactive (default).
 *   2. Visible when seeded active=true via the settings RPC.
 *   3. Shows reason text when present.
 *   4. Shows default copy when reason is empty.
 *   5. Becomes visible when a fleet:lockdown:changed event fires active=true.
 *   6. Hides again when a fleet:lockdown:changed event fires active=false.
 */
import { describe, it, expect, beforeEach, afterEach, vi } from 'vitest';
import { mount, flushPromises } from '@vue/test-utils';
import LockdownBanner from '@/components/ui/LockdownBanner.vue';
import { provideFakeClient } from '@/lib/harnessClientContext';
import { setConnectionState } from '@/lib/useConnectionState';

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

function mountBanner(
  lockdownStatus: { active: boolean; reason?: string } = { active: false, reason: '' },
) {
  const fleetLockdownStatus = vi.fn().mockResolvedValue(lockdownStatus);
  const w = mount(LockdownBanner, {
    global: {
      plugins: [
        {
          install(app) {
            provideFakeClient(app, {
              settings: { fleetLockdownStatus } as any,
            });
          },
        },
      ],
    },
  });
  return { w, fleetLockdownStatus };
}

describe('LockdownBanner (fleet-emergency-lockdown-01NDFSEX12 WP05)', () => {
  beforeEach(() => {
    installFakeRuntime();
    setConnectionState('ready');
  });
  afterEach(() => {
    uninstallRuntime();
    vi.restoreAllMocks();
  });

  it('is hidden when lockdown is inactive', async () => {
    const { w } = mountBanner({ active: false });
    await flushPromises();
    expect(w.find('[data-testid="lockdown-banner"]').exists()).toBe(false);
  });

  it('is visible when seeded active=true', async () => {
    const { w } = mountBanner({ active: true, reason: '' });
    await flushPromises();
    expect(w.find('[data-testid="lockdown-banner"]').exists()).toBe(true);
  });

  it('shows reason text when provided', async () => {
    const { w } = mountBanner({ active: true, reason: 'Security incident' });
    await flushPromises();
    expect(w.find('[data-testid="lockdown-banner-reason"]').text()).toContain('Security incident');
  });

  it('shows default copy when reason is empty', async () => {
    const { w } = mountBanner({ active: true, reason: '' });
    await flushPromises();
    expect(w.text()).toContain('Admin has suspended all AI actions');
  });

  it('becomes visible when fleet:lockdown:changed fires active=true', async () => {
    const rt = installFakeRuntime();
    const { w } = mountBanner({ active: false });
    await flushPromises();
    expect(w.find('[data-testid="lockdown-banner"]').exists()).toBe(false);

    rt.emit('fleet:lockdown:changed', { active: true, reason: 'drill' });
    await flushPromises();
    expect(w.find('[data-testid="lockdown-banner"]').exists()).toBe(true);
  });

  it('hides again when fleet:lockdown:changed fires active=false', async () => {
    const rt = installFakeRuntime();
    const { w } = mountBanner({ active: true, reason: '' });
    await flushPromises();
    expect(w.find('[data-testid="lockdown-banner"]').exists()).toBe(true);

    rt.emit('fleet:lockdown:changed', { active: false });
    await flushPromises();
    expect(w.find('[data-testid="lockdown-banner"]').exists()).toBe(false);
  });
});
