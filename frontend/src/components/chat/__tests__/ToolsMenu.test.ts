/**
 * ToolsMenu (tool-context-budget-01TCBUD01 WP06, FR-K2): every control
 * writes the binding that changes the session's NEXT request — Load is
 * Sessions_LoadTools(sticky), Unload / Undo unload write the session
 * layer, Pin writes the project layer on top of what it holds — and the
 * menu re-reads the resolved state afterwards. Served mode is a boundary
 * panel that fetches nothing.
 */
import { describe, it, expect, vi } from 'vitest';
import { mount, flushPromises } from '@vue/test-utils';
import ToolsMenu from '@/components/chat/ToolsMenu.vue';
import { createFakeHarnessClient } from '@/lib/harnessClient';
import { HarnessClientKey } from '@/lib/harnessClientContext';
import type {
  ServerSchemaCost,
  ToolExposure,
  UsageComposition,
} from '@/lib/types';

function cost(o: Partial<ServerSchemaCost> & { server: string }): ServerSchemaCost {
  return {
    state: 'running',
    running: true,
    toolCount: 2,
    tokenEst: 2000,
    tier: 'summary',
    source: 'default',
    pinned: false,
    sendableTokenEst: 0,
    tools: [],
    ...o,
  };
}

const COSTS: ServerSchemaCost[] = [
  cost({ server: 'kenaz', tier: 'mixed', source: '', tokenEst: 9000, sendableTokenEst: 6000 }),
  cost({ server: 'outlook', toolCount: 94, tokenEst: 61_000 }),
  cost({ server: 'filesystem', tier: 'full', source: 'user', tokenEst: 4000, sendableTokenEst: 4000 }),
  cost({ server: 'fetch', tier: 'full', source: 'org_pin', pinned: true, tokenEst: 300, sendableTokenEst: 300 }),
  cost({ server: 'github', running: false, state: 'failed', toolCount: 0, tokenEst: 0 }),
];

function setup(opts: {
  costs?: ServerSchemaCost[];
  sessionLayer?: ToolExposure;
  projectLayer?: ToolExposure;
  projectId?: string;
  servedMode?: boolean;
  composition?: UsageComposition | null;
  notLoaded?: { name: string; reason: string }[];
} = {}) {
  const base = createFakeHarnessClient();
  const schemaCosts = vi.fn(async () => opts.costs ?? COSTS);
  const loadTools = vi.fn(async (_id: string, servers: string[]) => ({
    loaded: servers.map((s) => `${s}__x`),
    not_loaded: opts.notLoaded ?? [],
    summary: 'ok',
  }));
  const setSession = vi.fn(async () => {});
  const getProject = vi.fn(async () => opts.projectLayer ?? {});
  const setProject = vi.fn(async () => {});
  const client = {
    ...base,
    tools: { ...base.tools, schemaCosts },
    sessions: {
      ...base.sessions,
      getToolExposure: async () => ({ exposure: opts.sessionLayer ?? {}, activations: [] }),
      setToolExposure: setSession,
      loadTools,
    },
    projects: { ...base.projects, getToolExposure: getProject, setToolExposure: setProject },
  };
  const w = mount(ToolsMenu, {
    props: {
      open: true,
      sessionId: 's1',
      projectId: opts.projectId ?? '',
      composition: opts.composition ?? null,
      servedMode: opts.servedMode ?? false,
    },
    global: { provide: { [HarnessClientKey as symbol]: client } },
  });
  return { w, schemaCosts, loadTools, setSession, getProject, setProject };
}

describe('ToolsMenu', () => {
  it('lists each server with its tier and state for this session', async () => {
    const { w, schemaCosts } = setup();
    await flushPromises();
    expect(schemaCosts).toHaveBeenCalledWith('s1', '');
    expect(w.find('[data-testid="tools-menu-state-outlook"]').text()).toBe('Summary — the model can load it');
    expect(w.find('[data-testid="tools-menu-state-filesystem"]').text()).toBe('Loaded — your default');
    expect(w.find('[data-testid="tools-menu-state-fetch"]').text()).toContain('set by your organisation');
    expect(w.find('[data-testid="tools-menu-state-github"]').text()).toBe('Not running (failed)');
    // Org-pinned and stopped rows offer nothing; the built-in hot set is never unloaded.
    expect(w.find('[data-testid="tools-menu-row-fetch"]').findAll('button')).toHaveLength(0);
    expect(w.find('[data-testid="tools-menu-row-github"]').findAll('button')).toHaveLength(0);
    expect(w.find('[data-testid="tools-menu-unload-kenaz"]').exists()).toBe(false);
  });

  it('Load for this session calls Sessions_LoadTools sticky and re-reads the resolved state', async () => {
    const { w, loadTools, schemaCosts } = setup();
    await flushPromises();
    await w.find('[data-testid="tools-menu-load-outlook"]').trigger('click');
    await flushPromises();
    expect(loadTools).toHaveBeenCalledWith('s1', ['outlook'], [], true);
    expect(schemaCosts).toHaveBeenCalledTimes(2);
    expect(w.emitted('changed')).toHaveLength(1);
  });

  it('names every tool Load could not load, with the reason', async () => {
    const { w } = setup({ notLoaded: [{ name: 'outlook__delete-mail', reason: 'off — turned off for this project' }] });
    await flushPromises();
    await w.find('[data-testid="tools-menu-load-outlook"]').trigger('click');
    await flushPromises();
    expect(w.find('[data-testid="tools-menu-notice"]').text()).toBe(
      'outlook__delete-mail: off — turned off for this project',
    );
  });

  it('Unload writes the server off in the session layer, keeping its other entries', async () => {
    const sessionLayer: ToolExposure = { servers: { outlook: { tools: { 'send-mail': 'full' } } } };
    const { w, setSession } = setup({ sessionLayer });
    await flushPromises();
    await w.find('[data-testid="tools-menu-unload-filesystem"]').trigger('click');
    await flushPromises();
    expect(setSession).toHaveBeenCalledWith('s1', {
      servers: { outlook: { tools: { 'send-mail': 'full' } }, filesystem: { tier: 'off' } },
    });
  });

  it('an unloaded server offers Undo unload, which removes the session entry', async () => {
    const { w, setSession } = setup({ sessionLayer: { servers: { filesystem: { tier: 'off' } } } });
    await flushPromises();
    expect(w.find('[data-testid="tools-menu-state-filesystem"]').text()).toBe('Unloaded for this session');
    expect(w.find('[data-testid="tools-menu-load-filesystem"]').exists()).toBe(false);
    await w.find('[data-testid="tools-menu-undo-filesystem"]').trigger('click');
    await flushPromises();
    expect(setSession).toHaveBeenCalledWith('s1', {});
  });

  it('Pin for this project sets the server full on top of the stored project layer', async () => {
    const projectLayer: ToolExposure = { servers: { git: { tier: 'full' } } };
    const { w, getProject, setProject } = setup({ projectId: 'p1', projectLayer });
    await flushPromises();
    await w.find('[data-testid="tools-menu-pin-outlook"]').trigger('click');
    await flushPromises();
    expect(getProject).toHaveBeenCalledWith('p1');
    expect(setProject).toHaveBeenCalledWith('p1', {
      servers: { git: { tier: 'full' }, outlook: { tier: 'full' } },
    });
  });

  it('offers no Pin outside a project, and none for a server the project already pins', async () => {
    const loose = setup();
    await flushPromises();
    expect(loose.w.find('[data-testid="tools-menu-pin-outlook"]').exists()).toBe(false);

    const pinned = setup({ projectId: 'p1', projectLayer: { servers: { outlook: { tier: 'full' } } } });
    await flushPromises();
    expect(pinned.w.find('[data-testid="tools-menu-pin-outlook"]').exists()).toBe(false);
    expect(pinned.w.find('[data-testid="tools-menu-state-outlook"]').text()).toContain('pinned for this project');
  });

  it('meters the next request against the effective budget and shows the last request with cached tokens', async () => {
    const composition: UsageComposition = {
      system: 900, tools: 18_500, history: 4000, attachments: 0, memory: 0, cached: 12_000,
      toolsFull: 22, toolsSummary: 3, budgetWarning: 'Pinned tools exceed the schema budget by 1,200 tokens',
    };
    const { w } = setup({ composition });
    await flushPromises();
    // 6000 + 4000 + 300 sendable; fake settings report a 24,000 budget.
    expect(w.find('[data-testid="tools-menu-next-tokens"]').text()).toBe(
      `~10k of ${(24_000).toLocaleString()} budget`,
    );
    const last = w.find('[data-testid="tools-menu-last"]').text();
    expect(last).toContain(`${(18_500).toLocaleString()} tokens of tool definitions`);
    expect(last).toContain(`${(12_000).toLocaleString()} cached`);
    expect(w.find('[data-testid="tools-menu-budget-warning"]').text()).toBe(
      'Pinned tools exceed the schema budget by 1,200 tokens',
    );
  });

  it('renders no budget warning when the composition carries none', async () => {
    const { w } = setup({
      composition: { system: 1, tools: 2, history: 3, attachments: 0, memory: 0, cached: 0, toolsFull: 1 },
    });
    await flushPromises();
    expect(w.find('[data-testid="tools-menu-budget-warning"]').exists()).toBe(false);
  });

  it('served mode is a boundary panel and calls nothing', async () => {
    const { w, schemaCosts, loadTools } = setup({ servedMode: true });
    await flushPromises();
    expect(w.find('[data-testid="not-available-in-served-mode"]').exists()).toBe(true);
    expect(w.find('[data-testid="tools-menu-row-outlook"]').exists()).toBe(false);
    expect(schemaCosts).not.toHaveBeenCalled();
    expect(loadTools).not.toHaveBeenCalled();
  });
});
