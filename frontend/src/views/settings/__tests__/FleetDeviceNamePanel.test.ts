/**
 * FleetDeviceNamePanel (Fleet ER-43): reads the device-name setting, shows
 * the name Fleet admins will see, defaults sharing ON with the neutral
 * name, and writes each change through Settings_SetFleetDeviceName.
 * Fake names only.
 */
import { describe, it, expect, vi } from 'vitest';
import { mount, flushPromises } from '@vue/test-utils';
import FleetDeviceNamePanel from '@/views/settings/FleetDeviceNamePanel.vue';
import { createFakeHarnessClient } from '@/lib/harnessClient';
import { HarnessClientKey } from '@/lib/harnessClientContext';
import type { FleetDeviceNameSettings } from '@/lib/types';

const DEFAULT = 'macOS device 3f2a';
const COMPUTER = 'Example-Workstation';

function setup(initial?: Partial<FleetDeviceNameSettings>) {
  const base = createFakeHarnessClient();
  let stored: FleetDeviceNameSettings = {
    share: true,
    useComputerName: false,
    customName: '',
    computerName: COMPUTER,
    defaultName: DEFAULT,
    effectiveName: DEFAULT,
    maxLength: 64,
    ...initial,
  };
  const get = vi.fn(async () => ({ ...stored }));
  const set = vi.fn(async (dn: FleetDeviceNameSettings) => {
    const custom = dn.customName.trim();
    stored = {
      ...stored,
      share: dn.share,
      useComputerName: dn.useComputerName,
      customName: custom,
      effectiveName: !dn.share ? '' : custom || (dn.useComputerName ? COMPUTER : DEFAULT),
    };
  });
  const client = {
    ...base,
    settings: { ...base.settings, getFleetDeviceName: get, setFleetDeviceName: set },
  };
  const w = mount(FleetDeviceNamePanel, {
    global: { provide: { [HarnessClientKey as symbol]: client } },
  });
  return { w, get, set };
}

const effective = (w: ReturnType<typeof setup>['w']) =>
  w.find('[data-testid="fleet-device-name-effective"]').text().replace(/\s+/g, ' ').trim();

describe('FleetDeviceNamePanel', () => {
  it('defaults to sharing the neutral name', async () => {
    const { w } = setup();
    await flushPromises();
    const share = w.find('[data-testid="fleet-device-name-share"]').element as HTMLInputElement;
    const def = w.find('[data-testid="fleet-device-name-mode-default"]').element as HTMLInputElement;
    expect(share.checked).toBe(true);
    expect(def.checked).toBe(true);
    expect(w.text()).toContain("Share this device's name with your organization");
    expect(w.find('[data-testid="fleet-device-name-computer"]').text()).toContain(COMPUTER);
    expect(effective(w)).toBe(`Shown to your organization as ${DEFAULT}.`);
  });

  it('choosing the computer name saves it', async () => {
    const { w, set } = setup();
    await flushPromises();
    await w.find('[data-testid="fleet-device-name-mode-computer"]').setValue(true);
    await flushPromises();
    expect(set).toHaveBeenLastCalledWith(
      expect.objectContaining({ share: true, useComputerName: true, customName: '' }),
    );
    expect(effective(w)).toBe(`Shown to your organization as ${COMPUTER}.`);
  });

  it('a custom name overrides, capped by maxlength', async () => {
    const { w, set } = setup();
    await flushPromises();
    await w.find('[data-testid="fleet-device-name-mode-custom"]').setValue(true);
    await flushPromises();
    // Custom with nothing typed is not saved.
    expect(set).not.toHaveBeenCalled();
    const input = w.find('[data-testid="fleet-device-name-custom"]');
    await input.setValue('Test Laptop');
    await flushPromises();
    expect(set).toHaveBeenLastCalledWith(
      expect.objectContaining({ share: true, useComputerName: false, customName: 'Test Laptop' }),
    );
    expect(effective(w)).toBe('Shown to your organization as Test Laptop.');
  });

  it('turning sharing off sends nothing and hides the choices', async () => {
    const { w, set } = setup({ customName: 'Test Laptop', effectiveName: 'Test Laptop' });
    await flushPromises();
    await w.find('[data-testid="fleet-device-name-share"]').setValue(false);
    await flushPromises();
    expect(set).toHaveBeenLastCalledWith(expect.objectContaining({ share: false }));
    expect(w.find('[data-testid="fleet-device-name-mode-default"]').exists()).toBe(false);
    expect(effective(w)).toBe('No device name is shared.');
  });

  it('disables the computer-name choice when it cannot be read', async () => {
    const { w } = setup({ computerName: '' });
    await flushPromises();
    const computer = w.find('[data-testid="fleet-device-name-mode-computer"]').element as HTMLInputElement;
    expect(computer.disabled).toBe(true);
  });

  it('turning sharing off always saves, even with an empty custom choice pending', async () => {
    // Review finding: stored = computer name; pick "A name I choose", type
    // nothing, untick Share. The off must reach the backend.
    const { w, set } = setup({ useComputerName: true, effectiveName: COMPUTER });
    await flushPromises();
    await w.find('[data-testid="fleet-device-name-mode-custom"]').setValue(true);
    await flushPromises();
    expect(set).not.toHaveBeenCalled();
    await w.find('[data-testid="fleet-device-name-share"]').setValue(false);
    await flushPromises();
    expect(set).toHaveBeenCalledTimes(1);
    expect(set).toHaveBeenLastCalledWith(expect.objectContaining({ share: false, useComputerName: false }));
    expect(effective(w)).toBe('No device name is shared.');
  });

  it('clearing the custom name falls back to the neutral name', async () => {
    const { w, set } = setup({ customName: 'Test Laptop', effectiveName: 'Test Laptop' });
    await flushPromises();
    await w.find('[data-testid="fleet-device-name-custom"]').setValue('');
    await flushPromises();
    expect(set).toHaveBeenLastCalledWith(
      expect.objectContaining({ share: true, useComputerName: false, customName: '' }),
    );
    expect(effective(w)).toBe(`Shown to your organization as ${DEFAULT}.`);
    const def = w.find('[data-testid="fleet-device-name-mode-default"]').element as HTMLInputElement;
    expect(def.checked).toBe(true);
  });

  it('caps the custom name at 64 characters, counting code points', async () => {
    const { w, set } = setup();
    await flushPromises();
    await w.find('[data-testid="fleet-device-name-mode-custom"]').setValue(true);
    await flushPromises();
    // 70 astral characters = 140 UTF-16 units; 64 must survive whole.
    const emoji = '\u{1F600}';
    await w.find('[data-testid="fleet-device-name-custom"]').setValue(emoji.repeat(70));
    await flushPromises();
    const sent = (set.mock.calls.at(-1)?.[0] as FleetDeviceNameSettings).customName;
    expect(Array.from(sent).length).toBe(64);
    expect(sent).toBe(emoji.repeat(64));
  });
});
