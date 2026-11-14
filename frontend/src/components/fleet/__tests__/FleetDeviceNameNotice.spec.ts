/**
 * FleetDeviceNameNotice (Fleet ER-43): the device-name disclosure shown where
 * the member signs in and on the Account card. Fake names only.
 */
import { describe, it, expect, vi } from 'vitest';
import { mount, flushPromises } from '@vue/test-utils';
import FleetDeviceNameNotice from '@/components/fleet/FleetDeviceNameNotice.vue';
import { createFakeHarnessClient } from '@/lib/harnessClient';
import { HarnessClientKey } from '@/lib/harnessClientContext';
import { notifyFleetDeviceNameChanged } from '@/lib/fleetDeviceName';
import type { FleetDeviceNameSettings } from '@/lib/types';

function dn(over: Partial<FleetDeviceNameSettings> = {}): FleetDeviceNameSettings {
  return {
    share: true,
    useComputerName: false,
    useNeutralName: false,
    customName: '',
    computerName: 'Example-Workstation',
    defaultName: 'macOS device 3f2a',
    effectiveName: 'Example-Workstation',
    maxLength: 64,
    ...over,
  };
}

function mountWith(get: () => Promise<FleetDeviceNameSettings>) {
  const base = createFakeHarnessClient();
  const getFleetDeviceName = vi.fn(get);
  const client = { ...base, settings: { ...base.settings, getFleetDeviceName } };
  const w = mount(FleetDeviceNameNotice, {
    global: { provide: { [HarnessClientKey as symbol]: client } },
  });
  return { w, getFleetDeviceName };
}

const notice = (w: ReturnType<typeof mountWith>['w']) => w.find('[data-testid="fleet-device-name-notice"]');

describe('FleetDeviceNameNotice', () => {
  it('names the device the organization will see', async () => {
    const { w } = mountWith(async () => dn());
    await flushPromises();
    expect(notice(w).text()).toBe(
      `Your organization's admins will see this device as "Example-Workstation". You can change this in Settings › Account.`,
    );
  });

  it('shows the neutral label when that is what is sent', async () => {
    const { w } = mountWith(async () => dn({ useNeutralName: true, effectiveName: 'macOS device 3f2a' }));
    await flushPromises();
    expect(notice(w).text()).toContain('as "macOS device 3f2a"');
  });

  it('says a neutral name is used when none is resolved yet', async () => {
    const { w } = mountWith(async () => dn({ computerName: '', defaultName: '', effectiveName: '' }));
    await flushPromises();
    expect(notice(w).text()).toBe(
      `Your organization's admins will see this device under a neutral name. You can change this in Settings › Account.`,
    );
  });

  it('shows nothing when sharing is off', async () => {
    const { w } = mountWith(async () => dn({ share: false, effectiveName: '' }));
    await flushPromises();
    expect(notice(w).exists()).toBe(false);
  });

  it('shows nothing, and does not throw, when the setting cannot be read', async () => {
    const { w } = mountWith(async () => {
      throw new Error('unreadable');
    });
    await flushPromises();
    expect(notice(w).exists()).toBe(false);
  });

  it('re-reads after the device-name setting changes', async () => {
    let cur = dn();
    const { w, getFleetDeviceName } = mountWith(async () => cur);
    await flushPromises();
    cur = dn({ customName: 'Test Laptop', effectiveName: 'Test Laptop' });
    notifyFleetDeviceNameChanged();
    await flushPromises();
    expect(getFleetDeviceName).toHaveBeenCalledTimes(2);
    expect(notice(w).text()).toContain('as "Test Laptop"');
  });
});
