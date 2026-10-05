/**
 * CapabilitySurface — the fleet-catalog browse folded in from the retired
 * MarketplaceView (install-framework-01DOGF0B Phase 4 WP08).
 *
 * These pins MOVED here from views/marketplace/__tests__/MarketplaceView.spec.ts
 * (deleted with the view); each keeps its original number in the title so
 * the lineage is greppable:
 *
 *   M1  signed out → the provider-less kinds render reason rows (P-5), no
 *       Catalog_List call — was "not-signed-in gate"
 *   M2  bundle / agent_pack catalog items render; installed/ residue is
 *       "Downloaded — not active", never "Installed" (WP02)
 *   M4  P-2: bundle / agent_pack Install is DISABLED with a visible reason
 *       naming the working alternative, aria-describedby'd, never calls
 *       Catalog_Install. Workflow catalog items are no longer catalog-only:
 *       they install through the workflow provider (pinned in
 *       CapabilitySurface.test.ts WP05), so they get no disabled row here.
 *   M4a the install guard refuses a force-enabled button (review F4)
 *   M4d reason element ids are unique per id+version (review F2)
 *   M5  "Remove download" on residue calls Catalog_Uninstall and refreshes
 *   M8–M12 Withdraw (fleet-enforcement-truth-01PMZ505 WP11): offered on every
 *       catalog listing (catalog-only rows AND skill/workflow provider rows
 *       that are catalog listings), confirm copy distinct from Uninstall
 *       (AC-021), confirm → Catalog_Unpublish + refresh, cancel, and a 403
 *       passes through as a forbidden error, never a tier message (AC-020(b)).
 *
 * MarketplaceView pins 4b/4c/6/7 (skill install/uninstall routing through
 * slashcmd) did not move: a catalog skill is a provider row now, and its
 * Install / badge / Remove are pinned through Capability_* in
 * CapabilitySurface.test.ts (WP05) and SkillDetail.test.ts. M-skill below
 * pins that a catalog skill never also renders as a catalog-only row.
 *
 * In-memory fake client, DELIBERATELY (WP-PI AC-PI-2): these pin which RPCs
 * the UI calls; the Go refusal is TestCatalogInstall_RefusesUnconsumedKinds.
 */
import { describe, it, expect, vi, beforeEach } from 'vitest';
import { mount, flushPromises } from '@vue/test-utils';
import { defineComponent, ref } from 'vue';
import { createMemoryHistory, createRouter } from 'vue-router';
import CapabilitySurface from '@/views/capabilities/CapabilitySurface.vue';
import { createFakeHarnessClient } from '@/lib/harnessClient';
import { HarnessClientKey } from '@/lib/harnessClientContext';
import type { CapabilityItem, CapabilityListing, CatalogItemView } from '@/lib/types';

const _signedIn = ref(true);
vi.mock('@/lib/featureFlags', async (importOriginal) => ({
  ...(await importOriginal<typeof import('@/lib/featureFlags')>()),
  get signedIn() {
    return _signedIn;
  },
}));

const BUNDLE: CatalogItemView = {
  id: 'cat-b1',
  kind: 'bundle',
  slug: 'my-bundle',
  version: '2.1.0',
  description: 'A test bundle',
  visibility: 'org_public',
  published_at: '2026-09-01T00:00:00Z',
  installed: false,
};
const PACK: CatalogItemView = {
  id: 'cat-p1',
  kind: 'agent_pack',
  slug: 'my-pack',
  version: '1.0.0',
  description: 'A test agent pack',
  visibility: 'team',
  installed: false,
};
const WORKFLOW: CatalogItemView = {
  id: 'cat-w1',
  kind: 'workflow',
  slug: 'my-workflow',
  version: '1.0.0',
  description: 'A test workflow',
  visibility: 'team',
  installed: false,
};
const SKILL: CatalogItemView = {
  id: 'cat-standup',
  kind: 'skill',
  slug: 'standup',
  version: '1.0.0',
  description: 'Daily standup skill',
  visibility: 'private',
  installed: false,
};

function skillItem(id: string, overrides: Partial<CapabilityItem> = {}): CapabilityItem {
  return {
    kind: 'skill',
    id,
    version: '1.0.0',
    name: 'standup',
    description: 'Daily standup skill',
    source: 'team_catalog',
    state: { installed: false, consumer: 'slash registry' },
    requirements: [],
    ...overrides,
  };
}

interface Opts {
  catalog?: CatalogItemView[] | (() => Promise<CatalogItemView[]>);
  listing?: CapabilityListing;
  uninstall?: ReturnType<typeof vi.fn>;
  unpublish?: ReturnType<typeof vi.fn>;
}

function setup(o: Opts = {}) {
  const base = createFakeHarnessClient();
  const catalogList =
    typeof o.catalog === 'function'
      ? vi.fn(o.catalog)
      : vi.fn(async () => (o.catalog as CatalogItemView[] | undefined) ?? []);
  const catalogInstall = vi.fn(async () => {});
  const catalogUninstall = o.uninstall ?? vi.fn(async () => {});
  const unpublish = o.unpublish ?? vi.fn(async () => {});
  const capList = vi.fn(async () => o.listing ?? { items: [] });
  const client = createFakeHarnessClient({
    capabilities: { ...base.capabilities, list: capList },
    catalog: {
      ...base.catalog,
      list: catalogList,
      install: catalogInstall,
      uninstall: catalogUninstall,
      unpublish,
    },
  });
  return { client, catalogList, catalogInstall, catalogUninstall, unpublish, capList };
}

async function mountSurface(s: ReturnType<typeof setup>, at = '/') {
  const r = createRouter({
    history: createMemoryHistory(),
    routes: [
      { path: '/', component: defineComponent({ render: () => null }) },
      { path: '/bundles', component: defineComponent({ render: () => null }) },
      { path: '/knowledge/learned', component: defineComponent({ render: () => null }) },
    ],
  });
  await r.push(at);
  await r.isReady();
  const w = mount(CapabilitySurface, {
    global: { plugins: [r], provide: { [HarnessClientKey as symbol]: s.client } },
    attachTo: document.body,
  });
  await flushPromises();
  return { w, r };
}

describe('CapabilitySurface — fleet catalog browse (WP08, moved from MarketplaceView)', () => {
  beforeEach(() => {
    _signedIn.value = true;
  });

  it('M1. signed out: bundle and agent_pack render reason rows, and Catalog_List is not called', async () => {
    _signedIn.value = false;
    const s = setup({ catalog: [BUNDLE] });
    const { w } = await mountSurface(s);
    expect(s.catalogList).not.toHaveBeenCalled();
    expect(w.get('[data-testid=capability-unavailable-bundle-all]').text()).toContain('Sign in');
    expect(w.get('[data-testid=capability-unavailable-agent_pack-all]').text()).toContain('Sign in');
    expect(w.find('[data-testid^=capability-catalog-row-]').exists()).toBe(false);
    w.unmount();
  });

  it('M1b. a Catalog_List failure renders reason rows with the error, never a hidden kind', async () => {
    const s = setup({
      catalog: async () => {
        throw new Error('fleet: disabled');
      },
    });
    const { w } = await mountSurface(s);
    expect(w.get('[data-testid=capability-unavailable-bundle-all]').text()).toContain('fleet: disabled');
    expect(w.get('[data-testid=capability-unavailable-agent_pack-all]').text()).toContain('fleet: disabled');
    w.unmount();
  });

  it('M2. renders bundle / agent_pack catalog items; installed/ residue is "Downloaded — not active", never Installed', async () => {
    const residue: CatalogItemView = { ...BUNDLE, id: 'cat-b2', slug: 'old-bundle', installed: true };
    const s = setup({ catalog: [BUNDLE, PACK, residue] });
    const { w } = await mountSurface(s);
    expect(w.find('[data-testid=capability-catalog-row-bundle-my-bundle]').exists()).toBe(true);
    expect(w.find('[data-testid=capability-catalog-row-agent_pack-my-pack]').exists()).toBe(true);
    const row = w.get('[data-testid=capability-catalog-row-bundle-old-bundle]');
    expect(row.text()).toContain('Downloaded — not active');
    expect(row.text()).not.toContain('Installed');
    w.unmount();
  });

  it('M2b. ?kind=bundle deep-links to the Bundles chip', async () => {
    const s = setup({ catalog: [BUNDLE, PACK] });
    const { w } = await mountSurface(s, '/?kind=bundle');
    expect(w.get('[data-testid=capability-kind-chip-bundle]').attributes('aria-pressed')).toBe('true');
    expect(w.find('[data-testid=capability-catalog-row-bundle-my-bundle]').exists()).toBe(true);
    expect(w.find('[data-testid=capability-catalog-row-agent_pack-my-pack]').exists()).toBe(false);
    w.unmount();
  });

  it('M2c. search matches catalog rows by slug and description', async () => {
    const s = setup({ catalog: [BUNDLE, PACK] });
    const { w } = await mountSurface(s);
    await w.get('[data-testid=capability-search]').setValue('agent pack');
    expect(w.find('[data-testid=capability-catalog-row-agent_pack-my-pack]').exists()).toBe(true);
    expect(w.find('[data-testid=capability-catalog-row-bundle-my-bundle]').exists()).toBe(false);
    w.unmount();
  });

  it('M4. P-2: bundle / agent_pack Install is disabled with a visible reason naming the alternative and never calls Catalog_Install', async () => {
    const s = setup({ catalog: [BUNDLE, PACK, WORKFLOW] });
    const { w } = await mountSurface(s);
    const kinds: Array<[string, string, RegExp]> = [
      ['agent_pack', 'my-pack', /agents folder/],
      ['bundle', 'my-bundle', /Settings › Integrations › Bundles/],
    ];
    for (const [kind, slug, alternative] of kinds) {
      const btn = w.find(`[data-testid=capability-catalog-install-${kind}-${slug}]`);
      // Disabled, not hidden.
      expect(btn.exists(), `${kind} Install button must be shown`).toBe(true);
      expect(btn.attributes('disabled'), `${kind} Install must be disabled`).toBeDefined();
      const reason = w.find(`[data-testid=capability-catalog-unsupported-${kind}-${slug}]`);
      expect(reason.exists(), `${kind} must show its reason as visible text`).toBe(true);
      expect(reason.text()).toContain("isn't supported yet");
      expect(reason.text()).toMatch(alternative);
      expect(reason.attributes('id')).toBeTruthy();
      expect(btn.attributes('aria-describedby')).toBe(reason.attributes('id'));
      await btn.trigger('click');
    }
    await flushPromises();
    expect(s.catalogInstall).not.toHaveBeenCalled();
    // A workflow catalog item installs through its provider; it is never a
    // disabled catalog-only row.
    expect(w.find('[data-testid^=capability-catalog-row-workflow-]').exists()).toBe(false);
    w.unmount();
  });

  it('M4-bundle. the bundle detail links to the working alternative (Settings › Integrations › Bundles)', async () => {
    const s = setup({ catalog: [BUNDLE] });
    const { w, r } = await mountSurface(s);
    await w.get('[data-testid=capability-catalog-row-bundle-my-bundle] button').trigger('click');
    expect(w.get('[data-testid=catalog-detail-unsupported]').text()).toMatch(/Settings › Integrations › Bundles/);
    await w.get('[data-testid=catalog-detail-alternative-bundle]').trigger('click');
    await flushPromises();
    expect(r.currentRoute.value.path).toBe('/bundles');
    w.unmount();
  });

  it('M4a. the install guard refuses a force-enabled button: the reason shows, Catalog_Install is never called (F4)', async () => {
    const s = setup({ catalog: [BUNDLE] });
    const { w } = await mountSurface(s);
    const btn = w.get('[data-testid=capability-catalog-install-bundle-my-bundle]');
    (btn.element as HTMLButtonElement).disabled = false;
    btn.element.removeAttribute('disabled');
    await btn.trigger('click');
    await flushPromises();
    expect(s.catalogInstall).not.toHaveBeenCalled();
    const err = w.get('[data-testid=capability-catalog-error-bundle-my-bundle]');
    expect(err.text()).toContain("isn't supported yet");
    expect(err.text()).toContain('Settings › Integrations › Bundles');
    w.unmount();
  });

  it('M4d. reason element ids are unique per id+version even when slugs collide (F2)', async () => {
    const a: CatalogItemView = { ...BUNDLE, id: 'cat-x', version: '1.0.0' };
    const b: CatalogItemView = { ...BUNDLE, id: 'cat-y', version: '2.0.0' };
    const s = setup({ catalog: [a, b] });
    const { w } = await mountSurface(s);
    const ids = w
      .findAll('[data-testid=capability-catalog-unsupported-bundle-my-bundle]')
      .map((x) => x.attributes('id'));
    expect(ids).toHaveLength(2);
    expect(new Set(ids).size).toBe(2);
    w.unmount();
  });

  it('M5. "Remove download" on installed/ residue calls Catalog_Uninstall and refreshes the catalog', async () => {
    const residue: CatalogItemView = { ...BUNDLE, installed: true };
    const lists = [[residue], [{ ...residue, installed: false }]];
    let call = 0;
    const s = setup({ catalog: async () => lists[Math.min(call++, 1)] });
    const { w } = await mountSurface(s);
    const btn = w.get('[data-testid=capability-catalog-remove-download-bundle-my-bundle]');
    expect(btn.text()).toBe('Remove download');
    await btn.trigger('click');
    await flushPromises();
    expect(s.catalogUninstall).toHaveBeenCalledWith('bundle', 'cat-b1', '2.1.0');
    expect(s.catalogList).toHaveBeenCalledTimes(2);
    expect(w.find('[data-testid=capability-catalog-remove-download-bundle-my-bundle]').exists()).toBe(false);
    w.unmount();
  });

  it('M5b. workflow residue says to install from its Workflows row, and only offers Remove download', async () => {
    const s = setup({ catalog: [{ ...WORKFLOW, installed: true }] });
    const { w } = await mountSurface(s);
    await w.get('[data-testid=capability-catalog-row-workflow-my-workflow] button').trigger('click');
    expect(w.get('[data-testid=catalog-detail-residue]').text()).toContain('Workflows row');
    expect(w.find('[data-testid=capability-catalog-install-workflow-my-workflow]').exists()).toBe(false);
    w.unmount();
  });

  it('M-skill. a catalog skill is a provider row only — never also a catalog-only row', async () => {
    const s = setup({ catalog: [SKILL, { ...SKILL, installed: true }], listing: { items: [skillItem('cat-standup')] } });
    const { w } = await mountSurface(s);
    expect(w.find('[data-testid=capability-row-skill-cat-standup]').exists()).toBe(true);
    expect(w.find('[data-testid^=capability-catalog-row-skill-]').exists()).toBe(false);
    w.unmount();
  });

  // ── withdraw (fleet-enforcement-truth-01PMZ505 WP11) ─────────────────────

  it('M8. Withdraw is offered on catalog-only rows, residue rows and skill provider rows that are catalog listings', async () => {
    const residue: CatalogItemView = { ...PACK, id: 'cat-p2', slug: 'old-pack', installed: true };
    const s = setup({ catalog: [BUNDLE, residue, SKILL], listing: { items: [skillItem('cat-standup')] } });
    const { w } = await mountSurface(s);
    await w.get('[data-testid=capability-catalog-row-bundle-my-bundle] button').trigger('click');
    expect(w.find('[data-testid=capability-withdraw-bundle-cat-b1]').exists()).toBe(true);
    await w.get('[data-testid=capability-catalog-row-agent_pack-old-pack] button').trigger('click');
    expect(w.find('[data-testid=capability-withdraw-agent_pack-cat-p2]').exists()).toBe(true);
    await w.get('[data-testid=capability-row-skill-cat-standup] button').trigger('click');
    expect(w.find('[data-testid=capability-withdraw-skill-cat-standup]').exists()).toBe(true);
    expect(w.get('[data-testid=catalog-listing-visibility]').text()).toBe('Private');
    w.unmount();
  });

  it('M8b. a provider row that is not a catalog listing has no Withdraw', async () => {
    const s = setup({ catalog: [], listing: { items: [skillItem('local-only')] } });
    const { w } = await mountSurface(s);
    await w.get('[data-testid=capability-row-skill-local-only] button').trigger('click');
    expect(w.find('[data-testid^=capability-withdraw-]').exists()).toBe(false);
    w.unmount();
  });

  it('M9. withdraw opens a confirm naming the org listing, distinct from Uninstall', async () => {
    const s = setup({ catalog: [BUNDLE] });
    const { w } = await mountSurface(s);
    await w.get('[data-testid=capability-catalog-row-bundle-my-bundle] button').trigger('click');
    await w.get('[data-testid=capability-withdraw-bundle-cat-b1]').trigger('click');
    expect(w.find('[data-testid=withdraw-confirm-modal]').exists()).toBe(true);
    const copy = w.get('[data-testid=withdraw-confirm-copy]').text();
    expect(copy).toContain('org catalog listing');
    expect(copy).toContain('Uninstall');
    expect(copy).toContain('local copy');
    w.unmount();
  });

  it('M10. withdraw confirm calls Catalog_Unpublish and refreshes the catalog and the capability list', async () => {
    const lists = [[BUNDLE], []];
    let call = 0;
    const s = setup({ catalog: async () => lists[Math.min(call++, 1)] });
    const { w } = await mountSurface(s);
    await w.get('[data-testid=capability-catalog-row-bundle-my-bundle] button').trigger('click');
    await w.get('[data-testid=capability-withdraw-bundle-cat-b1]').trigger('click');
    await w.get('[data-testid=withdraw-confirm]').trigger('click');
    await flushPromises();
    expect(s.unpublish).toHaveBeenCalledWith('cat-b1');
    expect(s.catalogList).toHaveBeenCalledTimes(2);
    expect(s.capList).toHaveBeenCalledTimes(2);
    expect(w.find('[data-testid=withdraw-confirm-modal]').exists()).toBe(false);
    w.unmount();
  });

  it('M11. withdraw cancel dismisses without calling Catalog_Unpublish', async () => {
    const s = setup({ catalog: [BUNDLE] });
    const { w } = await mountSurface(s);
    await w.get('[data-testid=capability-catalog-row-bundle-my-bundle] button').trigger('click');
    await w.get('[data-testid=capability-withdraw-bundle-cat-b1]').trigger('click');
    await w.get('[data-testid=withdraw-cancel]').trigger('click');
    expect(s.unpublish).not.toHaveBeenCalled();
    expect(w.find('[data-testid=withdraw-confirm-modal]').exists()).toBe(false);
    w.unmount();
  });

  it('M12. AC-020(b): a 403 on withdraw surfaces a forbidden error, never a tier message', async () => {
    const unpublish = vi.fn(async () => {
      throw new Error("fleet/catalog: not the item's owner or a fleet admin");
    });
    const s = setup({ catalog: [BUNDLE], unpublish });
    const { w } = await mountSurface(s);
    await w.get('[data-testid=capability-catalog-row-bundle-my-bundle] button').trigger('click');
    await w.get('[data-testid=capability-withdraw-bundle-cat-b1]').trigger('click');
    await w.get('[data-testid=withdraw-confirm]').trigger('click');
    await flushPromises();
    const err = w.get('[data-testid=withdraw-error]');
    expect(err.text()).toContain("not the item's owner or a fleet admin");
    expect(err.text().toLowerCase()).not.toContain('tier');
    expect(err.text().toLowerCase()).not.toContain('subscription');
    // The modal stays open on failure so the user can retry or cancel.
    expect(w.find('[data-testid=withdraw-confirm-modal]').exists()).toBe(true);
    w.unmount();
  });
});
