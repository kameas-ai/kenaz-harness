import { describe, it, expect, vi, afterEach, beforeEach } from 'vitest';
import { mount, flushPromises } from '@vue/test-utils';
import { defineComponent, h } from 'vue';
import { createMemoryHistory, createRouter } from 'vue-router';
import KaneazToolsPanel from '@/views/tools/KaneazToolsPanel.vue';
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
  const install = vi.fn<
    [string, Record<string, string>],
    Promise<RecipeStatus>
  >(async (id) =>
    makeStatus(id, { enabled: true, state: 'starting', keysPresent: true }),
  );
  const uninstall = vi.fn(async () => undefined);
  const forgetKey = vi.fn(async () => undefined);
  const status = vi.fn<[string], Promise<RecipeStatus>>(async (id) => {
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
      },
    },
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
  return mount(KaneazToolsPanel, {
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

describe('KaneazToolsPanel — recipes section', () => {
  it('renders the existing Memory row plus a row per recipe', async () => {
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
    expect(w.find('[data-testid=recipe-row-brave-search]').exists()).toBe(true);
    expect(w.find('[data-testid=recipe-row-filesystem]').exists()).toBe(true);
    expect(w.find('[data-testid=recipe-state-filesystem]').text()).toBe(
      'running',
    );
  });

  it('toggle-on with keysPresent calls install({}) and does not open the modal', async () => {
    const recipes = [
      makeListing(makeRecipe('brave-search'), {
        enabled: false,
        keysPresent: true,
        status: makeStatus('brave-search', { keysPresent: true }),
      }),
    ];
    const setup = makeClient(recipes);
    const w = await mountPanel(setup);
    await flushPromises();

    const toggle = w.get('[data-testid=recipe-toggle-brave-search]');
    await toggle.setValue(true);
    await flushPromises();

    expect(setup.spies.install).toHaveBeenCalledTimes(1);
    expect(setup.spies.install).toHaveBeenCalledWith('brave-search', {});

    // Modal stub is rendered only when `modalRecipe` is non-null in the
    // panel; with keysPresent the modal stays unmounted.
    expect(w.findComponent(Stub).exists()).toBe(false);
  });

  it('toggle-on with missing keys opens the modal (does not call install)', async () => {
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

    const toggle = w.get('[data-testid=recipe-toggle-brave-search]');
    await toggle.setValue(true);
    await flushPromises();

    expect(setup.spies.install).not.toHaveBeenCalled();
    const modal = w.findComponent(Stub);
    expect(modal.exists()).toBe(true);
    expect(modal.props('open')).toBe(true);
    expect((modal.props('recipe') as Recipe).id).toBe('brave-search');
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
    // Start with a stopped row that we toggle on with keysPresent so
    // useToolsRecipes records a startedAt timestamp.
    const recipes = [
      makeListing(makeRecipe('brave-search'), {
        enabled: false,
        keysPresent: true,
        status: makeStatus('brave-search', {
          enabled: false,
          state: 'stopped',
          keysPresent: true,
        }),
      }),
    ];
    // After install the status sequence keeps the row in `starting`.
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

    const toggle = w.get('[data-testid=recipe-toggle-brave-search]');
    await toggle.setValue(true);
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
});
