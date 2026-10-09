/**
 * ToolExposurePanel (tool-context-budget-01TCBUD01 WP06, FR-K1): every
 * control writes the layer the resolver reads for the next request — the
 * user layer by default, the project layer once a project is chosen — with
 * the stored budget / TTL preserved, and the rows are re-read from the
 * resolver afterwards. Org-pinned rows cannot be edited.
 */
import { describe, it, expect, vi } from 'vitest';
import { mount, flushPromises } from '@vue/test-utils';
import ToolExposurePanel from '@/views/tools/ToolExposurePanel.vue';
import { createFakeHarnessClient } from '@/lib/harnessClient';
import { HarnessClientKey } from '@/lib/harnessClientContext';
import type { ServerSchemaCost, ToolExposure, ToolExposureSettings } from '@/lib/types';

const SETTINGS: ToolExposureSettings = {
  exposure: { servers: { fetch: { tier: 'summary' } } },
  schemaBudgetTokens: 0,
  activationTtlTurns: 9,
  effectiveSchemaBudgetTokens: 24_000,
  effectiveActivationTtlTurns: 9,
  org: { settings: [], schemaBudgetTokens: 0, bundleId: 0 },
};

const COSTS: ServerSchemaCost[] = [
  {
    server: 'outlook', state: 'running', running: true, toolCount: 2, tokenEst: 6400,
    tier: 'summary', source: 'default', pinned: false, sendableTokenEst: 0,
    tools: [
      { name: 'list-messages', tokenEst: 3000, tier: 'summary', source: 'default', activated: false, sendable: false, hot: false },
      { name: 'send-mail', tokenEst: 3400, tier: 'summary', source: 'default', activated: false, sendable: false, hot: false },
    ],
  },
  {
    server: 'fetch', state: 'running', running: true, toolCount: 1, tokenEst: 300,
    tier: 'full', source: 'org_pin', pinned: true, sendableTokenEst: 300,
    tools: [{ name: 'fetch', tokenEst: 300, tier: 'full', source: 'org_pin', activated: false, sendable: true, hot: false }],
  },
  {
    server: 'github', state: 'failed', running: false, toolCount: 0, tokenEst: 0,
    tier: 'summary', source: 'default', pinned: false, sendableTokenEst: 0, tools: [],
  },
];

function setup(
  opts: {
    setSettings?: () => Promise<void>;
    projectLayer?: ToolExposure;
    costs?: ServerSchemaCost[];
    settings?: ToolExposureSettings;
  } = {},
) {
  const base = createFakeHarnessClient();
  const schemaCosts = vi.fn(async () => opts.costs ?? COSTS);
  let stored = { ...(opts.settings ?? SETTINGS) };
  const getSettings = vi.fn(async () => stored);
  const setSettings = vi.fn(opts.setSettings ?? (async (s: ToolExposureSettings) => {
    stored = { ...s };
  }));
  const getProject = vi.fn(async () => opts.projectLayer ?? {});
  const setProject = vi.fn(async () => {});
  const client = {
    ...base,
    tools: { ...base.tools, schemaCosts },
    settings: { ...base.settings, getToolExposure: getSettings, setToolExposure: setSettings },
    projects: {
      ...base.projects,
      list: async () => [{ id: 'p1', name: 'Alpha', description: '', createdAt: '', updatedAt: '' }],
      getToolExposure: getProject,
      setToolExposure: setProject,
    },
  };
  const w = mount(ToolExposurePanel, {
    global: { provide: { [HarnessClientKey as symbol]: client } },
  });
  const replaceStored = (s: ToolExposureSettings) => {
    stored = { ...s };
  };
  return { w, schemaCosts, setSettings, getProject, setProject, replaceStored };
}

async function choose(w: ReturnType<typeof setup>['w'], testid: string, value: string) {
  const el = w.find(`[data-testid="${testid}"]`);
  await el.setValue(value);
  await flushPromises();
}

describe('ToolExposurePanel', () => {
  it('shows each server with its schema cost when loaded and its resolved tier', async () => {
    const { w, schemaCosts } = setup();
    await flushPromises();
    expect(schemaCosts).toHaveBeenCalledWith('', '');
    expect(w.find('[data-testid="tool-exposure-cost-outlook"]').text()).toBe('Schema cost ~6.4k tokens when loaded');
    expect(w.find('[data-testid="tool-exposure-cost-github"]').text()).toBe('Schema cost unknown — server not running (failed to start)');
    expect(w.find('[data-testid="tool-exposure-resolved-outlook"]').text()).toBe('Summary · harness default');
    // The select shows the user layer's own value; outlook has none.
    expect((w.find('[data-testid="tool-exposure-tier-outlook"]').element as HTMLSelectElement).value).toBe('');
    expect((w.find('[data-testid="tool-exposure-tier-fetch"]').element as HTMLSelectElement).value).toBe('summary');
  });

  it('a server tier writes the user layer, keeps the other entries and the stored TTL, and re-reads the rows', async () => {
    const { w, setSettings, schemaCosts } = setup();
    await flushPromises();
    await choose(w, 'tool-exposure-tier-outlook', 'full');
    expect(setSettings).toHaveBeenCalledTimes(1);
    const written = setSettings.mock.calls[0][0];
    expect(written.exposure).toEqual({ servers: { fetch: { tier: 'summary' }, outlook: { tier: 'full' } } });
    expect(written.activationTtlTurns).toBe(9);
    expect(written.schemaBudgetTokens).toBe(0);
    expect(schemaCosts).toHaveBeenCalledTimes(2);
  });

  it('choosing the inherit option clears the server entry', async () => {
    const { w, setSettings } = setup();
    await flushPromises();
    await choose(w, 'tool-exposure-tier-outlook', 'full');
    await choose(w, 'tool-exposure-tier-outlook', '');
    expect(setSettings.mock.calls[1][0].exposure).toEqual({ servers: { fetch: { tier: 'summary' } } });
  });

  it('an org-pinned row is read-only', async () => {
    const { w, setSettings } = setup();
    await flushPromises();
    await choose(w, 'tool-exposure-tier-fetch', '');
    expect(setSettings).not.toHaveBeenCalled();
    expect(w.find('[data-testid="tool-exposure-tier-fetch"]').attributes('disabled')).toBeDefined();
    expect(w.find('[data-testid="tool-exposure-pinned-fetch"]').text()).toContain('Set by your organisation');
  });

  it('the per-tool drawer writes a tool override under its server', async () => {
    const { w, setSettings } = setup();
    await flushPromises();
    expect(w.find('[data-testid="tool-exposure-drawer-outlook"]').exists()).toBe(false);
    await w.find('[data-testid="tool-exposure-drawer-toggle-outlook"]').trigger('click');
    await choose(w, 'tool-exposure-tool-tier-outlook-send-mail', 'off');
    expect(setSettings.mock.calls[0][0].exposure).toEqual({
      servers: { fetch: { tier: 'summary' }, outlook: { tools: { 'send-mail': 'off' } } },
    });
  });

  it('with a project chosen, tiers resolve and write that project layer, not the user layer', async () => {
    const { w, schemaCosts, getProject, setProject, setSettings } = setup({
      projectLayer: { servers: { git: { tier: 'full' } } },
    });
    await flushPromises();
    await choose(w, 'tool-exposure-scope', 'p1');
    expect(getProject).toHaveBeenCalledWith('p1');
    expect(schemaCosts).toHaveBeenLastCalledWith('', 'p1');
    // Budget and TTL are user settings only.
    expect(w.find('[data-testid="tool-exposure-budget"]').exists()).toBe(false);
    await choose(w, 'tool-exposure-tier-outlook', 'full');
    expect(setProject).toHaveBeenCalledWith('p1', {
      servers: { git: { tier: 'full' }, outlook: { tier: 'full' } },
    });
    expect(setSettings).not.toHaveBeenCalled();
  });

  it('the schema budget writes the user setting; blank means the default', async () => {
    const { w, setSettings } = setup();
    await flushPromises();
    await choose(w, 'tool-exposure-budget', '12000');
    expect(setSettings.mock.calls[0][0].schemaBudgetTokens).toBe(12_000);
    // The tier layer and the other number ride along unchanged.
    expect(setSettings.mock.calls[0][0].exposure).toEqual(SETTINGS.exposure);
    expect(setSettings.mock.calls[0][0].activationTtlTurns).toBe(9);
    await choose(w, 'tool-exposure-budget', '');
    expect(setSettings.mock.calls[1][0].schemaBudgetTokens).toBe(0);
  });

  it('the activation TTL writes the user setting', async () => {
    const { w, setSettings } = setup();
    await flushPromises();
    await choose(w, 'tool-exposure-ttl', '3');
    expect(setSettings.mock.calls[0][0].activationTtlTurns).toBe(3);
    expect(setSettings.mock.calls[0][0].exposure).toEqual(SETTINGS.exposure);
  });

  it('a server with only some tools pinned keeps its tier select; the pinned tool rows are locked', async () => {
    const mixed: ServerSchemaCost = {
      server: 'outlook', state: 'running', running: true, toolCount: 2, tokenEst: 600,
      tier: 'mixed', source: '', pinned: true, sendableTokenEst: 300,
      tools: [
        { name: 'send-mail', tokenEst: 300, tier: 'off', source: 'org_pin', activated: false, sendable: false, hot: false },
        { name: 'list-messages', tokenEst: 300, tier: 'full', source: 'user', activated: false, sendable: true, hot: false },
      ],
    };
    const { w } = setup({ costs: [mixed] });
    await flushPromises();
    expect(w.find('[data-testid="tool-exposure-tier-outlook"]').attributes('disabled')).toBeUndefined();
    expect(w.find('[data-testid="tool-exposure-pinned-outlook"]').text()).toContain('Some tools are set by your organisation');
    await w.find('[data-testid="tool-exposure-drawer-toggle-outlook"]').trigger('click');
    expect(w.find('[data-testid="tool-exposure-tool-tier-outlook-send-mail"]').attributes('disabled')).toBeDefined();
    expect(w.find('[data-testid="tool-exposure-tool-tier-outlook-list-messages"]').attributes('disabled')).toBeUndefined();
  });

  // tool-context-budget-01TCBUD01 WP07 read-only UI: a tool the org added
  // to the hot set (hot_set_extra) is locked like a pin; an org default
  // (pinned:false) stays editable — the user's layer sits above it.
  it('an org hot-set tool is read-only and says so; an org default stays editable', async () => {
    const org: ServerSchemaCost = {
      server: 'outlook', state: 'running', running: true, toolCount: 2, tokenEst: 600,
      tier: 'mixed', source: '', pinned: false, sendableTokenEst: 300,
      tools: [
        { name: 'send-mail', tokenEst: 300, tier: 'full', source: 'org_hot_set', activated: false, sendable: true, hot: false },
        { name: 'list-messages', tokenEst: 300, tier: 'summary', source: 'org_default', activated: false, sendable: false, hot: false },
      ],
    };
    const { w, setSettings } = setup({ costs: [org] });
    await flushPromises();
    expect(w.find('[data-testid="tool-exposure-pinned-outlook"]').text()).toContain('Some tools are set by your organisation');
    await w.find('[data-testid="tool-exposure-drawer-toggle-outlook"]').trigger('click');
    expect(w.find('[data-testid="tool-exposure-tool-tier-outlook-send-mail"]').attributes('disabled')).toBeDefined();
    expect(w.find('[data-testid="tool-exposure-tool-org-outlook-send-mail"]').text()).toContain('set by your organisation');
    expect(w.find('[data-testid="tool-exposure-tool-tier-outlook-list-messages"]').attributes('disabled')).toBeUndefined();
    expect(w.find('[data-testid="tool-exposure-tool-org-outlook-list-messages"]').exists()).toBe(false);
    await choose(w, 'tool-exposure-tool-tier-outlook-list-messages', 'full');
    expect(setSettings).toHaveBeenCalledTimes(1);
  });

  it('an org-set schema budget disables the budget input and names the organisation', async () => {
    const { w, setSettings } = setup({
      settings: { ...SETTINGS, org: { settings: [], schemaBudgetTokens: 16_000, bundleId: 7 } },
    });
    await flushPromises();
    const input = w.find('[data-testid="tool-exposure-budget"]');
    expect(input.attributes('disabled')).toBeDefined();
    expect(w.find('[data-testid="tool-exposure-budget-org"]').text()).toContain('set by your organisation');
    expect(setSettings).not.toHaveBeenCalled();
    // Without an org budget the input is live and there is no note.
    const plain = setup();
    await flushPromises();
    expect(plain.w.find('[data-testid="tool-exposure-budget"]').attributes('disabled')).toBeUndefined();
    expect(plain.w.find('[data-testid="tool-exposure-budget-org"]').exists()).toBe(false);
  });

  it('a built-in server tier keeps the hot set explicitly full', async () => {
    const kenaz: ServerSchemaCost = {
      server: 'kenaz', state: 'running', running: true, toolCount: 3, tokenEst: 900,
      tier: 'mixed', source: '', pinned: false, sendableTokenEst: 600,
      tools: [
        { name: 'load_tools', tokenEst: 300, tier: 'full', source: 'default', activated: false, sendable: true, hot: true },
        { name: 'read_file', tokenEst: 300, tier: 'full', source: 'default', activated: false, sendable: true, hot: true },
        { name: 'monitor', tokenEst: 300, tier: 'summary', source: 'default', activated: false, sendable: false, hot: false },
      ],
    };
    const { w, setSettings } = setup({ costs: [kenaz] });
    await flushPromises();
    await choose(w, 'tool-exposure-tier-kenaz', 'off');
    expect(setSettings.mock.calls[0][0].exposure.servers?.kenaz).toEqual({
      tier: 'off',
      tools: { load_tools: 'full', read_file: 'full' },
    });
  });

  it('writes onto the layer as stored now, not the snapshot from mount', async () => {
    const { w, setSettings, replaceStored } = setup();
    await flushPromises();
    replaceStored({ ...SETTINGS, exposure: { servers: { github: { tier: 'off' } } } });
    await choose(w, 'tool-exposure-tier-outlook', 'full');
    expect(setSettings.mock.calls[0][0].exposure).toEqual({
      servers: { github: { tier: 'off' }, outlook: { tier: 'full' } },
    });
  });

  it('a refused write shows the reason', async () => {
    const { w } = setup({
      setSettings: async () => {
        throw new Error('kenaz__load_tools is required while tools are in the summary tier');
      },
    });
    await flushPromises();
    await choose(w, 'tool-exposure-tier-outlook', 'off');
    const msg = w.find('[data-testid="tool-exposure-save-error"]').text();
    expect(msg).toMatch(/^Keep kenaz__load_tools full/);
    expect(msg).toContain('kenaz__load_tools is required');
  });
});
