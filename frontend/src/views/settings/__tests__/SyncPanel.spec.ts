/**
 * SyncPanel.spec.ts — fleet-share-and-sync-01NDFSEX14 WP06
 *
 * Six specs:
 *   1. shows not-signed-in gate when signedIn is false
 *   2. shows the sync UI for an entitled user (capability 'context_sync')
 *   3. shows pro-gate when the entitlement is absent
 *   4. renders category list with toggles for each category
 *   5. toggle calls client.sync.toggle with correct arguments
 *   6. pending-secrets banner appears when pendingMCPSecrets returns results
 *
 * The capability fake below is deliberately **key-sensitive**: it grants
 * only SYNC_CAPABILITY. Before 2026-08-14 this file mocked capability() to
 * return true unconditionally and asserted only the negative path, so it
 * stayed green while SyncPanel gated on `settings_sync` — a key that does
 * not exist in `fleet.AllCapabilities()`. Spec 2 is the positive assertion
 * whose absence let that ship; it fails if the gate key drifts again.
 */
import { describe, it, expect, vi, beforeEach } from 'vitest';
import { mount, flushPromises } from '@vue/test-utils';
import { ref } from 'vue';
import SyncPanel from '@/views/settings/SyncPanel.vue';
import { createFakeHarnessClient } from '@/lib/harnessClient';
import { HarnessClientKey } from '@/lib/harnessClientContext';
import { capability } from '@/lib/featureFlags';
import type { SyncStatusView, PendingMCPSecret } from '@/lib/types';

// ── featureFlags mock ──────────────────────────────────────────────────────
// Use a real Vue ref so template auto-unwrapping works correctly.
const _signedIn = ref(true);

// The wire key SyncPanel must gate on — `fleet.CapContextSync`
// (core/fleet/capability.go). Kept as a const so the intent is explicit.
const SYNC_CAPABILITY = 'context_sync';

// Capability keys the fake fleet snapshot has enabled. Mutated per-test;
// capability() answers from this set, so asking for any other key is false.
const _enabledCaps = new Set<string>();

vi.mock('@/lib/featureFlags', () => ({
  get signedIn() { return _signedIn; },
  capability: vi.fn(),
}));

// ── vue-router stub (SyncPanel uses useRouter optionally) ─────────────────
vi.mock('vue-router', () => ({
  useRouter: () => ({ push: vi.fn() }),
  useRoute: () => undefined,
}));

// ── fixtures ───────────────────────────────────────────────────────────────
//
// Canonical wire ids for SyncCategory, mirroring corefleet.AllSyncCategories()
// (core/fleet/sync.go:39-47) in declaration order. Both the response fixture
// below and the coverage assertion in spec 4 read from this single list, so
// a wrong id here fails loudly instead of the two independently hardcoded
// literals silently drifting apart — the exact shape of the bug this pins:
// before WP07 (fleet-enforcement-truth-01PMZ505) this fixture hand-built
// 'installed_mcp_servers', which is NOT corefleet.SyncCategoryInstalledMCP
// ("installed_mcp") and so kept this file green while the real panel
// (SyncPanel.vue) sent an id the backend rejected with "unknown category"
// on every toggle. CLAUDE.md names this blind spot explicitly: a test
// fixture that bypasses the layer under test.
const BACKEND_SYNC_CATEGORY_IDS = [
  'provider_profiles',
  'model_prefs',
  'mcp_recipes',
  'installed_mcp',
  'ui_theme',
] as const;

const STATUSES: SyncStatusView[] = [
  { category: BACKEND_SYNC_CATEGORY_IDS[0], enabled: true, last_push_at: '2026-07-01T10:00:00Z' },
  { category: BACKEND_SYNC_CATEGORY_IDS[1], enabled: false },
  { category: BACKEND_SYNC_CATEGORY_IDS[2], enabled: false },
  { category: BACKEND_SYNC_CATEGORY_IDS[3], enabled: true },
  { category: BACKEND_SYNC_CATEGORY_IDS[4], enabled: false },
];

const PENDING_SECRET: PendingMCPSecret = {
  mcp_id: 'mcp-001',
  recipe_id: 'recipe-001',
  requires_secret_keys: ['API_KEY'],
};

function buildClient(overrides: {
  statuses?: SyncStatusView[];
  pending?: PendingMCPSecret[];
  toggleFn?: ReturnType<typeof vi.fn>;
} = {}) {
  const toggleFn = overrides.toggleFn ?? vi.fn(async () => {});
  return {
    client: createFakeHarnessClient({
      sync: {
        status: async () => overrides.statuses ?? STATUSES,
        toggle: toggleFn,
        forcePush: async () => {},
        forcePull: async () => {},
        pendingMCPSecrets: async () => overrides.pending ?? [],
      },
    }),
    toggleFn,
  };
}

function mountPanel(client = buildClient().client) {
  return mount(SyncPanel, {
    global: { provide: { [HarnessClientKey as symbol]: client } },
  });
}

describe('SyncPanel', () => {
  beforeEach(() => {
    _signedIn.value = true;
    _enabledCaps.clear();
    _enabledCaps.add(SYNC_CAPABILITY);
    vi.mocked(capability).mockImplementation((key: string) => _enabledCaps.has(key));
  });

  it('1. shows not-signed-in gate when signedIn is false', async () => {
    _signedIn.value = false;
    const { client } = buildClient();
    const wrapper = mountPanel(client);
    await flushPromises();

    expect(wrapper.find('[data-testid="sync-not-signed-in"]').exists()).toBe(true);
    expect(wrapper.find('[data-testid="sync-category-list"]').exists()).toBe(false);
  });

  it('2. shows the sync UI to an entitled user holding "context_sync"', async () => {
    // Entitled: signed in, and the ONLY capability granted is the real wire
    // key. If SyncPanel asks for anything else (e.g. the historic
    // `settings_sync` typo) the fake answers false and the pro-gate renders
    // instead — which is exactly what this spec fails on.
    _signedIn.value = true;
    _enabledCaps.clear();
    _enabledCaps.add(SYNC_CAPABILITY);
    const { client } = buildClient({ statuses: STATUSES });
    const wrapper = mountPanel(client);
    await flushPromises();

    expect(wrapper.find('[data-testid="sync-pro-gate"]').exists()).toBe(false);
    expect(wrapper.find('[data-testid="sync-category-list"]').exists()).toBe(true);
    expect(vi.mocked(capability)).toHaveBeenCalledWith(SYNC_CAPABILITY);
  });

  it('3. shows pro-gate when the sync entitlement is absent', async () => {
    _signedIn.value = true;
    _enabledCaps.clear(); // signed in, but no capabilities granted
    const { client } = buildClient();
    const wrapper = mountPanel(client);
    await flushPromises();

    expect(wrapper.find('[data-testid="sync-pro-gate"]').exists()).toBe(true);
    expect(wrapper.find('[data-testid="sync-category-list"]').exists()).toBe(false);
  });

  it('4. renders all 5 categories with toggles, matching the canonical backend ids', async () => {
    const { client } = buildClient({ statuses: STATUSES });
    const wrapper = mountPanel(client);
    await flushPromises();

    for (const id of BACKEND_SYNC_CATEGORY_IDS) {
      expect(wrapper.find(`[data-testid="sync-category-${id}"]`).exists()).toBe(true);
      expect(wrapper.find(`[data-testid="sync-toggle-${id}"]`).exists()).toBe(true);
    }
  });

  it('5. toggle checkbox calls sync.toggle with correct category and enabled state', async () => {
    const { client, toggleFn } = buildClient({ statuses: STATUSES });
    const wrapper = mountPanel(client);
    await flushPromises();

    // model_prefs is currently disabled — toggling should call toggle('model_prefs', true)
    const toggle = wrapper.find('[data-testid="sync-toggle-model_prefs"]');
    await toggle.trigger('change');
    await flushPromises();

    expect(toggleFn).toHaveBeenCalledWith('model_prefs', true);
  });

  it('7. installed-MCP toggle sends the canonical backend category id, not the historic installed_mcp_servers id', async () => {
    const { client, toggleFn } = buildClient({ statuses: STATUSES });
    const wrapper = mountPanel(client);
    await flushPromises();

    // STATUSES marks installed_mcp enabled — toggling flips it off.
    const toggle = wrapper.find('[data-testid="sync-toggle-installed_mcp"]');
    expect(toggle.exists()).toBe(true);
    await toggle.trigger('change');
    await flushPromises();

    expect(toggleFn).toHaveBeenCalledWith('installed_mcp', false);
    expect(toggleFn).not.toHaveBeenCalledWith('installed_mcp_servers', expect.anything());
  });

  it('6. pending-secrets banner appears when MCP servers need credentials', async () => {
    const { client } = buildClient({ pending: [PENDING_SECRET] });
    const wrapper = mountPanel(client);
    await flushPromises();

    const banner = wrapper.find('[data-testid="sync-pending-secrets-banner"]');
    expect(banner.exists()).toBe(true);
    expect(banner.text()).toContain('1 MCP server need');
    expect(wrapper.find('[data-testid="sync-go-to-mcp-btn"]').exists()).toBe(true);
  });
});
