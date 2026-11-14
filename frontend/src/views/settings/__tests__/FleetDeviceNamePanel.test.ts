/**
 * FleetDeviceNamePanel (Fleet ER-43): reads the device-name setting, shows
 * the name Fleet admins will see, defaults sharing ON with this computer's
 * name (the neutral name when there is none), and writes each change
 * through Settings_SetFleetDeviceName. Fake names only.
 */
import { describe, it, expect, vi } from 'vitest';
import { mount, flushPromises } from '@vue/test-utils';
import FleetDeviceNamePanel from '@/views/settings/FleetDeviceNamePanel.vue';
import { createFakeHarnessClient } from '@/lib/harnessClient';
import { HarnessClientKey } from '@/lib/harnessClientContext';
import type { FleetDeviceNameSettings } from '@/lib/types';

const NEUTRAL = 'macOS device 3f2a';
const COMPUTER = 'Example-Workstation';

function resolve(dn: FleetDeviceNameSettings, computer: string): string {
  if (!dn.share) return '';
  const custom = dn.customName.trim();
  if (custom) return custom;
  if (!dn.useNeutralName && computer) return computer;
  return NEUTRAL;
}

function setup(initial?: Partial<FleetDeviceNameSettings>) {
  const base = createFakeHarnessClient();
  let stored: FleetDeviceNameSettings = {
    share: true,
    useComputerName: false,
    useNeutralName: false,
    customName: '',
    computerName: COMPUTER,
    defaultName: NEUTRAL,
    effectiveName: '',
    maxLength: 64,
    ...initial,
  };
  if (!initial || initial.effectiveName === undefined) {
    stored.effectiveName = resolve(stored, stored.computerName);
  }
  const get = vi.fn(async () => ({ ...stored }));
  const set = vi.fn(async (dn: FleetDeviceNameSettings) => {
    stored = {
      ...stored,
      share: dn.share,
      useComputerName: dn.useComputerName,
      useNeutralName: dn.useNeutralName && !dn.useComputerName,
      customName: dn.customName.trim(),
    };
    stored.effectiveName = resolve(stored, stored.computerName);
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
const checked = (w: ReturnType<typeof setup>['w'], id: string) =>
  (w.find(`[data-testid="${id}"]`).element as HTMLInputElement).checked;

describe('FleetDeviceNamePanel', () => {
  it("defaults to sharing this computer's name", async () => {
    const { w, set } = setup();
    await flushPromises();
    expect(checked(w, 'fleet-device-name-share')).toBe(true);
    expect(checked(w, 'fleet-device-name-mode-computer')).toBe(true);
    expect(checked(w, 'fleet-device-name-mode-neutral')).toBe(false);
    expect(w.text()).toContain("Share this device's name with your organization");
    expect(w.text()).toContain("By default your organization's Fleet admins see this computer's name");
    expect(w.find('[data-testid="fleet-device-name-computer"]').text()).toContain(COMPUTER);
    expect(w.find('[data-testid="fleet-device-name-neutral"]').text()).toContain(NEUTRAL);
    expect(effective(w)).toBe(`Shown to your organization as ${COMPUTER}.`);
    // Showing the default writes nothing.
    expect(set).not.toHaveBeenCalled();
  });

  it('falls back to the neutral name when the computer name is unavailable', async () => {
    const { w } = setup({ computerName: '' });
    await flushPromises();
    const computer = w.find('[data-testid="fleet-device-name-mode-computer"]').element as HTMLInputElement;
    expect(computer.disabled).toBe(true);
    expect(checked(w, 'fleet-device-name-mode-neutral')).toBe(true);
    expect(effective(w)).toBe(`Shown to your organization as ${NEUTRAL}.`);
  });

  it('choosing the neutral name saves it explicitly', async () => {
    const { w, set } = setup();
    await flushPromises();
    await w.find('[data-testid="fleet-device-name-mode-neutral"]').setValue(true);
    await flushPromises();
    expect(set).toHaveBeenLastCalledWith(
      expect.objectContaining({ share: true, useComputerName: false, useNeutralName: true, customName: '' }),
    );
    expect(effective(w)).toBe(`Shown to your organization as ${NEUTRAL}.`);
    expect(checked(w, 'fleet-device-name-mode-neutral')).toBe(true);
  });

  it('an explicit neutral choice is shown as neutral', async () => {
    const { w } = setup({ useNeutralName: true });
    await flushPromises();
    expect(checked(w, 'fleet-device-name-mode-neutral')).toBe(true);
    expect(effective(w)).toBe(`Shown to your organization as ${NEUTRAL}.`);
  });

  it('choosing the computer name again saves it', async () => {
    const { w, set } = setup({ useNeutralName: true });
    await flushPromises();
    await w.find('[data-testid="fleet-device-name-mode-computer"]').setValue(true);
    await flushPromises();
    expect(set).toHaveBeenLastCalledWith(
      expect.objectContaining({ share: true, useComputerName: true, useNeutralName: false, customName: '' }),
    );
    expect(effective(w)).toBe(`Shown to your organization as ${COMPUTER}.`);
  });

  it('a custom name overrides', async () => {
    const { w, set } = setup();
    await flushPromises();
    await w.find('[data-testid="fleet-device-name-mode-custom"]').setValue(true);
    await flushPromises();
    // Custom with nothing typed is not saved.
    expect(set).not.toHaveBeenCalled();
    await w.find('[data-testid="fleet-device-name-custom"]').setValue('Test Laptop');
    await flushPromises();
    expect(set).toHaveBeenLastCalledWith(
      expect.objectContaining({ share: true, useComputerName: false, useNeutralName: false, customName: 'Test Laptop' }),
    );
    expect(effective(w)).toBe('Shown to your organization as Test Laptop.');
  });

  it('turning sharing off sends nothing and hides the choices', async () => {
    const { w, set } = setup({ customName: 'Test Laptop' });
    await flushPromises();
    await w.find('[data-testid="fleet-device-name-share"]').setValue(false);
    await flushPromises();
    expect(set).toHaveBeenLastCalledWith(expect.objectContaining({ share: false }));
    expect(w.find('[data-testid="fleet-device-name-mode-neutral"]').exists()).toBe(false);
    expect(effective(w)).toBe('No device name is shared.');
  });

  it('turning sharing off always saves, even with an empty custom choice pending', async () => {
    // Review finding: pick "A name I choose", type nothing, untick Share.
    // The off must reach the backend.
    const { w, set } = setup({ useComputerName: true });
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

  it("clearing the custom name falls back to this computer's name", async () => {
    const { w, set } = setup({ customName: 'Test Laptop' });
    await flushPromises();
    await w.find('[data-testid="fleet-device-name-custom"]').setValue('');
    await flushPromises();
    expect(set).toHaveBeenLastCalledWith(
      expect.objectContaining({ share: true, useComputerName: false, useNeutralName: false, customName: '' }),
    );
    expect(effective(w)).toBe(`Shown to your organization as ${COMPUTER}.`);
    expect(checked(w, 'fleet-device-name-mode-computer')).toBe(true);
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
