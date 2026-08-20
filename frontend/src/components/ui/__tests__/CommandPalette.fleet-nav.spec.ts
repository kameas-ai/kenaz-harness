/**
 * CommandPalette.fleet-nav.spec.ts — Sites and Marketplace in ⌘K.
 *
 * Before the capability gate was wired, `/sites` and `/marketplace` were
 * reachable by nobody: their LeftRail entries were permanently hidden, there is
 * no `router.push` to either anywhere in `frontend/src`, and neither appeared
 * among the palette's nav actions. Adding them here closes the second half of
 * that gap — but ungated palette entries would be a door around the
 * entitlement check, so they carry the same predicate as the rail entries.
 *
 * Drives the real ⌘K binding rather than calling `palette.open()` directly, so
 * the `visible` filter is exercised through the component that applies it.
 *
 * (docs/dead-code-audit-2026-08-16.md finding A4)
 */
import { describe, it, expect, afterEach, beforeEach } from 'vitest';
import { mount, flushPromises } from '@vue/test-utils';
import CommandPalette from '@/components/ui/CommandPalette.vue';
import { provideFakeClient } from '@/lib/harnessClientContext';
import { initFeatureFlags } from '@/lib/featureFlags';
import { _resetPlatformCache } from '@/lib/shortcuts/platform';
import type { AppInfo } from '@/lib/types';

function makeAppInfo(caps: Record<string, boolean>): AppInfo {
  return {
    build: 'test',
    commit: 'test',
    buildTime: '',
    goVersion: '',
    platform: 'test',
    windowSize: { width: 1280, height: 800 },
    capabilities: caps,
  };
}

function key(k: string, meta = false) {
  window.dispatchEvent(new KeyboardEvent('keydown', { key: k, metaKey: meta }));
}

async function openPalette() {
  // CommandPalette registers a theme-toggle action via useTheme, which needs
  // the RPC client to persist the choice.
  const w = mount(CommandPalette, {
    global: { plugins: [{ install(app) { provideFakeClient(app); } }] },
  });
  key('k', true);
  await flushPromises();
  return w;
}

describe('CommandPalette — fleet nav actions', () => {
  beforeEach(() => {
    // controls-and-readouts-that-tell-the-truth-01PMZ808 WP09: the ⌘K
    // listener now resolves through the registry + matchesEvent
    // (platform-aware) instead of an OS-agnostic metaKey check; this
    // spec's openPalette() simulates a Mac keypress via metaKey, so pin
    // the detected platform to 'mac' (same convention as
    // Shell.cmdk.spec.ts / Shell.shortcutRebind.spec.ts).
    localStorage.setItem('kenaz_shortcut_platform', 'mac');
    _resetPlatformCache();
  });
  afterEach(() => {
    key('Escape');
    initFeatureFlags(null);
    localStorage.removeItem('kenaz_shortcut_platform');
    _resetPlatformCache();
  });

  it('hides both from a signed-out user', async () => {
    initFeatureFlags(null);
    const w = await openPalette();
    expect(w.text()).not.toContain('Go to Sites');
    expect(w.text()).not.toContain('Go to Marketplace');
  });

  it('offers Marketplace but not Sites without the sites_hosting capability', async () => {
    initFeatureFlags(makeAppInfo({ context_sync: true }));
    const w = await openPalette();
    expect(w.text()).toContain('Go to Marketplace');
    expect(w.text()).not.toContain('Go to Sites');
  });

  it('offers both when signed in with sites_hosting', async () => {
    initFeatureFlags(makeAppInfo({ sites_hosting: true }));
    const w = await openPalette();
    expect(w.text()).toContain('Go to Sites');
    expect(w.text()).toContain('Go to Marketplace');
  });

  it('leaves the ungated actions alone', async () => {
    // Regression guard for the `visible` filter itself: adding the predicate
    // must not accidentally hide the ungated majority.
    initFeatureFlags(null);
    const w = await openPalette();
    expect(w.text()).toContain('Go to Sessions');
    expect(w.text()).toContain('Go to Security policy');
  });
});
