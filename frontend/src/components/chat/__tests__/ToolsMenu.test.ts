/**
 * ToolsMenu (tool-context-budget-01TCBUD01 WP06, FR-K2): every control
 * writes the binding that changes the session's NEXT request — Load is
 * Sessions_LoadTools(sticky), Unload / Undo unload write the session
 * layer as stored at write time, Pin writes the project layer on top of
 * what it holds — and the menu re-reads the resolved state afterwards.
 * Served mode is a boundary panel that fetches nothing.
 */
import { describe, it, expect, vi } from 'vitest';
import { mount, flushPromises } from '@vue/test-utils';
import { nextTick } from 'vue';
import ToolsMenu from '@/components/chat/ToolsMenu.vue';
import { createFakeHarnessClient } from '@/lib/harnessClient';
import { HarnessClientKey } from '@/lib/harnessClientContext';
import type {
  ServerSchemaCost,
  ToolExposure,
  UsageComposition,
} from '@/lib/types';

function cost(o: Partial<ServerSchemaCost> & { server: string; sendableN?: number }): ServerSchemaCost {
  const { sendableN, ...rest } = o;
  const toolCount = rest.toolCount ?? 2;
  const tier = rest.tier ?? 'summary';
  const n = sendableN ?? (tier === 'full' ? toolCount : 0);
  return {
    state: 'running',
    running: true,
    tokenEst: 2000,
    source: 'default',
    pinned: false,
    sendableTokenEst: 0,
    tools: Array.from({ length: toolCount }, (_, i) => ({
      name: `t${i}`, tokenEst: 10, tier: tier === 'mixed' ? 'summary' : tier, source: 'default' as const,
      activated: false, sendable: i < n, hot: false,
    })),
    ...rest,
    toolCount,
    tier,
  };
}

const COSTS: ServerSchemaCost[] = [
  cost({ server: 'kenaz', tier: 'mixed', source: '', toolCount: 20, sendableN: 15, tokenEst: 9000, sendableTokenEst: 6000 }),
  cost({ server: 'outlook', toolCount: 94, tokenEst: 61_000 }),
  cost({ server: 'filesystem', tier: 'full', source: 'user', tokenEst: 4000, sendableTokenEst: 4000 }),
  cost({ server: 'fetch', tier: 'full', source: 'org_pin', pinned: true, toolCount: 1, tokenEst: 300, sendableTokenEst: 300 }),
  cost({ server: 'github', running: false, state: 'failed', toolCount: 0, tokenEst: 0 }),
];

function setup(opts: {
  costs?: ServerSchemaCost[];
  sessionLayer?: ToolExposure;
  projectLayer?: ToolExposure;
  projectId?: string;
  servedMode?: boolean;
  composition?: UsageComposition | null;
  windowTokens?: number;
  notLoaded?: { name: string; reason: string }[];
  schemaCosts?: (sid: string, pid: string) => Promise<ServerSchemaCost[]>;
} = {}) {
  const base = createFakeHarnessClient();
  const schemaCosts = vi.fn(opts.schemaCosts ?? (async () => opts.costs ?? COSTS));
  const loadTools = vi.fn(async (_id: string, servers: string[]) => ({
    loaded: servers.map((s) => `${s}__x`),
    loaded_by_server: {},
    not_loaded: opts.notLoaded ?? [],
    summary: 'ok',
  }));
  const getSession = vi.fn(async () => ({ exposure: opts.sessionLayer ?? {}, activations: [] }));
  const setSession = vi.fn(async () => {});
  const getProject = vi.fn(async () => opts.projectLayer ?? {});
  const setProject = vi.fn(async () => {});
  const client = {
    ...base,
    tools: { ...base.tools, schemaCosts },
    sessions: {
      ...base.sessions,
      getToolExposure: getSession,
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
      windowTokens: opts.windowTokens ?? 0,
      servedMode: opts.servedMode ?? false,
    },
    global: { provide: { [HarnessClientKey as symbol]: client } },
    attachTo: document.body,
  });
  return { w, schemaCosts, loadTools, getSession, setSession, getProject, setProject };
}

const COMPOSITION: UsageComposition = {
  system: 900, tools: 18_500, history: 4000, attachments: 0, memory: 0, cached: 12_000,
  toolsFull: 22, toolsSummary: 3,
};

describe('ToolsMenu', () => {
  it('lists each server with its tier and state for this session', async () => {
    const { w, schemaCosts } = setup();
    await flushPromises();
    expect(schemaCosts).toHaveBeenCalledWith('s1', '');
    expect(w.find('[data-testid="tools-menu-state-outlook"]').text()).toBe('Summary — the model can load it');
    expect(w.find('[data-testid="tools-menu-state-filesystem"]').text()).toBe('Loaded — your default');
    expect(w.find('[data-testid="tools-menu-state-fetch"]').text()).toContain('set by your organisation');
    expect(w.find('[data-testid="tools-menu-state-github"]').text()).toBe('Not running (failed to start)');
    expect(w.find('[data-testid="tools-menu-state-kenaz"]').text()).toBe('Core tools always loaded — 15 of 20 loaded');
    // Org-pinned and stopped rows offer nothing; the built-in server is never unloaded or pinned.
    expect(w.find('[data-testid="tools-menu-row-fetch"]').findAll('button')).toHaveLength(0);
    expect(w.find('[data-testid="tools-menu-row-github"]').findAll('button')).toHaveLength(0);
    expect(w.find('[data-testid="tools-menu-unload-kenaz"]').exists()).toBe(false);
    expect(w.find('[data-testid="tools-menu-load-kenaz"]').exists()).toBe(true);
    w.unmount();
  });

  it('a partly loaded server shows N of M and offers both Load and Unload', async () => {
    const { w } = setup({ costs: [cost({ server: 'outlook', toolCount: 94, sendableN: 3, sendableTokenEst: 900 })] });
    await flushPromises();
    expect(w.find('[data-testid="tools-menu-state-outlook"]').text()).toBe('3 of 94 loaded');
    expect(w.find('[data-testid="tools-menu-load-outlook"]').exists()).toBe(true);
    expect(w.find('[data-testid="tools-menu-unload-outlook"]').exists()).toBe(true);
    w.unmount();
  });

  it('Load for this session calls Sessions_LoadTools sticky and re-reads the resolved state', async () => {
    const { w, loadTools, schemaCosts } = setup();
    await flushPromises();
    await w.find('[data-testid="tools-menu-load-outlook"]').trigger('click');
    await flushPromises();
    expect(loadTools).toHaveBeenCalledWith('s1', ['outlook'], [], true);
    expect(schemaCosts).toHaveBeenCalledTimes(2);
    w.unmount();
  });

  it('names every tool Load could not load, with the reason', async () => {
    const { w } = setup({ notLoaded: [{ name: 'outlook__delete-mail', reason: 'off — turned off for this project' }] });
    await flushPromises();
    await w.find('[data-testid="tools-menu-load-outlook"]').trigger('click');
    await flushPromises();
    expect(w.find('[data-testid="tools-menu-notice"]').text()).toBe(
      'outlook__delete-mail: off — turned off for this project',
    );
    w.unmount();
  });

  it('Unload writes the server off on the session layer as stored at write time', async () => {
    const sessionLayer: ToolExposure = { servers: { outlook: { tools: { 'send-mail': 'full' } } } };
    const { w, getSession, setSession } = setup({ sessionLayer });
    await flushPromises();
    const readsBefore = getSession.mock.calls.length;
    await w.find('[data-testid="tools-menu-unload-filesystem"]').trigger('click');
    await flushPromises();
    expect(getSession.mock.calls.length).toBeGreaterThan(readsBefore);
    expect(setSession).toHaveBeenCalledWith('s1', {
      servers: { outlook: { tools: { 'send-mail': 'full' } }, filesystem: { tier: 'off' } },
    });
    w.unmount();
  });

  it('an unloaded server offers Undo unload, which removes the session entry', async () => {
    const { w, setSession } = setup({ sessionLayer: { servers: { filesystem: { tier: 'off' } } } });
    await flushPromises();
    expect(w.find('[data-testid="tools-menu-state-filesystem"]').text()).toBe('Unloaded for this session');
    expect(w.find('[data-testid="tools-menu-load-filesystem"]').exists()).toBe(false);
    await w.find('[data-testid="tools-menu-undo-filesystem"]').trigger('click');
    await flushPromises();
    expect(setSession).toHaveBeenCalledWith('s1', {});
    w.unmount();
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
    w.unmount();
  });

  it('offers no Pin outside a project, and none for a server the project already pins', async () => {
    const loose = setup();
    await flushPromises();
    expect(loose.w.find('[data-testid="tools-menu-pin-outlook"]').exists()).toBe(false);
    loose.w.unmount();

    const pinned = setup({ projectId: 'p1', projectLayer: { servers: { outlook: { tier: 'full' } } } });
    await flushPromises();
    expect(pinned.w.find('[data-testid="tools-menu-pin-outlook"]').exists()).toBe(false);
    expect(pinned.w.find('[data-testid="tools-menu-state-outlook"]').text()).toContain('pinned for this project');
    pinned.w.unmount();
  });

  it('the guard refusal is restated as the fix', async () => {
    const { w, setSession } = setup();
    setSession.mockRejectedValueOnce(new Error('toolexposure: kenaz__load_tools is required while tools are in the summary tier'));
    await flushPromises();
    await w.find('[data-testid="tools-menu-unload-filesystem"]').trigger('click');
    await flushPromises();
    expect(w.find('[data-testid="tools-menu-error"]').text()).toMatch(/^Keep kenaz__load_tools full/);
    w.unmount();
  });

  it('meters the next request (before budget) against the budget the last call reported', async () => {
    const { w } = setup({ composition: { ...COMPOSITION, schemaBudget: 20_000 }, windowTokens: 100_000 });
    await flushPromises();
    // 6000 + 4000 + 300 sendable.
    expect(w.find('[data-testid="tools-menu-next-tokens"]').text()).toBe('~10k of 20k budget');
    expect(w.find('[data-testid="tools-menu-meter"]').text()).toContain('before budget');
    const last = w.find('[data-testid="tools-menu-last"]').text();
    expect(last).toContain(`${(18_500).toLocaleString()} tokens of tool definitions`);
    expect(last).toContain(`${(12_000).toLocaleString()} cached`);
    w.unmount();
  });

  it('without a reported budget, caps the effective setting at 15% of the model window', async () => {
    const { w } = setup({ composition: COMPOSITION, windowTokens: 100_000 });
    await flushPromises();
    // Fake settings report 24,000; 15% of 100,000 is 15,000.
    expect(w.find('[data-testid="tools-menu-next-tokens"]').text()).toBe('~10k of 15k budget');
    w.unmount();
  });

  it('renders WP04 budget outcomes only when they are non-zero', async () => {
    const { w } = setup({
      composition: { ...COMPOSITION, pinnedOverBudgetBy: 1200, toolsEvicted: 2, hotOverBudgetBy: 300 },
    });
    await flushPromises();
    expect(w.find('[data-testid="tools-menu-pinned-over"]').text()).toBe(
      `Pinned tools exceed the schema budget by ${(1200).toLocaleString()} tokens.`,
    );
    expect(w.find('[data-testid="tools-menu-pinned-over"]').attributes('role')).toBe('status');
    expect(w.find('[data-testid="tools-menu-evicted"]').text()).toBe(
      '2 loaded tools left out of the last request to fit the budget.',
    );
    expect(w.find('[data-testid="tools-menu-hot-over"]').text()).toContain(
      "This model's window is too small for the core tools (over by 300",
    );
    w.unmount();

    const quiet = setup({ composition: { ...COMPOSITION, pinnedOverBudgetBy: 0, toolsEvicted: 0, hotOverBudgetBy: 0 } });
    await flushPromises();
    for (const id of ['tools-menu-pinned-over', 'tools-menu-evicted', 'tools-menu-hot-over']) {
      expect(quiet.w.find(`[data-testid="${id}"]`).exists()).toBe(false);
    }
    quiet.w.unmount();
  });

  it('a new composition re-reads an open menu', async () => {
    const { w, schemaCosts } = setup({ composition: COMPOSITION });
    await flushPromises();
    expect(schemaCosts).toHaveBeenCalledTimes(1);
    await w.setProps({ composition: { ...COMPOSITION, tools: 30_000 } });
    await flushPromises();
    expect(schemaCosts).toHaveBeenCalledTimes(2);
    w.unmount();
  });

  it("a stale response cannot overwrite another session's rows", async () => {
    let releaseFirst!: (v: ServerSchemaCost[]) => void;
    const { w } = setup({
      schemaCosts: (sid) =>
        sid === 's1'
          ? new Promise<ServerSchemaCost[]>((r) => { releaseFirst = r; })
          : Promise.resolve([cost({ server: 'fetch' })]),
    });
    await w.setProps({ sessionId: 's2' });
    await flushPromises();
    releaseFirst([cost({ server: 'outlook' })]);
    await flushPromises();
    expect(w.find('[data-testid="tools-menu-row-fetch"]').exists()).toBe(true);
    expect(w.find('[data-testid="tools-menu-row-outlook"]').exists()).toBe(false);
    w.unmount();
  });

  it('is a labelled dialog: focus moves in on open, Escape closes and returns focus, outside click closes', async () => {
    const { w } = setup();
    await flushPromises();
    await nextTick();
    const toggle = w.find('[data-testid="tools-menu-toggle"]');
    const panel = w.find('[data-testid="tools-menu-panel"]');
    expect(toggle.attributes('aria-haspopup')).toBe('dialog');
    expect(toggle.attributes('aria-controls')).toBe(panel.attributes('id'));
    expect(panel.element.contains(document.activeElement)).toBe(true);
    expect(document.activeElement?.tagName).toBe('BUTTON');

    await panel.trigger('keydown', { key: 'Escape' });
    await nextTick();
    expect(w.emitted('update:open')?.at(-1)).toEqual([false]);
    expect(document.activeElement).toBe(toggle.element);

    document.body.dispatchEvent(new MouseEvent('mousedown', { bubbles: true }));
    expect(w.emitted('update:open')?.filter((e) => e[0] === false)).toHaveLength(2);
    // A click inside the menu does not close it.
    panel.element.dispatchEvent(new MouseEvent('mousedown', { bubbles: true }));
    expect(w.emitted('update:open')?.filter((e) => e[0] === false)).toHaveLength(2);
    w.unmount();
  });

  it('served mode is a boundary panel and calls nothing', async () => {
    const { w, schemaCosts, loadTools } = setup({ servedMode: true });
    await flushPromises();
    expect(w.find('[data-testid="not-available-in-served-mode"]').exists()).toBe(true);
    expect(w.find('[data-testid="tools-menu-row-outlook"]').exists()).toBe(false);
    expect(schemaCosts).not.toHaveBeenCalled();
    expect(loadTools).not.toHaveBeenCalled();
    w.unmount();
  });
});
