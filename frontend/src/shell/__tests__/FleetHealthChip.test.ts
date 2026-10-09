/**
 * FleetHealthChip — fleet config-source chip (bundle-key-rotation WP01).
 *
 * Pins the "unknown-key" branch: FleetHealth.configSource === "unknown-key"
 * (fleet.ErrSigningKeyUnknown — the latest bundle was signed with a key this
 * build never pinned) renders "unknown key — update" in the warn style, with
 * the poller's error in the tooltip. Also pins the neighbouring states so the
 * new branch cannot swallow them.
 */
import { describe, it, expect, vi } from 'vitest';
import { mount, flushPromises } from '@vue/test-utils';
import FleetHealthChip from '@/shell/FleetHealthChip.vue';
import { provideFakeClient } from '@/lib/harnessClientContext';
import type { FleetHealthView, FleetSessionView } from '@/lib/types';
import { dispatchServedEvent } from '@/lib/useServedEvents';

async function mountChip(view: Partial<FleetHealthView>) {
  const full: FleetHealthView = {
    configDistributionEnabled: true,
    configSource: 'fleet',
    configLastError: '',
    signedIn: true,
    ...view,
  };
  const w = mount(FleetHealthChip, {
    global: {
      plugins: [
        {
          install(app) {
            provideFakeClient(app, {
              settings: { fleetHealth: vi.fn().mockResolvedValue(full) } as any,
            });
          },
        },
      ],
    },
  });
  await flushPromises();
  return w;
}

describe('FleetHealthChip', () => {
  it('renders "unknown key — update" in warn style for configSource unknown-key', async () => {
    const err =
      'bundle verification failed (hard-reject): fleet: bundle signed with an unknown key: key_id "630dcd2966c43366" matches none of the 1 key(s) pinned in this build';
    const w = await mountChip({ configSource: 'unknown-key', configLastError: err });
    const chip = w.find('[data-testid="fleet-health-chip"]');
    expect(chip.exists()).toBe(true);
    expect(chip.text()).toBe('fleet: unknown key — update');
    expect(chip.classes()).toContain('text-signal-warn');
    expect(chip.attributes('title')).toContain('bundle signed with an unknown key');
  });

  it('keeps the healthy fleet state green', async () => {
    const w = await mountChip({ configSource: 'fleet' });
    const chip = w.find('[data-testid="fleet-health-chip"]');
    expect(chip.text()).toBe('fleet: fleet');
    expect(chip.classes()).toContain('text-signal-ok');
  });

  it('stays hidden when no signing key is pinned (no-key)', async () => {
    const w = await mountChip({ configDistributionEnabled: false, configSource: 'no-key' });
    expect(w.find('[data-testid="fleet-health-chip"]').exists()).toBe(false);
  });

  it('explains the default-deny state in its tooltip', async () => {
    const w = await mountChip({ configSource: 'default-deny' });
    const chip = w.find('[data-testid="fleet-health-chip"]');
    expect(chip.text()).toBe('fleet: default-deny');
    expect(chip.attributes('title')).toContain('no fleet config bundle has ever been applied');
  });
});

// Dogfood 2026-10-08 P2: the chip read fleetHealth once on mount and showed
// `default-deny` all session after a verified bundle apply.
describe('FleetHealthChip freshness', () => {
  function mountSequenced() {
    const views: FleetHealthView[] = [
      { configDistributionEnabled: true, configSource: 'default-deny', configLastError: '', signedIn: true },
      { configDistributionEnabled: true, configSource: 'fleet', configLastError: '', signedIn: true },
    ];
    let i = 0;
    const fleetHealth = vi.fn(async () => views[Math.min(i++, views.length - 1)]);
    const w = mount(FleetHealthChip, {
      global: {
        plugins: [
          {
            install(app) {
              provideFakeClient(app, { settings: { fleetHealth } as any });
            },
          },
        ],
      },
    });
    return { w, fleetHealth };
  }

  const snapshot = (n: number): FleetSessionView =>
    ({
      state: 'signed_in',
      autoRetry: true,
      tokensUsable: true,
      claims: { hasSubject: true, hasOrgClaim: true },
      capabilities: { tier: 'team', enabled: { [`cap${n}`]: true }, fetchedAt: '', source: 'fleet' },
      sync: {},
      updatedAt: String(n),
    }) as unknown as FleetSessionView;

  it('re-reads fleet health when a fleet:session-changed event arrives', async () => {
    const { w, fleetHealth } = mountSequenced();
    await flushPromises();
    expect(w.find('[data-testid="fleet-health-chip"]').text()).toBe('fleet: default-deny');
    const before = fleetHealth.mock.calls.length;

    dispatchServedEvent('fleet:session-changed', snapshot(1));
    await flushPromises();

    expect(fleetHealth.mock.calls.length).toBeGreaterThan(before);
    expect(w.find('[data-testid="fleet-health-chip"]').text()).toBe('fleet: fleet');
    w.unmount();
  });

  it('re-reads on window focus and on the 60s interval, and stops on unmount', async () => {
    vi.useFakeTimers();
    try {
      const { w, fleetHealth } = mountSequenced();
      await flushPromises();
      const afterMount = fleetHealth.mock.calls.length;

      window.dispatchEvent(new Event('focus'));
      await flushPromises();
      expect(fleetHealth.mock.calls.length).toBeGreaterThan(afterMount);

      const afterFocus = fleetHealth.mock.calls.length;
      vi.advanceTimersByTime(60_000);
      await flushPromises();
      expect(fleetHealth.mock.calls.length).toBeGreaterThan(afterFocus);

      w.unmount();
      const afterUnmount = fleetHealth.mock.calls.length;
      vi.advanceTimersByTime(180_000);
      window.dispatchEvent(new Event('focus'));
      await flushPromises();
      expect(fleetHealth.mock.calls.length).toBe(afterUnmount);
    } finally {
      vi.useRealTimers();
    }
  });
});
