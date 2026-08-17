/**
 * featureFlags.servedFence.spec.ts — the served-mode fence on the capability
 * gates (adversarial review of fix/featureflags, 2026-08-16).
 *
 * WHY THIS FILE EXISTS. Wiring `bootFeatureFlags` fixed a gate that had been
 * permanently false; the inverse risk is the one that needed guarding, and it
 * was only half-guarded. `main-served.ts` boots the capability snapshot too,
 * and `AppInfo` IS in the served allowlist — it answers with the desktop
 * process's real capability map. So a browser client of a signed-in harness
 * read `signedIn === true` and every gate opened.
 *
 * Seven of the eight newly-visible surfaces turn out to be covered already,
 * but by three DIFFERENT mechanisms, which is why the eighth was missed:
 *
 *   - Sites / Marketplace  — routes absent from main-served.ts, plus an
 *                            explicit `!served` guard on the rail entries.
 *   - WorkflowsView        — renders NotAvailableInServedMode over its whole
 *                            template.
 *   - SettingsView         — same, and it owns both SyncPanel and
 *                            SlashCommandsView.
 *   - CedarEditor          — has no production mount at all.
 *
 *   - BundlesView          — none of the above. `/bundles` IS a served route,
 *                            the view has no boundary panel, and its "Publish
 *                            to team" button gates on `signedIn` alone.
 *
 * `Catalog_Publish` is not in `core/serve/methods.go`, whose allowlist is 33
 * methods and carries no fleet method of any kind, so that button rendered a
 * control whose only outcome was `unknown RPC method`.
 *
 * The fence therefore lives in the helpers, not at the call sites — the call
 * sites are the thing that keeps being forgotten. These tests assert the
 * helper contract directly, so a future fleet surface inherits it without
 * having to know this history.
 */
import { describe, it, expect, vi, beforeEach } from 'vitest';
import { ref } from 'vue';
import type { AppInfo } from '@/lib/types';

// A real ref, not a plain object: `signedIn` is a Vue computed, so a
// non-reactive stub would be read once and cached and the toggle test would
// assert against a stale value rather than against the fence.
const served = vi.hoisted(async () => {
  const { ref: r } = await import('vue');
  return r(false);
});

vi.mock('@/lib/useServedMode', async (importOriginal) => {
  const actual = await importOriginal<typeof import('@/lib/useServedMode')>();
  const flag = await served;
  return { ...actual, isServedMode: () => flag.value };
});

/** Resolved once; `served` is a promise because vi.hoisted runs pre-import. */
let servedFlag: ReturnType<typeof ref<boolean>>;

function makeAppInfo(capabilities: Record<string, boolean>): AppInfo {
  return {
    build: '1.2.3',
    commit: 'deadbeef',
    buildTime: '',
    goVersion: 'go1.25',
    platform: 'test/arm64',
    windowSize: { width: 1280, height: 800 },
    capabilities,
    tier: 'team',
  };
}

describe('capability gates — served-mode fence', () => {
  beforeEach(async () => {
    servedFlag = await served;
    servedFlag.value = false;
  });

  it('opens the gates on desktop with a populated snapshot', async () => {
    const flags = await import('@/lib/featureFlags');
    flags.initFeatureFlags(
      makeAppInfo({ sites_hosting: true, context_sync: true }),
    );

    expect(flags.signedIn.value).toBe(true);
    expect(flags.capability('sites_hosting')).toBe(true);
    expect(flags.capability('context_sync')).toBe(true);
  });

  it('closes every gate in served mode despite the same snapshot', async () => {
    const flags = await import('@/lib/featureFlags');
    // The snapshot is real and signed-in — AppInfo is served, so this is
    // exactly what a browser client receives from a signed-in harness.
    flags.initFeatureFlags(
      makeAppInfo({ sites_hosting: true, context_sync: true }),
    );
    servedFlag.value = true;

    // signedIn is what WorkflowsView, BundlesView and MarketplaceView gate on.
    expect(flags.signedIn.value).toBe(false);
    // capability() is what SyncPanel, SlashCommandsView, CedarEditor and the
    // Sites rail entry gate on.
    expect(flags.capability('sites_hosting')).toBe(false);
    expect(flags.capability('context_sync')).toBe(false);
  });

  it('reopens the gates when served mode is false again', async () => {
    const flags = await import('@/lib/featureFlags');
    flags.initFeatureFlags(makeAppInfo({ sites_hosting: true }));

    servedFlag.value = true;
    expect(flags.signedIn.value).toBe(false);

    servedFlag.value = false;
    expect(flags.signedIn.value).toBe(true);
    expect(flags.capability('sites_hosting')).toBe(true);
  });
});
