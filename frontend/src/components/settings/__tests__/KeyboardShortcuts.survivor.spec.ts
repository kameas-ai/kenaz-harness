/**
 * KeyboardShortcuts.survivor.spec.ts — the load-bearing assertion for
 * WP04's deletion half (consent-surfaces-truth-01PMTR01 / dead-code-audit
 * B6).
 *
 * B6 deleted the rival PermissionsClient.getShortcuts/setShortcut/
 * setShortcuts door (dead: zero callers, backed by
 * Settings_GetShortcuts/_SetShortcut/_SetShortcuts bindings that were
 * never reached from the frontend). For a deletion WP the assertion that
 * matters is on the SURVIVOR, not the corpse: KeyboardShortcuts.vue never
 * used the deleted door in the first place — it always persisted through
 * client.settings.get() / client.settings.set() (the full-settings
 * round-trip every other SettingsView section uses, via debouncedSave).
 * This test proves that live path still works after the rival door is
 * gone.
 *
 * Mutation: make persistOverrides() a no-op (or stop spreading
 * keyboardShortcuts into the saved object) in KeyboardShortcuts.vue →
 * this test fails, because the flushed pending Settings never carries
 * the new binding.
 */
import { describe, it, expect, vi } from 'vitest';
import { mount, flushPromises } from '@vue/test-utils';
import KeyboardShortcuts from '@/components/settings/KeyboardShortcuts.vue';
import { createFakeHarnessClient } from '@/lib/harnessClient';
import { HarnessClientKey } from '@/lib/harnessClientContext';
import { _flushForTest as flushSettingsDebounce } from '@/lib/settings';
import type { Settings } from '@/lib/types';

function baseSettings(): Settings {
  return {
    schemaVersion: 1,
    lastRoute: '/settings',
    theme: 'system',
    accent: 'default',
    windowSize: { width: 1280, height: 800 },
    memoryEnabled: false,
    keyboardShortcuts: {},
  } as Settings;
}

function mountShortcuts() {
  const client = createFakeHarnessClient();
  vi.spyOn(client.settings, 'get').mockResolvedValue(baseSettings());
  const setSpy = vi.spyOn(client.settings, 'set').mockResolvedValue(undefined);

  const wrapper = mount(KeyboardShortcuts, {
    global: {
      provide: { [HarnessClientKey as symbol]: client },
    },
  });
  return { wrapper, client, setSpy };
}

describe('KeyboardShortcuts — the full-settings round trip survives the WP04 deletion', () => {
  it('rebinding a shortcut persists through client.settings.set with the new binding', async () => {
    const { wrapper, client, setSpy } = mountShortcuts();
    await flushPromises();

    await wrapper.find('[data-testid="record-btn-chat.copy-last"]').trigger('click');
    await flushPromises();

    const captureEl = wrapper.find('[data-testid="capture-mode-chat.copy-last"] [tabindex="0"]');
    expect(captureEl.exists()).toBe(true);
    await captureEl.trigger('keydown', { key: 'k', ctrlKey: true, shiftKey: true });
    await flushPromises();

    // Force the debounced save to flush so the test does not have to wait
    // out the real timer (same pattern as CompactionSection.spec.ts).
    const pending = flushSettingsDebounce();
    expect(pending).not.toBeNull();
    if (pending) await client.settings.set(pending);

    expect(setSpy).toHaveBeenCalled();
    const last = setSpy.mock.calls[setSpy.mock.calls.length - 1][0] as Settings;
    expect(last.keyboardShortcuts?.['chat.copy-last']).toBe('Cmd+Shift+K');
  });

  it('reset clears an override and persists through the same round trip', async () => {
    const { wrapper, client, setSpy } = mountShortcuts();
    await flushPromises();

    // Bind first.
    await wrapper.find('[data-testid="record-btn-chat.copy-last"]').trigger('click');
    await flushPromises();
    await wrapper
      .find('[data-testid="capture-mode-chat.copy-last"] [tabindex="0"]')
      .trigger('keydown', { key: 'k', ctrlKey: true, shiftKey: true });
    await flushPromises();
    let pending = flushSettingsDebounce();
    if (pending) await client.settings.set(pending);
    setSpy.mockClear();

    // Then reset it.
    await wrapper.find('[data-testid="reset-btn-chat.copy-last"]').trigger('click');
    await flushPromises();
    pending = flushSettingsDebounce();
    expect(pending).not.toBeNull();
    if (pending) await client.settings.set(pending);

    expect(setSpy).toHaveBeenCalled();
    const last = setSpy.mock.calls[setSpy.mock.calls.length - 1][0] as Settings;
    expect(last.keyboardShortcuts?.['chat.copy-last']).toBeUndefined();
  });
});
