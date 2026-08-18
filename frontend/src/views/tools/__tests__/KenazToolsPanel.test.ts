import { describe, it, expect, vi, afterEach } from 'vitest';
import { mount, flushPromises } from '@vue/test-utils';
import { defineComponent } from 'vue';
import { createMemoryHistory, createRouter } from 'vue-router';
import KenazToolsPanel from '@/views/tools/KenazToolsPanel.vue';
import {
  createFakeHarnessClient,
  type HarnessClient,
} from '@/lib/harnessClient';
import { HarnessClientKey } from '@/lib/harnessClientContext';
import type {
  Recipe,
  RecipeListing,
  RecipeStatus,
} from '@/lib/types';

function makeRecipe(
  id: string,
  overrides: Partial<Recipe> = {},
): Recipe {
  return {
    id,
    displayName: id,
    description: `Recipe ${id}`,
    category: 'search',
    envKeys: [
      {
        name: `${id.toUpperCase()}_API_KEY`,
        display: `${id} API Key`,
        docsUrl: `https://example.com/${id}/keys`,
        required: true,
      },
    ],
    capabilities: {
      tools: true,
      resources: false,
      prompts: false,
      sampling: false,
    },
    docsUrl: `https://example.com/${id}`,
    ...overrides,
  };
}

function makeStatus(
  id: string,
  overrides: Partial<RecipeStatus> = {},
): RecipeStatus {
  return {
    id,
    enabled: false,
    state: 'stopped',
    restartAttempts: 0,
    keysPresent: false,
    pid: 0,
    toolCount: 0,
    resourceCount: 0,
    promptCount: 0,
    ...overrides,
  };
}

function makeListing(
  recipe: Recipe,
  overrides: Partial<RecipeListing> = {},
): RecipeListing {
  return {
    recipe,
    enabled: false,
    keysPresent: false,
    status: makeStatus(recipe.id),
    ...overrides,
  };
}

interface ToolsSpy {
  list: ReturnType<typeof vi.fn>;
  install: ReturnType<typeof vi.fn>;
  uninstall: ReturnType<typeof vi.fn>;
  forgetKey: ReturnType<typeof vi.fn>;
  status: ReturnType<typeof vi.fn>;
}

interface MountResult {
  client: HarnessClient;
  spies: ToolsSpy;
  router: ReturnType<typeof createRouter>;
}

function makeClient(
  initialList: RecipeListing[],
  statusOverrides: Map<string, RecipeStatus[]> = new Map(),
): MountResult {
  const list = vi.fn(async () => initialList.map((l) => ({ ...l })));
  const install = vi.fn(async (id: string) =>
    makeStatus(id, { enabled: true, state: 'starting', keysPresent: true }),
  );
  const uninstall = vi.fn(async () => undefined);
  const forgetKey = vi.fn(async () => undefined);
  const config = vi.fn(async (_id: string): Promise<Record<string, unknown>> => ({}));
  const status = vi.fn(async (id: string): Promise<RecipeStatus> => {
    const seq = statusOverrides.get(id);
    if (seq && seq.length > 0) {
      return seq.shift() ?? makeStatus(id);
    }
    return makeStatus(id, {
      enabled: true,
      state: 'running',
      keysPresent: true,
    });
  });

  const client = createFakeHarnessClient({
    tools: {
      recipes: {
        list,
        install,
        uninstall,
        forgetKey,
        status,
        config,
      } as any,
    } as any,
  });

  const router = createRouter({
    history: createMemoryHistory(),
    routes: [{ path: '/', component: { render: () => null } }],
  });

  return {
    client,
    spies: { list, install, uninstall, forgetKey, status },
    router,
  };
}

const Stub = defineComponent({
  props: {
    open: Boolean,
    recipe: { type: Object, default: null },
    install: { type: Function, default: null },
  },
  emits: ['close', 'installed'],
  render() {
    return null;
  },
});

async function mountPanel(setup: MountResult) {
  return mount(KenazToolsPanel, {
    global: {
      provide: { [HarnessClientKey as symbol]: setup.client },
      plugins: [setup.router],
      stubs: {
        RecipeKeyPromptModal: Stub,
      },
    },
  });
}

afterEach(() => {
  vi.useRealTimers();
});

describe('KenazToolsPanel — recipes section', () => {
  it('renders the existing Memory row plus a row per enabled recipe (catalog rows hidden)', async () => {
    // The panel intentionally only surfaces installed (enabled) recipes;
    // catalog browsing lives in the Add MCP Server modal. So a clean-
    // install (enabled=false) row should NOT render in the main list.
    const recipes = [
      makeListing(makeRecipe('brave-search'), {
        enabled: false,
        keysPresent: false,
      }),
      makeListing(
        makeRecipe('filesystem', { category: 'filesystem' }),
        { enabled: true, keysPresent: true, status: makeStatus('filesystem', { enabled: true, state: 'running', keysPresent: true }) },
      ),
    ];
    const setup = makeClient(recipes);
    const w = await mountPanel(setup);
    await flushPromises();

    expect(w.text()).toContain('Long-term memory');
    expect(w.text()).toContain('Connected MCP recipes');
    // Disabled catalog row is hidden from the main panel.
    expect(w.find('[data-testid=recipe-row-brave-search]').exists()).toBe(false);
    // Enabled (installed) row renders with its current state pill.
    expect(w.find('[data-testid=recipe-row-filesystem]').exists()).toBe(true);
    // HealthPill renders "●running" — check contain rather than exact match.
    expect(w.find('[data-testid=recipe-state-filesystem]').text()).toContain(
      'running',
    );
  });

  it('edit-configuration flow on an enabled recipe forwards env+config to install via the modal', async () => {
    // The panel hides catalog (disabled) rows; the equivalent install
    // path on the panel itself is the per-row "Edit configuration"
    // button on an already-enabled recipe with configOptions. Trigger
    // it, capture the modal's install handler, and verify it forwards
    // through tools.recipes.install with the expected args.
    const fsRecipeWithConfig = makeRecipe('filesystem', {
      category: 'filesystem',
      envKeys: [],
      configOptions: [
        {
          name: 'allowed_directories',
          display: 'Allowed directories',
          kind: 'directory_list',
          required: true,
          description: 'Allowed dirs.',
          default: ['${DATA_DIR}/agent-workspace'],
        },
      ],
    });
    const recipes = [
      makeListing(fsRecipeWithConfig, {
        enabled: true,
        keysPresent: true,
        status: makeStatus('filesystem', {
          enabled: true,
          state: 'running',
          keysPresent: true,
        }),
      }),
    ];
    const setup = makeClient(recipes);
    const w = await mountPanel(setup);
    await flushPromises();

    await w
      .get('[data-testid=recipe-edit-config-filesystem]')
      .trigger('click');
    await flushPromises();

    const modal = w.findComponent(Stub);
    expect(modal.exists()).toBe(true);
    expect(modal.props('open')).toBe(true);
    expect((modal.props('recipe') as Recipe).id).toBe('filesystem');

    const install = modal.props('install') as (
      id: string,
      env: Record<string, string>,
      config: Record<string, unknown>,
    ) => Promise<RecipeStatus>;
    await install('filesystem', {}, { allowed_directories: ['/tmp/x'] });
    await flushPromises();

    expect(setup.spies.install).toHaveBeenCalledTimes(1);
    const [calledId, calledEnv, calledConfig] =
      setup.spies.install.mock.calls[0];
    expect(calledId).toBe('filesystem');
    expect(calledEnv).toEqual({});
    expect(calledConfig).toEqual({ allowed_directories: ['/tmp/x'] });
  });

  it('recipes-empty placeholder renders when no recipes are enabled (clean install)', async () => {
    // A clean install (no enabled recipes) shows the placeholder + the
    // Add MCP Server CTA rather than any per-row toggles. This replaces
    // the legacy "toggle-on opens modal" path which now lives in the
    // Add MCP Server flow.
    const recipes = [
      makeListing(makeRecipe('brave-search'), {
        enabled: false,
        keysPresent: false,
        status: makeStatus('brave-search', { keysPresent: false }),
      }),
    ];
    const setup = makeClient(recipes);
    const w = await mountPanel(setup);
    await flushPromises();

    expect(w.find('[data-testid=recipes-empty]').exists()).toBe(true);
    expect(w.find('[data-testid=add-mcp-server-btn]').exists()).toBe(true);
    expect(setup.spies.install).not.toHaveBeenCalled();
    // No per-row toggle for a non-enabled catalog entry.
    expect(w.find('[data-testid=recipe-toggle-brave-search]').exists()).toBe(
      false,
    );
  });

  it('toggle-off calls uninstall', async () => {
    const recipes = [
      makeListing(makeRecipe('brave-search'), {
        enabled: true,
        keysPresent: true,
        status: makeStatus('brave-search', {
          enabled: true,
          state: 'running',
          keysPresent: true,
        }),
      }),
    ];
    const setup = makeClient(recipes);
    const w = await mountPanel(setup);
    await flushPromises();

    const toggle = w.get('[data-testid=recipe-toggle-brave-search]');
    await toggle.setValue(false);
    await flushPromises();

    expect(setup.spies.uninstall).toHaveBeenCalledTimes(1);
    expect(setup.spies.uninstall).toHaveBeenCalledWith('brave-search');
  });

  it('forget-key button calls forgetKey(id, name)', async () => {
    const recipes = [
      makeListing(makeRecipe('brave-search'), {
        enabled: true,
        keysPresent: true,
        status: makeStatus('brave-search', {
          enabled: true,
          state: 'running',
          keysPresent: true,
        }),
      }),
    ];
    const setup = makeClient(recipes);
    const w = await mountPanel(setup);
    await flushPromises();

    await w
      .get('[data-testid=recipe-forget-brave-search-BRAVE-SEARCH_API_KEY]')
      .trigger('click');
    await flushPromises();

    expect(setup.spies.forgetKey).toHaveBeenCalledWith(
      'brave-search',
      'BRAVE-SEARCH_API_KEY',
    );
  });

  it('polls recipeStatus(id) at 1 Hz while a row is starting and stops once terminal', async () => {
    vi.useFakeTimers();
    // First poll keeps it in `starting`, second lands `running`.
    const seq = [
      makeStatus('brave-search', {
        enabled: true,
        state: 'starting',
        keysPresent: true,
      }),
      makeStatus('brave-search', {
        enabled: true,
        state: 'running',
        keysPresent: true,
      }),
    ];
    const recipes = [
      makeListing(makeRecipe('brave-search'), {
        enabled: true,
        keysPresent: true,
        status: makeStatus('brave-search', {
          enabled: true,
          state: 'starting',
          keysPresent: true,
        }),
      }),
    ];
    const setup = makeClient(
      recipes,
      new Map([['brave-search', seq]]),
    );
    const w = await mountPanel(setup);
    await flushPromises();

    expect(setup.spies.status).not.toHaveBeenCalled();

    await vi.advanceTimersByTimeAsync(1000);
    expect(setup.spies.status).toHaveBeenCalledTimes(1);

    await vi.advanceTimersByTimeAsync(1000);
    // Second tick should land `running` (terminal) and stop the timer.
    expect(setup.spies.status).toHaveBeenCalledTimes(2);

    // Further timer ticks must not hit status again.
    await vi.advanceTimersByTimeAsync(3000);
    expect(setup.spies.status).toHaveBeenCalledTimes(2);

    w.unmount();
  });

  it('renders the warming indicator only after 4 s in starting state', async () => {
    vi.useFakeTimers();
    vi.setSystemTime(new Date(2026, 0, 1, 12, 0, 0));
    // Mount an enabled recipe with configOptions so the panel renders
    // its row + the "Edit configuration" affordance. Trigger that
    // affordance, capture the modal's install handler, and call it —
    // this seeds startedAt[id] in the same way the legacy toggle-on
    // path used to. Then advance the clock past the cold-spawn
    // threshold and verify the warming indicator surfaces.
    const recipeWithConfig = makeRecipe('brave-search', {
      envKeys: [],
      configOptions: [
        {
          name: 'mode',
          display: 'Mode',
          kind: 'string',
          required: false,
          description: 'Optional flag.',
          default: 'fast',
        },
      ],
    });
    const recipes = [
      makeListing(recipeWithConfig, {
        enabled: true,
        keysPresent: true,
        status: makeStatus('brave-search', {
          enabled: true,
          state: 'starting',
          keysPresent: true,
        }),
      }),
    ];
    const seq = [
      makeStatus('brave-search', {
        enabled: true,
        state: 'starting',
        keysPresent: true,
      }),
      makeStatus('brave-search', {
        enabled: true,
        state: 'starting',
        keysPresent: true,
      }),
      makeStatus('brave-search', {
        enabled: true,
        state: 'starting',
        keysPresent: true,
      }),
      makeStatus('brave-search', {
        enabled: true,
        state: 'starting',
        keysPresent: true,
      }),
      makeStatus('brave-search', {
        enabled: true,
        state: 'starting',
        keysPresent: true,
      }),
    ];
    const setup = makeClient(
      recipes,
      new Map([['brave-search', seq]]),
    );
    setup.spies.install.mockImplementation(async () =>
      makeStatus('brave-search', {
        enabled: true,
        state: 'starting',
        keysPresent: true,
      }),
    );

    const w = await mountPanel(setup);
    await flushPromises();

    // Open the modal via Edit configuration, then call its install
    // handler — this routes through tools.install and seeds startedAt.
    await w
      .get('[data-testid=recipe-edit-config-brave-search]')
      .trigger('click');
    await flushPromises();
    const modal = w.findComponent(Stub);
    expect(modal.exists()).toBe(true);
    const installHandler = modal.props('install') as (
      id: string,
      env: Record<string, string>,
      config: Record<string, unknown>,
    ) => Promise<RecipeStatus>;
    await installHandler('brave-search', {}, { mode: 'fast' });
    await flushPromises();

    // Before the cold-spawn threshold elapses, no warming hint.
    expect(
      w.find('[data-testid=recipe-warming-brave-search]').exists(),
    ).toBe(false);

    // Step time forward 5 s; the now-tick interval inside the panel
    // refreshes the elapsed-time computation.
    await vi.advanceTimersByTimeAsync(5000);
    await flushPromises();

    expect(
      w.find('[data-testid=recipe-warming-brave-search]').exists(),
    ).toBe(true);

    w.unmount();
  });

  it('uses only design tokens — no raw hex / rgba in markup', async () => {
    const recipes = [
      makeListing(makeRecipe('brave-search'), {
        enabled: false,
        keysPresent: false,
      }),
    ];
    const setup = makeClient(recipes);
    const w = await mountPanel(setup);
    await flushPromises();
    const html = w.html();
    expect(html).not.toMatch(/#[0-9a-fA-F]{3,8}\b/);
    expect(html).not.toMatch(/rgba?\s*\(/i);
  });

  // ── Filesystem-specific UX (WP03) ──────────────────────────────────

  function fsRecipe(): Recipe {
    return {
      id: 'filesystem',
      displayName: 'Filesystem',
      description: 'Read and write files in a sandboxed workspace.',
      category: 'filesystem',
      envKeys: [],
      capabilities: {
        tools: true,
        resources: false,
        prompts: false,
        sampling: false,
      },
      argsTemplate: ['${ALLOWED_DIRS}'],
      configOptions: [
        {
          name: 'allowed_directories',
          display: 'Allowed directories',
          kind: 'directory_list',
          default: ['${DATA_DIR}/agent-workspace'],
          required: true,
          description: 'Directories the model can read and write.',
        },
      ],
    };
  }

  it('filesystem-category running row shows "Open workspace" button', async () => {
    const recipes = [
      makeListing(fsRecipe(), {
        enabled: true,
        keysPresent: true,
        status: makeStatus('filesystem', {
          enabled: true,
          state: 'running',
          keysPresent: true,
        }),
      }),
    ];
    const setup = makeClient(recipes);
    setup.spies.status.mockResolvedValue(
      makeStatus('filesystem', {
        enabled: true,
        state: 'running',
        keysPresent: true,
      }),
    );
    // Pre-load the persisted config so the panel can resolve the path.
    const config = vi.fn(
      async (_id: string): Promise<Record<string, unknown>> => ({
        allowed_directories: ['/Users/me/.harness/agent-workspace'],
      }),
    );
    setup.client.tools.recipes.config = config;

    const w = await mountPanel(setup);
    await flushPromises();
    expect(
      w.find('[data-testid=recipe-open-workspace-filesystem]').exists(),
    ).toBe(true);
  });

  it('filesystem rows hide "Open workspace" while not running', async () => {
    const recipes = [
      makeListing(fsRecipe(), {
        enabled: true,
        keysPresent: true,
        status: makeStatus('filesystem', {
          enabled: true,
          state: 'starting',
          keysPresent: true,
        }),
      }),
    ];
    const setup = makeClient(recipes);
    setup.client.tools.recipes.config = vi.fn(async () => ({
      allowed_directories: ['/tmp/x'],
    }));
    const w = await mountPanel(setup);
    await flushPromises();
    expect(
      w.find('[data-testid=recipe-open-workspace-filesystem]').exists(),
    ).toBe(false);
  });

  it('clicking "Open workspace" calls shell.openInOSBrowser with the resolved path', async () => {
    const recipes = [
      makeListing(fsRecipe(), {
        enabled: true,
        keysPresent: true,
        status: makeStatus('filesystem', {
          enabled: true,
          state: 'running',
          keysPresent: true,
        }),
      }),
    ];
    const setup = makeClient(recipes);
    setup.client.tools.recipes.config = vi.fn(async () => ({
      allowed_directories: ['/Users/me/.harness/agent-workspace', '/tmp/extra'],
    }));
    const openInOSBrowser = vi.fn(async () => undefined);
    setup.client.shell = {
      openInOSBrowser,
      pathComplete: vi.fn(async () => []),
      readFile: vi.fn(async () => ({ dataBase64: '', mediaType: '' })),
    } as any;

    const w = await mountPanel(setup);
    await flushPromises();
    // Allow the watcher-triggered config load to settle.
    await flushPromises();

    await w
      .get('[data-testid=recipe-open-workspace-filesystem]')
      .trigger('click');
    await flushPromises();

    expect(openInOSBrowser).toHaveBeenCalledTimes(1);
    expect(openInOSBrowser).toHaveBeenCalledWith(
      '/Users/me/.harness/agent-workspace',
    );
  });

  it('edit-configuration on an enabled filesystem row opens the modal pre-seeded with the recipe (config flow)', async () => {
    // The legacy "toggle a stopped filesystem row" affordance is gone —
    // catalog browsing/install lives in the Add MCP Server modal. The
    // equivalent "open the config modal for filesystem" flow remains
    // reachable via the per-row Edit configuration button on enabled
    // rows. The contract under test is unchanged: opening the modal
    // does not call install on its own.
    const recipes = [
      makeListing(fsRecipe(), {
        enabled: true,
        keysPresent: true,
        status: makeStatus('filesystem', {
          enabled: true,
          state: 'running',
          keysPresent: true,
        }),
      }),
    ];
    const setup = makeClient(recipes);
    const w = await mountPanel(setup);
    await flushPromises();

    await w
      .get('[data-testid=recipe-edit-config-filesystem]')
      .trigger('click');
    await flushPromises();

    const modal = w.findComponent(Stub);
    expect(modal.exists()).toBe(true);
    expect(modal.props('open')).toBe(true);
    expect((modal.props('recipe') as Recipe).id).toBe('filesystem');
    // Opening the modal does not call install — install only fires
    // when the modal commits.
    expect(setup.spies.install).not.toHaveBeenCalled();
  });
});

// ── mcp-connector-lifecycle-01PMMC01 WP06 ────────────────────────────────
//
// WP02's CustomRecipeAuthoringKey gate is retired now that
// MCP_SaveCustomRecipe is real (see docs/unwired-ledger.md's 2026-08-18
// "CLOSED" entry). The row Edit button is unconditionally reachable
// again, same as before WP02, now backed by a working save path.

describe('KenazToolsPanel — row Edit button (custom-recipe authoring, post-WP06)', () => {
  function oneEnabledRecipe() {
    return [
      makeListing(makeRecipe('brave-search'), {
        enabled: true,
        keysPresent: true,
        status: makeStatus('brave-search', {
          enabled: true,
          state: 'running',
          keysPresent: true,
        }),
      }),
    ];
  }

  it('renders unconditionally for an enabled recipe row', async () => {
    const setup = makeClient(oneEnabledRecipe());
    const w = await mountPanel(setup);
    await flushPromises();

    expect(
      w.find('[data-testid="recipe-edit-btn-brave-search"]').exists(),
    ).toBe(true);
  });

  it('clicking Edit opens AddMCPServerModal on the recipe', async () => {
    const setup = makeClient(oneEnabledRecipe());
    const w = await mountPanel(setup);
    await flushPromises();

    await w.find('[data-testid="recipe-edit-btn-brave-search"]').trigger('click');
    await flushPromises();

    expect(w.find('[data-testid="add-mcp-modal"]').exists()).toBe(true);
  });
});

// ── builtin-filesystem-tools-01KR3N4P WP06 — FS toggle UI ──────────────

describe('KenazToolsPanel — builtin filesystem tools toggles', () => {
  it('renders the fs-read and fs-write toggle rows', async () => {
    const setup = makeClient([]);
    const w = await mountPanel(setup);
    await flushPromises();

    expect(w.find('[data-testid=fs-read-tool-row]').exists()).toBe(true);
    expect(w.find('[data-testid=fs-write-tool-row]').exists()).toBe(true);
  });

  it('fs-read toggle is unchecked by default (FSReadEnabled defaults false)', async () => {
    const setup = makeClient([]);
    const w = await mountPanel(setup);
    await flushPromises();

    const toggle = w.find<HTMLInputElement>('[data-testid=fs-read-toggle]');
    expect(toggle.exists()).toBe(true);
    expect((toggle.element as HTMLInputElement).checked).toBe(false);
  });

  it('fs-write toggle is unchecked by default (FSWriteEnabled defaults false)', async () => {
    const setup = makeClient([]);
    const w = await mountPanel(setup);
    await flushPromises();

    const toggle = w.find<HTMLInputElement>('[data-testid=fs-write-toggle]');
    expect(toggle.exists()).toBe(true);
    expect((toggle.element as HTMLInputElement).checked).toBe(false);
  });

  it('toggling fs-read calls settings.setFSReadEnabled with the new value', async () => {
    const setFSReadEnabled = vi.fn(async () => undefined);
    const setup = makeClient([]);
    setup.client.settings.setFSReadEnabled = setFSReadEnabled;

    const w = await mountPanel(setup);
    await flushPromises();

    const toggle = w.find('[data-testid=fs-read-toggle]');
    // Trigger the change event (the handler reads event.target.checked).
    const el = toggle.element as HTMLInputElement;
    el.checked = true;
    await toggle.trigger('change');
    await flushPromises();

    expect(setFSReadEnabled).toHaveBeenCalledTimes(1);
    expect(setFSReadEnabled).toHaveBeenCalledWith(true);
  });

  it('toggling fs-write calls settings.setFSWriteEnabled with the new value', async () => {
    const setFSWriteEnabled = vi.fn(async () => undefined);
    const setup = makeClient([]);
    setup.client.settings.setFSWriteEnabled = setFSWriteEnabled;

    const w = await mountPanel(setup);
    await flushPromises();

    const toggle = w.find('[data-testid=fs-write-toggle]');
    const el = toggle.element as HTMLInputElement;
    el.checked = true;
    await toggle.trigger('change');
    await flushPromises();

    expect(setFSWriteEnabled).toHaveBeenCalledTimes(1);
    expect(setFSWriteEnabled).toHaveBeenCalledWith(true);
  });

  it('fs-read toggle reflects the value returned by settings.getFSReadEnabled', async () => {
    const setup = makeClient([]);
    setup.client.settings.getFSReadEnabled = async () => true;

    const w = await mountPanel(setup);
    await flushPromises();

    const toggle = w.find<HTMLInputElement>('[data-testid=fs-read-toggle]');
    expect((toggle.element as HTMLInputElement).checked).toBe(true);
  });

  it('fs-write toggle reflects the value returned by settings.getFSWriteEnabled', async () => {
    const setup = makeClient([]);
    setup.client.settings.getFSWriteEnabled = async () => true;

    const w = await mountPanel(setup);
    await flushPromises();

    const toggle = w.find<HTMLInputElement>('[data-testid=fs-write-toggle]');
    expect((toggle.element as HTMLInputElement).checked).toBe(true);
  });

  it('reverts fs-read toggle on error and shows error message', async () => {
    const setup = makeClient([]);
    setup.client.settings.setFSReadEnabled = vi.fn(async () => {
      throw new Error('store write failed');
    });

    const w = await mountPanel(setup);
    await flushPromises();

    const toggle = w.find('[data-testid=fs-read-toggle]');
    const el = toggle.element as HTMLInputElement;
    el.checked = true;
    await toggle.trigger('change');
    await flushPromises();

    // The toggle should revert to false after the error.
    expect((toggle.element as HTMLInputElement).checked).toBe(false);
    // The error message from the thrown Error is surfaced to the user.
    const row = w.find('[data-testid=fs-read-tool-row]');
    expect(row.text()).toContain('store write failed');
  });

  it('reverts fs-write toggle on error and shows error message', async () => {
    const setup = makeClient([]);
    setup.client.settings.setFSWriteEnabled = vi.fn(async () => {
      throw new Error('store write failed');
    });

    const w = await mountPanel(setup);
    await flushPromises();

    const toggle = w.find('[data-testid=fs-write-toggle]');
    const el = toggle.element as HTMLInputElement;
    el.checked = true;
    await toggle.trigger('change');
    await flushPromises();

    // The toggle should revert to false after the error.
    expect((toggle.element as HTMLInputElement).checked).toBe(false);
    // The error message from the thrown Error is surfaced to the user.
    const row = w.find('[data-testid=fs-write-tool-row]');
    expect(row.text()).toContain('store write failed');
  });

  it('fs-read row lists the 5 read-family tool names', async () => {
    const setup = makeClient([]);
    const w = await mountPanel(setup);
    await flushPromises();

    const row = w.find('[data-testid=fs-read-tool-row]');
    const text = row.text();
    expect(text).toContain('kenaz__read_file');
    expect(text).toContain('kenaz__list_dir');
    expect(text).toContain('kenaz__glob');
    expect(text).toContain('kenaz__grep');
    expect(text).toContain('kenaz__list_open_worklist');
  });

  it('fs-write row lists the 2 write-family tool names', async () => {
    const setup = makeClient([]);
    const w = await mountPanel(setup);
    await flushPromises();

    const row = w.find('[data-testid=fs-write-tool-row]');
    const text = row.text();
    expect(text).toContain('kenaz__write_file');
    expect(text).toContain('kenaz__edit_file');
  });
});
