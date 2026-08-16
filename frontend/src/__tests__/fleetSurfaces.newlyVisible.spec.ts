/**
 * fleetSurfaces.newlyVisible.spec.ts — the Publish-to-team buttons that nobody
 * had ever seen.
 *
 * Wiring the fleet capability gate makes seven previously-unreachable surfaces
 * appear. Most already had render coverage elsewhere:
 *
 *   - Sites nav / SitesView          → shell/__tests__/LeftRail.sites-nav.test.ts
 *                                      + views/sites/__tests__/SitesView.test.ts
 *   - Marketplace nav / view         → LeftRail.marketplace-nav.test.ts
 *                                      + views/marketplace/__tests__/MarketplaceView.spec.ts
 *   - SlashCommands publish          → views/settings/__tests__/SlashCommandsView.spec.ts
 *   - Cedar team-policy publish      → views/policy/__tests__/CedarEditor.publish.spec.ts
 *   - Sync panel                     → views/settings/__tests__/SyncPanel.spec.ts
 *
 * The two with no gate coverage at all were `WorkflowsView.vue`'s and
 * `BundlesView.vue`'s "Publish to team" buttons — the surfaces most likely to
 * appear broken, because both open `PublishDialog` and neither had ever been
 * rendered with `signedIn === true` in a test. This file covers both, using the
 * real featureFlags module, and asserts the dialog they open actually mounts.
 *
 * (docs/dead-code-audit-2026-08-16.md finding A4)
 */
import { describe, it, expect, beforeEach, afterEach, vi } from 'vitest';
import { mount, flushPromises } from '@vue/test-utils';
import BundlesView from '@/views/bundles/BundlesView.vue';
import WorkflowsView from '@/views/workflows/WorkflowsView.vue';
import { createFakeHarnessClient } from '@/lib/harnessClient';
import { HarnessClientKey } from '@/lib/harnessClientContext';
import { createFakeWorkflowsClient } from '@/lib/workflowsClient';
import { initFeatureFlags } from '@/lib/featureFlags';
import type { AppInfo, Bundle } from '@/lib/types';

vi.mock('@/shell/CanvasHead.vue', () => ({
  default: { template: '<div data-testid="canvas-head-stub" />' },
}));

function signedInAppInfo(): AppInfo {
  return {
    build: 'test',
    commit: 'test',
    buildTime: '',
    goVersion: '',
    platform: 'test',
    windowSize: { width: 1280, height: 800 },
    // A Pro user with no team-specific capability: `signedIn` is what both
    // buttons gate on, so the map only has to be non-empty.
    capabilities: { context_sync: true },
    tier: 'pro',
  };
}

const bundle: Bundle = {
  id: 'bundle-1',
  name: 'Example bundle',
  version: '1.0.0',
  tier: 'local',
  artifactCount: 3,
};

function bundlesClient() {
  return createFakeHarnessClient({
    bundle: {
      list: async () => [bundle],
      get: async () => bundle,
      install: async () => bundle,
      remove: async () => undefined,
    },
  });
}

describe('BundlesView — Publish to team', () => {
  beforeEach(() => {
    initFeatureFlags(null);
    // Teleported dialog panels persist in document.body between tests; a stale
    // one would make the assertions below pass for the wrong reason.
    document.body.innerHTML = '';
  });
  afterEach(() => initFeatureFlags(null));

  it('is absent when signed out', async () => {
    const w = mount(BundlesView, {
      global: { provide: { [HarnessClientKey as symbol]: bundlesClient() } },
    });
    await flushPromises();
    expect(w.find('[data-testid="bundle-publish-bundle-1"]').exists()).toBe(false);
  });

  it('renders and opens a working publish dialog when signed in', async () => {
    initFeatureFlags(signedInAppInfo());
    const w = mount(BundlesView, {
      global: { provide: { [HarnessClientKey as symbol]: bundlesClient() } },
    });
    await flushPromises();

    const btn = w.find('[data-testid="bundle-publish-bundle-1"]');
    expect(btn.exists()).toBe(true);

    // The risk this file exists to retire: a gate that reveals a surface which
    // then throws. Clicking must mount the dialog, not blow up.
    await btn.trigger('click');
    await flushPromises();
    // BaseDialog teleports its panel to document.body, so query there.
    expect(document.querySelector('[data-testid="publish-dialog"]')).not.toBeNull();
    expect(document.querySelector('[data-testid="publish-submit-btn"]')).not.toBeNull();
  });
});

describe('WorkflowsView — Publish to team', () => {
  beforeEach(() => {
    initFeatureFlags(null);
    // Teleported dialog panels persist in document.body between tests; a stale
    // one would make the assertions below pass for the wrong reason.
    document.body.innerHTML = '';
  });
  afterEach(() => initFeatureFlags(null));

  // The Publish button lives in the detail pane, which only renders once a
  // workflow is selected — the view auto-selects the first row.
  const summary = {
    id: 'wf-1',
    name: 'Example workflow',
    description: '',
    version: 1,
    stepCount: 1,
    source: 'builtin',
  };
  const detail = {
    ...summary,
    inputs: [],
    steps: [{ name: 'plan', kind: 'model_turn' }],
  };

  function mountView() {
    return mount(WorkflowsView, {
      props: {
        client: createFakeWorkflowsClient({
          list: () => Promise.resolve([summary]),
          get: () => Promise.resolve(detail),
        }),
      },
      global: {
        provide: { [HarnessClientKey as symbol]: createFakeHarnessClient() },
      },
    });
  }

  it('is absent when signed out', async () => {
    const w = mountView();
    await flushPromises();
    expect(w.find('[data-testid="workflows-publish-btn"]').exists()).toBe(false);
  });

  it('renders and opens a working publish dialog when signed in', async () => {
    initFeatureFlags(signedInAppInfo());
    const w = mountView();
    await flushPromises();

    const btn = w.find('[data-testid="workflows-publish-btn"]');
    expect(btn.exists()).toBe(true);

    await btn.trigger('click');
    await flushPromises();
    // BaseDialog teleports its panel to document.body, so query there.
    expect(document.querySelector('[data-testid="publish-dialog"]')).not.toBeNull();
    expect(document.querySelector('[data-testid="publish-submit-btn"]')).not.toBeNull();
  });
});
