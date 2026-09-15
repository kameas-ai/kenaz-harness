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
        envSet: vi.fn(),
        envList: vi.fn().mockResolvedValue([]),
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
        envSet: vi.fn(),
        envList: vi.fn().mockResolvedValue([]),
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
        envSet: vi.fn(),
        envList: vi.fn().mockResolvedValue([]),
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
        envSet: vi.fn(),
        envList: vi.fn().mockResolvedValue([]),
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
        envSet: vi.fn(),
        envList: vi.fn().mockResolvedValue([]),
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
        envSet: vi.fn(),
        envList: vi.fn().mockResolvedValue([]),
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
        envSet: vi.fn(),
        envList: vi.fn().mockResolvedValue([]),
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
        envSet: vi.fn(),
        envList: vi.fn().mockResolvedValue([]),
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
        envSet: vi.fn(),
        envList: vi.fn().mockResolvedValue([]),
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
        envSet: vi.fn(),
        envList: vi.fn().mockResolvedValue([]),
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
        envSet: vi.fn(),
        envList: vi.fn().mockResolvedValue([]),
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
        envSet: vi.fn(),
        envList: vi.fn().mockResolvedValue([]),
      },
    });
    const w = mount(SitesView, { global });
    await flushPromises();
    await w.find('[data-testid="site-logs-api-server"]').trigger('click');
    await flushPromises();
    await w.find('[data-testid=logs-modal-close]').trigger('click');
    expect(w.find('[data-testid=logs-modal]').exists()).toBe(false);
  });

  // ── env vars modal (fleet-enforcement-truth-01PMZ505 WP09) ────────────────

  it('env vars modal fetches and displays declared names + status', async () => {
    const envListFn = vi.fn().mockResolvedValue([
      { name: 'API_KEY', description: 'Upstream API key', setAt: '2026-09-01T00:00:00Z' },
      { name: 'DEBUG', description: '' },
    ]);
    const { global } = provide({
      sites: {
        list: async () => [DYNAMIC_SITE],
        deploy: vi.fn(),
        status: vi.fn(),
        logs: vi.fn(),
        delete: vi.fn(),
        envSet: vi.fn(),
        envList: envListFn,
      },
    });
    const w = mount(SitesView, { global });
    await flushPromises();
    await w.find('[data-testid="site-env-api-server"]').trigger('click');
    await flushPromises();
    expect(envListFn).toHaveBeenCalledWith('api-server');
    expect(w.find('[data-testid=env-modal]').exists()).toBe(true);
    expect(w.find('[data-testid="env-entry-API_KEY"]').exists()).toBe(true);
    expect(w.find('[data-testid="env-status-API_KEY"]').text()).toContain('Set');
    expect(w.find('[data-testid="env-status-DEBUG"]').text()).toBe('Not set');
    // The wire shape carries no value — assert the modal never renders one.
    expect(w.html()).not.toContain('setAt=');
  });

  it('env vars modal shows empty state when the manifest declares no vars', async () => {
    const { global } = provide({
      sites: {
        list: async () => [DYNAMIC_SITE],
        deploy: vi.fn(),
        status: vi.fn(),
        logs: vi.fn(),
        delete: vi.fn(),
        envSet: vi.fn(),
        envList: vi.fn().mockResolvedValue([]),
      },
    });
    const w = mount(SitesView, { global });
    await flushPromises();
    await w.find('[data-testid="site-env-api-server"]').trigger('click');
    await flushPromises();
    expect(w.find('[data-testid=env-empty]').exists()).toBe(true);
  });

  it('env vars modal shows a load error when envList rejects', async () => {
    const { global } = provide({
      sites: {
        list: async () => [DYNAMIC_SITE],
        deploy: vi.fn(),
        status: vi.fn(),
        logs: vi.fn(),
        delete: vi.fn(),
        envSet: vi.fn(),
        envList: async () => {
          throw new Error('fleet unreachable');
        },
      },
    });
    const w = mount(SitesView, { global });
    await flushPromises();
    await w.find('[data-testid="site-env-api-server"]').trigger('click');
    await flushPromises();
    expect(w.find('[data-testid=env-list-error]').text()).toContain('fleet unreachable');
  });

  it('saving an env var calls envSet with the typed value and clears the input', async () => {
    const envSetFn = vi.fn().mockResolvedValue(undefined);
    const envListFn = vi.fn()
      .mockResolvedValueOnce([{ name: 'API_KEY', description: '' }])
      .mockResolvedValueOnce([
        { name: 'API_KEY', description: '', setAt: '2026-09-15T00:00:00Z' },
      ]);
    const { global } = provide({
      sites: {
        list: async () => [DYNAMIC_SITE],
        deploy: vi.fn(),
        status: vi.fn(),
        logs: vi.fn(),
        delete: vi.fn(),
        envSet: envSetFn,
        envList: envListFn,
      },
    });
    const w = mount(SitesView, { global });
    await flushPromises();
    await w.find('[data-testid="site-env-api-server"]').trigger('click');
    await flushPromises();

    const input = w.find('[data-testid="env-input-API_KEY"]');
    await input.setValue('sk-super-secret-value');
    await w.find('[data-testid="env-save-API_KEY"]').trigger('click');
    await flushPromises();

    expect(envSetFn).toHaveBeenCalledWith('api-server', { API_KEY: 'sk-super-secret-value' });

    // Mutation-check: the component must not hold the submitted value in
    // state after a successful save — the input re-renders empty and the
    // typed value never appears anywhere in the rendered HTML.
    expect((w.find('[data-testid="env-input-API_KEY"]').element as HTMLInputElement).value).toBe('');
    expect(w.html()).not.toContain('sk-super-secret-value');
    // And the status now reflects the reloaded entry.
    expect(w.find('[data-testid="env-status-API_KEY"]').text()).toContain('Set');
  });

  it('shows a save error and keeps the modal open when envSet rejects', async () => {
    const { global } = provide({
      sites: {
        list: async () => [DYNAMIC_SITE],
        deploy: vi.fn(),
        status: vi.fn(),
        logs: vi.fn(),
        delete: vi.fn(),
        envSet: async () => {
          throw new Error('permission denied');
        },
        envList: vi.fn().mockResolvedValue([{ name: 'API_KEY', description: '' }]),
      },
    });
    const w = mount(SitesView, { global });
    await flushPromises();
    await w.find('[data-testid="site-env-api-server"]').trigger('click');
    await flushPromises();
    await w.find('[data-testid="env-input-API_KEY"]').setValue('value');
    await w.find('[data-testid="env-save-API_KEY"]').trigger('click');
    await flushPromises();
    expect(w.find('[data-testid=env-save-error]').text()).toContain('permission denied');
    expect(w.find('[data-testid=env-modal]').exists()).toBe(true);
  });

  it('env vars modal can be closed', async () => {
    const { global } = provide({
      sites: {
        list: async () => [DYNAMIC_SITE],
        deploy: vi.fn(),
        status: vi.fn(),
        logs: vi.fn(),
        delete: vi.fn(),
        envSet: vi.fn(),
        envList: vi.fn().mockResolvedValue([]),
      },
    });
    const w = mount(SitesView, { global });
    await flushPromises();
    await w.find('[data-testid="site-env-api-server"]').trigger('click');
    await flushPromises();
    await w.find('[data-testid=env-modal-close]').trigger('click');
    expect(w.find('[data-testid=env-modal]').exists()).toBe(false);
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
