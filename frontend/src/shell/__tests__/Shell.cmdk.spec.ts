/**
 * Shell.cmdk.spec.ts — ⌘K has exactly one owner (engineer-truth-pass-01PMTP01
 * WP01, finding B1).
 *
 * Before this fix, two independent `window` keydown listeners both matched
 * ⌘K/Ctrl+K: Shell.vue's own `onGlobalKeydown` (toggling the search palette)
 * and useCommandPalette.ts's module-level `onKey` (toggling the command
 * palette, installed whenever CommandPalette.vue mounts — see App.vue, where
 * it is a sibling of <Shell />, not a child). Both fired on one keypress, and
 * both overlays painted at the same z-50 with the command palette later in
 * the DOM, so it visually won.
 *
 * This test mounts Shell (which owns the search palette) and CommandPalette
 * (which owns ⌘K) side by side, the same composition App.vue uses, and
 * dispatches a single ⌘K keydown on `window` — the real event source, not a
 * direct composable call — so it exercises whichever listener(s) are actually
 * registered.
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

function pressCmdK() {
  window.dispatchEvent(
    new KeyboardEvent('keydown', { key: 'k', metaKey: true, bubbles: true }),
  );
}

// Composes Shell and CommandPalette as siblings under one root — the same
// relationship App.vue uses in production (Shell and CommandPalette are
// both direct children of App, not nested). A single mount/unmount avoids
// vue-test-utils double-`attachTo` teardown ordering issues.
const ShellPlusPalette = {
  components: { Shell, CommandPalette },
  render() {
    return h('div', [h(Shell), h(CommandPalette)]);
  },
};

describe('⌘K owner (B1)', () => {
  beforeEach(() => {
    setConnectionState('ready');
    useSearchPalette().close();
    const cmd = useCommandPalette();
    if (cmd.isOpen.value) cmd.toggle();
    // controls-and-readouts-that-tell-the-truth-01PMZ808 WP09: onKey now
    // resolves through the registry + matchesEvent (platform-aware)
    // instead of an OS-agnostic `e.metaKey || e.ctrlKey` check, so this
    // test — which simulates a Mac ⌘K via metaKey — must pin the
    // detected platform to 'mac' the same way Shell.shortcutRebind.spec.ts
    // does, or it exercises the Windows/Linux Ctrl+K mapping instead.
    localStorage.setItem('kenaz_shortcut_platform', 'mac');
    _resetPlatformCache();
  });

  afterEach(() => {
    useSearchPalette().close();
    const cmd = useCommandPalette();
    if (cmd.isOpen.value) cmd.toggle();
    localStorage.removeItem('kenaz_shortcut_platform');
    _resetPlatformCache();
  });

  function mountShellAndPalette() {
    const client = createFakeHarnessClient();
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

  it('opens exactly one overlay on a single ⌘K keydown', async () => {
    const wrapper = mountShellAndPalette();
    await flushPromises();

    // Sanity: neither is open before the keypress.
    expect(document.querySelector('[data-testid="search-palette"]')).toBeNull();
    expect(document.querySelector('[aria-label="Command palette"]')).toBeNull();

    pressCmdK();
    await flushPromises();

    const searchRoot = document.querySelector('[data-testid="search-palette"]');
    const cmdRoot = document.querySelector('[aria-label="Command palette"]');
    const openRoots = [searchRoot, cmdRoot].filter((n) => n !== null);

    expect(openRoots).toHaveLength(1);
    expect(cmdRoot).not.toBeNull();
    expect(searchRoot).toBeNull();

    wrapper.unmount();
  });
});
