/**
 * SyncPanel.spec.ts — fleet-share-and-sync-01NDFSEX14 WP06
 *
 * Five specs:
 *   1. shows not-signed-in gate when signedIn is false
 *   2. shows pro-gate when capability('settings_sync') is false
 *   3. renders category list with toggles for each category
 *   4. toggle calls client.sync.toggle with correct arguments
 *   5. pending-secrets banner appears when pendingMCPSecrets returns results
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
vi.mock('@/lib/featureFlags', () => ({
  get signedIn() { return _signedIn; },
  capability: vi.fn().mockReturnValue(true),
}));

// ── vue-router stub (SyncPanel uses useRouter optionally) ─────────────────
vi.mock('vue-router', () => ({
  useRouter: () => ({ push: vi.fn() }),
  useRoute: () => undefined,
}));

// ── fixtures ───────────────────────────────────────────────────────────────
const STATUSES: SyncStatusView[] = [
  { category: 'provider_profiles', enabled: true, last_push_at: '2026-07-01T10:00:00Z' },
  { category: 'model_prefs', enabled: false },
  { category: 'mcp_recipes', enabled: false },
  { category: 'installed_mcp_servers', enabled: true },
  { category: 'ui_theme', enabled: false },
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
    vi.mocked(capability).mockReturnValue(true);
  });

  it('1. shows not-signed-in gate when signedIn is false', async () => {
    _signedIn.value = false;
    const { client } = buildClient();
    const wrapper = mountPanel(client);
    await flushPromises();

    expect(wrapper.find('[data-testid="sync-not-signed-in"]').exists()).toBe(true);
    expect(wrapper.find('[data-testid="sync-category-list"]').exists()).toBe(false);
  });

  it('2. shows pro-gate when capability("settings_sync") returns false', async () => {
    _signedIn.value = true;
    vi.mocked(capability).mockReturnValue(false);
    const { client } = buildClient();
    const wrapper = mountPanel(client);
    await flushPromises();

    expect(wrapper.find('[data-testid="sync-pro-gate"]').exists()).toBe(true);
    expect(wrapper.find('[data-testid="sync-category-list"]').exists()).toBe(false);
  });

  it('3. renders all 5 categories with toggles', async () => {
    const { client } = buildClient({ statuses: STATUSES });
    const wrapper = mountPanel(client);
    await flushPromises();

    const categories = [
      'provider_profiles',
      'model_prefs',
      'mcp_recipes',
      'installed_mcp_servers',
      'ui_theme',
    ];

    for (const id of categories) {
      expect(wrapper.find(`[data-testid="sync-category-${id}"]`).exists()).toBe(true);
      expect(wrapper.find(`[data-testid="sync-toggle-${id}"]`).exists()).toBe(true);
    }
  });

  it('4. toggle checkbox calls sync.toggle with correct category and enabled state', async () => {
    const { client, toggleFn } = buildClient({ statuses: STATUSES });
    const wrapper = mountPanel(client);
    await flushPromises();

    // model_prefs is currently disabled — toggling should call toggle('model_prefs', true)
    const toggle = wrapper.find('[data-testid="sync-toggle-model_prefs"]');
    await toggle.trigger('change');
    await flushPromises();

    expect(toggleFn).toHaveBeenCalledWith('model_prefs', true);
  });

  it('5. pending-secrets banner appears when MCP servers need credentials', async () => {
    const { client } = buildClient({ pending: [PENDING_SECRET] });
    const wrapper = mountPanel(client);
    await flushPromises();

    const banner = wrapper.find('[data-testid="sync-pending-secrets-banner"]');
    expect(banner.exists()).toBe(true);
    expect(banner.text()).toContain('1 MCP server need');
    expect(wrapper.find('[data-testid="sync-go-to-mcp-btn"]').exists()).toBe(true);
  });
});
