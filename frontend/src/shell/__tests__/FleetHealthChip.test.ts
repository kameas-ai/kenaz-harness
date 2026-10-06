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
import type { FleetHealthView } from '@/lib/types';

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
});
