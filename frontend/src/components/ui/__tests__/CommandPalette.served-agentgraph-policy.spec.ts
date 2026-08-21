/**
 * CommandPalette.served-agentgraph-policy.spec.ts — AC-709,
 * served-mode-is-a-real-mode-01PMZ707 WP03 (spec.md §5.3, D-701).
 *
 * nav.agentgraph and nav.policy would route into GraphsView.vue's and
 * PolicyView.vue's own boundary panels in a served build (Graph_ and
 * CedarPolicy_/Policy_ RPCs have no serve dispatch case), so both palette
 * entries carry the same `!isServedMode()` predicate as nav.sites and
 * nav.marketplace (CommandPalette.fleet-nav.spec.ts). Unlike the fleet
 * entries, neither is also gated on sign-in or a capability — hiding them
 * in served mode must not require either.
 *
 * Drives the real ⌘K binding rather than calling `palette.open()`
 * directly, so the `visible` filter is exercised through the component
 * that applies it (CommandPalette.vue:41).
 */
import { describe, it, expect, afterEach, beforeEach, vi } from 'vitest';
import { mount, flushPromises } from '@vue/test-utils';
import { readonly, ref } from 'vue';
import CommandPalette from '@/components/ui/CommandPalette.vue';
import { provideFakeClient } from '@/lib/harnessClientContext';
import { _resetPlatformCache } from '@/lib/shortcuts/platform';

let servedModeFlag = false;
vi.mock('@/lib/useServedMode', () => {
  const served = ref(false);
  return {
    isServedMode: () => servedModeFlag,
    useServedMode: () => readonly(served),
  };
});

function key(k: string, meta = false) {
  window.dispatchEvent(new KeyboardEvent('keydown', { key: k, metaKey: meta }));
}

async function openPalette() {
  const w = mount(CommandPalette, {
    global: { plugins: [{ install(app) { provideFakeClient(app); } }] },
  });
  key('k', true);
  await flushPromises();
  return w;
}

describe('CommandPalette — Agent graphs / Policy in served mode', () => {
  beforeEach(() => {
    localStorage.setItem('kenaz_shortcut_platform', 'mac');
    _resetPlatformCache();
  });
  afterEach(() => {
    key('Escape');
    servedModeFlag = false;
    localStorage.removeItem('kenaz_shortcut_platform');
    _resetPlatformCache();
  });

  it('hides both in served mode, no sign-in or capability required', async () => {
    servedModeFlag = true;
    const w = await openPalette();
    expect(w.text()).not.toContain('Go to Agent graphs');
    expect(w.text()).not.toContain('Go to Security policy');
  });

  it('offers both in desktop mode', async () => {
    servedModeFlag = false;
    const w = await openPalette();
    expect(w.text()).toContain('Go to Agent graphs');
    expect(w.text()).toContain('Go to Security policy');
  });

  it('leaves the ungated majority alone in served mode', async () => {
    // served-mode-is-a-real-mode-01PMZ707 WP05 also gated nav.audit (all
    // eleven Audit_* RPCs are unrouted — see AuditView.served.test.ts), so
    // it is deliberately NOT in this list; nav.permissions stays ungated
    // per D-710 (its pending-prompt surface genuinely works in served mode).
    servedModeFlag = true;
    const w = await openPalette();
    expect(w.text()).toContain('Go to Sessions');
    expect(w.text()).toContain('Go to Permissions');
    expect(w.text()).toContain('Go to Tools');
  });
});
