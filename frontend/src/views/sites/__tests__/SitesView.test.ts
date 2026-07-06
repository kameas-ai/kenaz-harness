import { describe, it, expect, vi, beforeEach } from 'vitest';
import { mount, flushPromises } from '@vue/test-utils';
import SitesView from '@/views/sites/SitesView.vue';
import { createFakeHarnessClient } from '@/lib/harnessClient';
import { HarnessClientKey } from '@/lib/harnessClientContext';
import type { SiteSummary } from '@/lib/types';

const STATIC_SITE: SiteSummary = {
  name: 'my-docs',
  kind: 'static',
  url: 'https://my-docs.fleet.example.com',
  status: 'live',
  deployedAt: new Date(Date.now() - 2 * 60_000).toISOString(), // 2m ago
};

const DYNAMIC_SITE: SiteSummary = {
  name: 'api-server',
  kind: 'dynamic',
  url: 'https://api-server.fleet.example.com',
  status: 'live',
  deployedAt: new Date(Date.now() - 3600_000).toISOString(), // 1h ago
};

const FAILED_SITE: SiteSummary = {
  name: 'broken',
  kind: 'static',
  url: '',
  status: 'failed',
};

function provide(overrides: Partial<ReturnType<typeof createFakeHarnessClient>> = {}) {
  const client = createFakeHarnessClient(overrides);
  return {
    client,
    global: { provide: { [HarnessClientKey as symbol]: client } },
  };
}

describe('SitesView', () => {
  beforeEach(() => {
    vi.restoreAllMocks();
  });

  it('renders the section header', async () => {
    const { global } = provide();
    const w = mount(SitesView, { global });
    await flushPromises();
    expect(w.text()).toContain('SITES');
    expect(w.text()).toContain('Hosted sites');
  });

  it('shows empty state with MCP one-liner when no sites', async () => {
    const { global } = provide({
      sites: {
        list: async () => [],
        deploy: vi.fn(),
        status: vi.fn(),
        logs: vi.fn(),
        delete: vi.fn(),
      },
    });
    const w = mount(SitesView, { global });
    await flushPromises();
    expect(w.find('[data-testid=sites-empty]').exists()).toBe(true);
    expect(w.text()).toContain('No sites deployed');
    expect(w.find('[data-testid=sites-mcp-oneliner]').text()).toContain(
      'claude mcp add fleet-sites',
    );
  });

  it('renders a table row for each site', async () => {
    const { global } = provide({
      sites: {
        list: async () => [STATIC_SITE, DYNAMIC_SITE, FAILED_SITE],
        deploy: vi.fn(),
        status: vi.fn(),
        logs: vi.fn(),
        delete: vi.fn(),
      },
    });
    const w = mount(SitesView, { global });
    await flushPromises();
    const table = w.find('[data-testid=sites-table]');
    expect(table.exists()).toBe(true);
    expect(w.find('[data-testid="site-row-my-docs"]').exists()).toBe(true);
    expect(w.find('[data-testid="site-row-api-server"]').exists()).toBe(true);
    expect(w.find('[data-testid="site-row-broken"]').exists()).toBe(true);
  });

  it('shows kind badge for each site', async () => {
    const { global } = provide({
      sites: {
        list: async () => [STATIC_SITE, DYNAMIC_SITE],
        deploy: vi.fn(),
        status: vi.fn(),
        logs: vi.fn(),
        delete: vi.fn(),
      },
    });
    const w = mount(SitesView, { global });
    await flushPromises();
    expect(w.find('[data-testid="site-kind-my-docs"]').text()).toBe('static');
    expect(w.find('[data-testid="site-kind-api-server"]').text()).toBe('dynamic');
  });

  it('shows logs button only for dynamic sites', async () => {
    const { global } = provide({
      sites: {
        list: async () => [STATIC_SITE, DYNAMIC_SITE],
        deploy: vi.fn(),
        status: vi.fn(),
        logs: vi.fn(),
        delete: vi.fn(),
      },
    });
    const w = mount(SitesView, { global });
    await flushPromises();
    // static: no logs button
    expect(w.find('[data-testid="site-logs-my-docs"]').exists()).toBe(false);
    // dynamic: logs button present
    expect(w.find('[data-testid="site-logs-api-server"]').exists()).toBe(true);
  });

  it('shows error state when list throws', async () => {
    const { global } = provide({
      sites: {
        list: async () => { throw new Error('fleet offline'); },
        deploy: vi.fn(),
        status: vi.fn(),
        logs: vi.fn(),
        delete: vi.fn(),
      },
    });
    const w = mount(SitesView, { global });
    await flushPromises();
    expect(w.find('[data-testid=sites-error]').text()).toContain('fleet offline');
  });

  it('opens delete confirm modal and calls delete on confirm', async () => {
    const deleteFn = vi.fn().mockResolvedValue(undefined);
    const listFn = vi.fn()
      .mockResolvedValueOnce([STATIC_SITE])
      .mockResolvedValue([]);
    const { global } = provide({
      sites: {
        list: listFn,
        deploy: vi.fn(),
        status: vi.fn(),
        logs: vi.fn(),
        delete: deleteFn,
      },
    });
    const w = mount(SitesView, { global });
    await flushPromises();

    // Open confirm modal
    await w.find('[data-testid="site-delete-my-docs"]').trigger('click');
    expect(w.find('[data-testid=delete-confirm-modal]').exists()).toBe(true);
    expect(w.text()).toContain('Delete "my-docs"?');

    // Click Delete
    await w.find('[data-testid=delete-confirm]').trigger('click');
    await flushPromises();
    expect(deleteFn).toHaveBeenCalledWith('my-docs');
    // modal closed
    expect(w.find('[data-testid=delete-confirm-modal]').exists()).toBe(false);
    // table now empty
    expect(w.find('[data-testid=sites-empty]').exists()).toBe(true);
  });

  it('cancel delete modal dismisses without calling delete', async () => {
    const deleteFn = vi.fn();
    const { global } = provide({
      sites: {
        list: async () => [STATIC_SITE],
        deploy: vi.fn(),
        status: vi.fn(),
        logs: vi.fn(),
        delete: deleteFn,
      },
    });
    const w = mount(SitesView, { global });
    await flushPromises();
    await w.find('[data-testid="site-delete-my-docs"]').trigger('click');
    expect(w.find('[data-testid=delete-confirm-modal]').exists()).toBe(true);
    await w.find('[data-testid=delete-cancel]').trigger('click');
    expect(deleteFn).not.toHaveBeenCalled();
    expect(w.find('[data-testid=delete-confirm-modal]').exists()).toBe(false);
  });

  it('shows delete error when delete throws', async () => {
    const { global } = provide({
      sites: {
        list: async () => [STATIC_SITE],
        deploy: vi.fn(),
        status: vi.fn(),
        logs: vi.fn(),
        delete: async () => { throw new Error('permission denied'); },
      },
    });
    const w = mount(SitesView, { global });
    await flushPromises();
    await w.find('[data-testid="site-delete-my-docs"]').trigger('click');
    await w.find('[data-testid=delete-confirm]').trigger('click');
    await flushPromises();
    expect(w.find('[data-testid=delete-error]').text()).toContain('permission denied');
    // modal still open
    expect(w.find('[data-testid=delete-confirm-modal]').exists()).toBe(true);
  });

  it('deploy button calls pickDirectory then deploy', async () => {
    const deployFn = vi.fn().mockResolvedValue(STATIC_SITE);
    const pickFn = vi.fn().mockResolvedValue('/home/user/my-site');
    const listFn = vi.fn().mockResolvedValue([]);
    // Build a full fake client so tools.pickDirectory can be overridden.
    const baseClient = createFakeHarnessClient({
      sites: {
        list: listFn,
        deploy: deployFn,
        status: vi.fn(),
        logs: vi.fn(),
        delete: vi.fn(),
      },
    });
    // Patch only pickDirectory; keep everything else from the fake.
    baseClient.tools.pickDirectory = pickFn;
    const global = { provide: { [HarnessClientKey as symbol]: baseClient } };
    const w = mount(SitesView, { global });
    await flushPromises();
    await w.find('[data-testid=sites-deploy-btn]').trigger('click');
    await flushPromises();
    expect(pickFn).toHaveBeenCalledWith('Select site folder', '');
    expect(deployFn).toHaveBeenCalledWith('/home/user/my-site');
  });

  it('shows deploy error when deploy throws', async () => {
    const deployFn = vi.fn().mockRejectedValue(new Error('invalid manifest'));
    const pickFn = vi.fn().mockResolvedValue('/home/user/bad-site');
    const baseClient = createFakeHarnessClient({
      sites: {
        list: async () => [],
        deploy: deployFn,
        status: vi.fn(),
        logs: vi.fn(),
        delete: vi.fn(),
      },
    });
    baseClient.tools.pickDirectory = pickFn;
    const global = { provide: { [HarnessClientKey as symbol]: baseClient } };
    const w = mount(SitesView, { global });
    await flushPromises();
    await w.find('[data-testid=sites-deploy-btn]').trigger('click');
    await flushPromises();
    expect(w.find('[data-testid=sites-deploy-error]').text()).toContain('invalid manifest');
  });

  it('logs modal fetches and displays logs', async () => {
    const logsFn = vi.fn().mockResolvedValue('INFO: server started\nINFO: request handled');
    const { global } = provide({
      sites: {
        list: async () => [DYNAMIC_SITE],
        deploy: vi.fn(),
        status: vi.fn(),
        logs: logsFn,
        delete: vi.fn(),
      },
    });
    const w = mount(SitesView, { global });
    await flushPromises();
    await w.find('[data-testid="site-logs-api-server"]').trigger('click');
    await flushPromises();
    expect(logsFn).toHaveBeenCalledWith('api-server', 200);
    const modal = w.find('[data-testid=logs-modal]');
    expect(modal.exists()).toBe(true);
    expect(w.find('[data-testid=logs-content]').text()).toContain('server started');
  });

  it('logs modal can be closed', async () => {
    const { global } = provide({
      sites: {
        list: async () => [DYNAMIC_SITE],
        deploy: vi.fn(),
        status: vi.fn(),
        logs: vi.fn().mockResolvedValue(''),
        delete: vi.fn(),
      },
    });
    const w = mount(SitesView, { global });
    await flushPromises();
    await w.find('[data-testid="site-logs-api-server"]').trigger('click');
    await flushPromises();
    await w.find('[data-testid=logs-modal-close]').trigger('click');
    expect(w.find('[data-testid=logs-modal]').exists()).toBe(false);
  });

  it('uses only design tokens — no raw hex/rgba', async () => {
    const { global } = provide();
    const w = mount(SitesView, { global });
    await flushPromises();
    const html = w.html();
    expect(html).not.toMatch(/#[0-9a-fA-F]{3,8}\b/);
    expect(html).not.toMatch(/rgba?\s*\(/i);
  });
});
