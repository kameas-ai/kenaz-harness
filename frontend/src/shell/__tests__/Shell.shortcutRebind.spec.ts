/**
 * Shell.shortcutRebind.spec.ts —
 * controls-and-readouts-that-tell-the-truth-01PMZ808 UNIT-5 / WP09.
 *
 * AC-021: with keyboardShortcuts = { 'nav.focus-search': 'Ctrl+Shift+F' },
 * a plain Cmd/Ctrl+F does NOT open search and Ctrl+Shift+F does.
 * AC-022: the same for the command palette (Cmd/Ctrl+K), via
 * useCommandPalette.ts's module-level listener.
 *
 * Before this WP, Shell.vue read keyboardShortcuts into shortcutOverrides
 * purely to feed CheatSheetModal's display — CheatSheetModal.vue rendered
 * an override nothing enforced. registry.ts's own header claimed
 * "Components and composables READ from this file; they never hard-code
 * binding strings", which was false for both of these listeners.
 */
import { afterEach, beforeEach, describe, expect, it } from 'vitest';
import { mount, flushPromises } from '@vue/test-utils';
import { h } from 'vue';
import Shell from '@/shell/Shell.vue';
import CommandPalette from '@/components/ui/CommandPalette.vue';
import { HarnessClientKey } from '@/lib/harnessClientContext';
import { createFakeHarnessClient } from '@/lib/harnessClient';
import { setConnectionState } from '@/lib/useConnectionState';
import { useSearchPalette } from '@/lib/useSearchPalette';
import { useCommandPalette } from '@/lib/useCommandPalette';
import { _resetPlatformCache } from '@/lib/shortcuts/platform';
import type { Settings } from '@/lib/types';

const ShellPlusPalette = {
  components: { Shell, CommandPalette },
  render() {
    return h('div', [h(Shell), h(CommandPalette)]);
  },
};

function mountShellAndPalette(keyboardShortcuts: Record<string, string>) {
  const settings: Settings = {
    schemaVersion: 1,
    lastRoute: '/sessions',
    theme: 'dark',
    accent: 'default',
    windowSize: { width: 1280, height: 800 },
    keyboardShortcuts,
  };
  const client = createFakeHarnessClient({
    settings: { get: async () => settings } as any,
  });
  return mount(ShellPlusPalette, {
    global: {
      provide: { [HarnessClientKey as symbol]: client },
      stubs: {
        Titlebar: true,
        Toolbar: true,
        LeftRail: true,
        LegendBar: true,
        ErrorBoundary: true,
        ConnectionLostBanner: true,
        LockdownBanner: true,
        SessionExpiredBanner: true,
        BootHealthBanner: true,
        SearchModal: true,
        CheatSheetModal: true,
      },
    },
    attachTo: document.body,
  });
}

describe('AC-021 — Cmd/Ctrl+F rebinds to a custom search shortcut', () => {
  beforeEach(() => {
    setConnectionState('ready');
    useSearchPalette().close();
    // Pin the platform to 'mac' so 'Cmd' and 'Ctrl' are distinct
    // modifiers in matchesEvent, matching this AC's own wording
    // ("a plain Cmd+F does not open search"). platform.ts memoizes
    // detectPlatform()'s result module-wide; _resetPlatformCache clears
    // it so this override — and this test's own teardown — take effect
    // rather than leaking into/from other spec files in the same run.
    localStorage.setItem('kenaz_shortcut_platform', 'mac');
    _resetPlatformCache();
  });
  afterEach(() => {
    useSearchPalette().close();
    localStorage.removeItem('kenaz_shortcut_platform');
    _resetPlatformCache();
  });

  it('a plain Cmd+F does not open search, and the Ctrl+Shift+F override does', async () => {
    const wrapper = mountShellAndPalette({ 'nav.focus-search': 'Ctrl+Shift+F' });
    await flushPromises();

    // SearchModal is stubbed; assert via the Shell instance's own state
    // instead of a rendered stub marker.
    const shellVm = wrapper.findComponent(Shell).vm as unknown as { searchOpen: boolean };

    window.dispatchEvent(
      new KeyboardEvent('keydown', { key: 'f', metaKey: true, bubbles: true }),
    );
    await flushPromises();
    expect(shellVm.searchOpen).toBe(false);

    window.dispatchEvent(
      new KeyboardEvent('keydown', { key: 'f', ctrlKey: true, shiftKey: true, bubbles: true }),
    );
    await flushPromises();
    expect(shellVm.searchOpen).toBe(true);

    wrapper.unmount();
  });
});

describe('AC-022 — Cmd/Ctrl+K rebinds the command palette', () => {
  beforeEach(() => {
    setConnectionState('ready');
    const cmd = useCommandPalette();
    if (cmd.isOpen.value) cmd.toggle();
    localStorage.setItem('kenaz_shortcut_platform', 'mac');
    _resetPlatformCache();
  });
  afterEach(() => {
    const cmd = useCommandPalette();
    if (cmd.isOpen.value) cmd.toggle();
    localStorage.removeItem('kenaz_shortcut_platform');
    _resetPlatformCache();
  });

  it('a plain Cmd+K does not open the palette once rebound, and the Ctrl+Shift+P override does', async () => {
    const wrapper = mountShellAndPalette({ 'nav.command-palette': 'Ctrl+Shift+P' });
    await flushPromises();

    window.dispatchEvent(
      new KeyboardEvent('keydown', { key: 'k', metaKey: true, bubbles: true }),
    );
    await flushPromises();
    expect(document.querySelector('[aria-label="Command palette"]')).toBeNull();

    window.dispatchEvent(
      new KeyboardEvent('keydown', { key: 'p', ctrlKey: true, shiftKey: true, bubbles: true }),
    );
    await flushPromises();
    expect(document.querySelector('[aria-label="Command palette"]')).not.toBeNull();

    wrapper.unmount();
  });
});
