/**
 * MarketplaceView.spec.ts — fleet-share-and-sync-01NDFSEX14 WP03
 *
 * Five specs:
 *   1. shows not-signed-in gate when signedIn is false
 *   2. renders catalog items returned by client.catalog.list
 *   3. shows empty state when no items exist
 *   4. install button calls catalog.install and refreshes list
 *   5. uninstall button calls catalog.uninstall and refreshes list
 */
import { describe, it, expect, vi, beforeEach } from 'vitest';
import { mount, flushPromises } from '@vue/test-utils';
import { ref } from 'vue';
import MarketplaceView from '../MarketplaceView.vue';
import { createFakeHarnessClient } from '@/lib/harnessClient';
import { HarnessClientKey } from '@/lib/harnessClientContext';
import type { CatalogItemView } from '@/lib/types';

// ── featureFlags mock ──────────────────────────────────────────────────────
// Use a real Vue ref so template auto-unwrapping works correctly.
const _signedIn = ref(true);
vi.mock('@/lib/featureFlags', () => ({
  get signedIn() { return _signedIn; },
  capability: vi.fn().mockReturnValue(true),
}));

// ── CanvasHead stub (no router dependency) ─────────────────────────────────
vi.mock('@/shell/CanvasHead.vue', () => ({
  default: { template: '<div />' },
}));

// ── fixture items ──────────────────────────────────────────────────────────
const WORKFLOW_ITEM: CatalogItemView = {
  id: 'cat-001',
  kind: 'workflow',
  slug: 'my-workflow',
  version: '1.0.0',
  description: 'A test workflow',
  visibility: 'team',
  installed: false,
};

const INSTALLED_ITEM: CatalogItemView = {
  id: 'cat-002',
  kind: 'bundle',
  slug: 'my-bundle',
  version: '2.1.0',
  description: 'A test bundle',
  visibility: 'org_public',
  installed: true,
};

function buildClient(items: CatalogItemView[] = []) {
  const listFn = vi.fn(async () => items);
  const installFn = vi.fn(async () => {});
  const uninstallFn = vi.fn(async () => {});
  const client = createFakeHarnessClient({
    catalog: {
      publish: async () => ({ ...WORKFLOW_ITEM }),
      list: listFn,
      install: installFn,
      uninstall: uninstallFn,
      installed: async () => [],
    },
  });
  return { client, listFn, installFn, uninstallFn };
}

function mountView(client = buildClient().client) {
  return mount(MarketplaceView, {
    global: { provide: { [HarnessClientKey as symbol]: client } },
  });
}

describe('MarketplaceView', () => {
  beforeEach(() => {
    _signedIn.value = true;
  });

  it('1. shows not-signed-in gate when user is not signed in', async () => {
    _signedIn.value = false;
    const { client } = buildClient();
    const wrapper = mountView(client);
    await flushPromises();

    expect(wrapper.find('[data-testid="marketplace-not-signed-in"]').exists()).toBe(true);
    expect(wrapper.find('[data-testid="marketplace-grid"]').exists()).toBe(false);
  });

  it('2. renders catalog items from client.catalog.list', async () => {
    const { client } = buildClient([WORKFLOW_ITEM, INSTALLED_ITEM]);
    const wrapper = mountView(client);
    await flushPromises();

    expect(wrapper.find('[data-testid="marketplace-grid"]').exists()).toBe(true);
    expect(wrapper.find('[data-testid="marketplace-item-my-workflow"]').exists()).toBe(true);
    expect(wrapper.find('[data-testid="marketplace-item-my-bundle"]').exists()).toBe(true);
    // installed badge should be shown for the installed item
    expect(wrapper.find('[data-testid="item-installed-badge"]').exists()).toBe(true);
  });

  it('3. shows empty state when catalog returns no items', async () => {
    const { client } = buildClient([]);
    const wrapper = mountView(client);
    await flushPromises();

    expect(wrapper.find('[data-testid="marketplace-empty"]').exists()).toBe(true);
    expect(wrapper.find('[data-testid="marketplace-grid"]').exists()).toBe(false);
  });

  it('4. install button calls catalog.install and refreshes the list', async () => {
    const listFn = vi.fn()
      .mockResolvedValueOnce([WORKFLOW_ITEM])
      .mockResolvedValueOnce([{ ...WORKFLOW_ITEM, installed: true }]);
    const installFn = vi.fn(async () => {});
    const client = createFakeHarnessClient({
      catalog: {
        publish: async () => ({ ...WORKFLOW_ITEM }),
        list: listFn,
        install: installFn,
        uninstall: async () => {},
        installed: async () => [],
      },
    });

    const wrapper = mountView(client);
    await flushPromises();

    const installBtn = wrapper.find('[data-testid="item-install-btn-my-workflow"]');
    expect(installBtn.exists()).toBe(true);
    await installBtn.trigger('click');
    await flushPromises();

    expect(installFn).toHaveBeenCalledWith('cat-001', '1.0.0');
    // list should have been refreshed (called twice total)
    expect(listFn).toHaveBeenCalledTimes(2);
  });

  it('5. uninstall button calls catalog.uninstall and refreshes the list', async () => {
    const listFn = vi.fn()
      .mockResolvedValueOnce([INSTALLED_ITEM])
      .mockResolvedValueOnce([{ ...INSTALLED_ITEM, installed: false }]);
    const uninstallFn = vi.fn(async () => {});
    const client = createFakeHarnessClient({
      catalog: {
        publish: async () => ({ ...WORKFLOW_ITEM }),
        list: listFn,
        install: async () => {},
        uninstall: uninstallFn,
        installed: async () => [],
      },
    });

    const wrapper = mountView(client);
    await flushPromises();

    const uninstallBtn = wrapper.find('[data-testid="item-uninstall-btn-my-bundle"]');
    expect(uninstallBtn.exists()).toBe(true);
    await uninstallBtn.trigger('click');
    await flushPromises();

    expect(uninstallFn).toHaveBeenCalledWith('bundle', 'cat-002', '2.1.0');
    expect(listFn).toHaveBeenCalledTimes(2);
  });
});
