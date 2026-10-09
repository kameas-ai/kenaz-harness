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
};

const COSTS: ServerSchemaCost[] = [
  {
    server: 'outlook', state: 'running', running: true, toolCount: 2, tokenEst: 6400,
    tier: 'summary', source: 'default', pinned: false, sendableTokenEst: 0,
    tools: [
      { name: 'list-messages', tokenEst: 3000, tier: 'summary', source: 'default', activated: false, sendable: false },
      { name: 'send-mail', tokenEst: 3400, tier: 'summary', source: 'default', activated: false, sendable: false },
    ],
  },
  {
    server: 'fetch', state: 'running', running: true, toolCount: 1, tokenEst: 300,
    tier: 'full', source: 'org_pin', pinned: true, sendableTokenEst: 300,
    tools: [{ name: 'fetch', tokenEst: 300, tier: 'full', source: 'org_pin', activated: false, sendable: true }],
  },
  {
    server: 'github', state: 'failed', running: false, toolCount: 0, tokenEst: 0,
    tier: 'summary', source: 'default', pinned: false, sendableTokenEst: 0, tools: [],
  },
];

function setup(opts: { setSettings?: () => Promise<void>; projectLayer?: ToolExposure } = {}) {
  const base = createFakeHarnessClient();
  const schemaCosts = vi.fn(async () => COSTS);
  let stored = { ...SETTINGS };
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
  return { w, schemaCosts, setSettings, getProject, setProject };
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
    expect(w.find('[data-testid="tool-exposure-cost-github"]').text()).toBe('Schema cost unknown — server failed');
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
    await choose(w, 'tool-exposure-budget', '');
    expect(setSettings.mock.calls[1][0].schemaBudgetTokens).toBe(0);
  });

  it('the activation TTL writes the user setting', async () => {
    const { w, setSettings } = setup();
    await flushPromises();
    await choose(w, 'tool-exposure-ttl', '3');
    expect(setSettings.mock.calls[0][0].activationTtlTurns).toBe(3);
  });

  it('a refused write shows the reason', async () => {
    const { w } = setup({
      setSettings: async () => {
        throw new Error('kenaz__load_tools is required while tools are in the summary tier');
      },
    });
    await flushPromises();
    await choose(w, 'tool-exposure-tier-outlook', 'off');
    expect(w.find('[data-testid="tool-exposure-save-error"]').text()).toContain('kenaz__load_tools is required');
  });
});
