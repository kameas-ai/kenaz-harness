/**
 * McpRecipeDetail — the MCP detail plugin of the "Add capability" surface
 * (install-framework-01DOGF0B WP04). Ported from the retired
 * KenazToolsPanel.test.ts: every per-row MCP flow KenazToolsPanel owned now
 * lives in this plugin, and these are the same assertions against it
 * (P-6, mcp flows: key prompt / edit configuration, directory-backed open
 * workspace, forget key, edit recipe, remove, org read-only, health push).
 *
 * In-memory fake client, DELIBERATELY (WP-PI AC-PI-2): these tests pin which
 * RPCs the plugin calls with which arguments, not storage — the persisted
 * enabled-list round trip is core/rpc/views/capabilities'
 * TestInstallProvider_MCPRecipe_ConsumerSeesInstall (recipes.LoadEnabled).
 */
import { describe, it, expect, vi, afterEach } from 'vitest';
import { mount, flushPromises } from '@vue/test-utils';
import { defineComponent } from 'vue';
import McpRecipeDetail from '@/views/capabilities/plugins/McpRecipeDetail.vue';
import { createFakeHarnessClient, type HarnessClient } from '@/lib/harnessClient';
import { HarnessClientKey } from '@/lib/harnessClientContext';
import { dispatchServedEvent } from '@/lib/useServedEvents';
import type { CapabilityItem, Recipe, RecipeListing, RecipeStatus } from '@/lib/types';

function makeRecipe(id: string, overrides: Partial<Recipe> = {}): Recipe {
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
    capabilities: { tools: true, resources: false, prompts: false, sampling: false },
    docsUrl: `https://example.com/${id}`,
    ...overrides,
  };
}

function makeStatus(id: string, overrides: Partial<RecipeStatus> = {}): RecipeStatus {
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

function running(id: string): RecipeStatus {
  return makeStatus(id, { enabled: true, state: 'running', keysPresent: true });
}

function makeListing(recipe: Recipe, overrides: Partial<RecipeListing> = {}): RecipeListing {
  return {
    recipe,
    enabled: false,
    keysPresent: false,
    status: makeStatus(recipe.id),
    source: 'shipped',
    ...overrides,
  };
}

function enabled(recipe: Recipe, overrides: Partial<RecipeListing> = {}): RecipeListing {
  return makeListing(recipe, { enabled: true, keysPresent: true, status: running(recipe.id), ...overrides });
}

function itemFor(l: RecipeListing): CapabilityItem {
  return {
    kind: 'mcp_recipe',
    id: l.recipe.id,
    name: l.recipe.displayName,
    description: l.recipe.description,
    category: String(l.recipe.category),
    source: l.source === 'org' ? 'org_catalog' : 'builtin',
    state: { installed: l.enabled, consumer: 'MCP supervisor' },
    read_only: l.source === 'org',
    read_only_reason: l.source === 'org' ? 'Provisioned by your org' : undefined,
  };
}

function makeClient(initial: RecipeListing[], statusSeq: Map<string, RecipeStatus[]> = new Map()) {
  const list = vi.fn(async () => initial.map((l) => ({ ...l })));
  const install = vi.fn(async (id: string) => makeStatus(id, { enabled: true, state: 'starting', keysPresent: true }));
  const uninstall = vi.fn(async () => undefined);
  const forgetKey = vi.fn(async () => undefined);
  const config = vi.fn(async (_id: string): Promise<Record<string, unknown>> => ({}));
  const status = vi.fn(async (id: string): Promise<RecipeStatus> => {
    const seq = statusSeq.get(id);
    if (seq && seq.length > 0) return seq.shift() ?? makeStatus(id);
    return running(id);
  });
  const client: HarnessClient = createFakeHarnessClient({
    tools: { recipes: { list, install, uninstall, forgetKey, status, config } as any } as any,
  });
  return { client, spies: { list, install, uninstall, forgetKey, status, config } };
}

const ModalStub = defineComponent({
  props: {
    open: Boolean,
    recipe: { type: Object, default: null },
    install: { type: Function, default: null },
    initialConfig: { type: Object, default: null },
  },
  emits: ['close', 'installed'],
  render: () => null,
});

function mountDetail(client: HarnessClient, l: RecipeListing) {
  return mount(McpRecipeDetail, {
    props: { item: itemFor(l) },
    global: {
      provide: { [HarnessClientKey as symbol]: client },
      stubs: { RecipeKeyPromptModal: ModalStub },
    },
  });
}

afterEach(() => {
  vi.useRealTimers();
});

describe('McpRecipeDetail — installed recipe', () => {
  it('renders the health pill from the supervisor status', async () => {
    const l = enabled(makeRecipe('filesystem', { category: 'filesystem' }));
    const { client } = makeClient([l]);
    const w = mountDetail(client, l);
    await flushPromises();
    expect(w.find('[data-testid=recipe-state-filesystem]').text()).toContain('running');
  });

  it('a live mcp:health-changed push flips the pill and shows the error with no poll tick (UNIT-8, AC-005b)', async () => {
    const l = enabled(makeRecipe('remote-srv'));
    const { client } = makeClient([l]);
    const w = mountDetail(client, l);
    await flushPromises();
    expect(w.find('[data-testid=recipe-health-alert-remote-srv]').exists()).toBe(false);

    dispatchServedEvent('mcp:health-changed', {
      id: 'remote-srv',
      state: 'failed',
      last_error: 'two consecutive tools/list probe failures',
      restart_attempts: 0,
      tool_count: 0,
    });
    await flushPromises();

    expect(w.find('[data-testid=recipe-state-remote-srv]').text()).toContain('failed');
    expect(w.find('[data-testid=recipe-health-alert-remote-srv]').text()).toContain(
      'two consecutive tools/list probe failures',
    );
  });

  it('edit configuration opens the key-prompt flow and forwards env+config to Tools_InstallRecipe', async () => {
    const r = makeRecipe('filesystem', {
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
    const l = enabled(r);
    const { client, spies } = makeClient([l]);
    const w = mountDetail(client, l);
    await flushPromises();

    await w.get('[data-testid=recipe-edit-config-filesystem]').trigger('click');
    await flushPromises();
    const modal = w.findComponent(ModalStub);
    expect(modal.props('open')).toBe(true);
    expect((modal.props('recipe') as Recipe).id).toBe('filesystem');
    expect(spies.install).not.toHaveBeenCalled();

    const install = modal.props('install') as (
      id: string,
      env: Record<string, string>,
      config: Record<string, unknown>,
    ) => Promise<RecipeStatus>;
    await install('filesystem', {}, { allowed_directories: ['/tmp/x'] });
    await flushPromises();
    expect(spies.install).toHaveBeenCalledWith('filesystem', {}, { allowed_directories: ['/tmp/x'] });
  });

  it('forget-key calls forgetKey(id, name)', async () => {
    const l = enabled(makeRecipe('brave-search'));
    const { client, spies } = makeClient([l]);
    const w = mountDetail(client, l);
    await flushPromises();
    await w.get('[data-testid=recipe-forget-brave-search-BRAVE-SEARCH_API_KEY]').trigger('click');
    await flushPromises();
    expect(spies.forgetKey).toHaveBeenCalledWith('brave-search', 'BRAVE-SEARCH_API_KEY');
  });

  it('remove asks for confirmation, then uninstalls through Tools_UninstallRecipe', async () => {
    const l = enabled(makeRecipe('brave-search'));
    const { client, spies } = makeClient([l]);
    const w = mountDetail(client, l);
    await flushPromises();

    await w.get('[data-testid=recipe-delete-btn-brave-search]').trigger('click');
    expect(spies.uninstall).not.toHaveBeenCalled();
    expect(w.find('[data-testid=recipe-delete-confirm]').exists()).toBe(true);
    await w.get('[data-testid=recipe-delete-confirm-yes-brave-search]').trigger('click');
    await flushPromises();
    expect(spies.uninstall).toHaveBeenCalledWith('brave-search');
    expect(w.emitted('changed')).toBeTruthy();
  });

  it('polls recipeStatus at 1 Hz while starting and stops once terminal', async () => {
    vi.useFakeTimers();
    const seq = [
      makeStatus('brave-search', { enabled: true, state: 'starting', keysPresent: true }),
      running('brave-search'),
    ];
    const l = enabled(makeRecipe('brave-search'), {
      status: makeStatus('brave-search', { enabled: true, state: 'starting', keysPresent: true }),
    });
    const { client, spies } = makeClient([l], new Map([['brave-search', seq]]));
    const w = mountDetail(client, l);
    await flushPromises();
    expect(spies.status).not.toHaveBeenCalled();
    await vi.advanceTimersByTimeAsync(1000);
    expect(spies.status).toHaveBeenCalledTimes(1);
    await vi.advanceTimersByTimeAsync(1000);
    expect(spies.status).toHaveBeenCalledTimes(2);
    await vi.advanceTimersByTimeAsync(3000);
    expect(spies.status).toHaveBeenCalledTimes(2);
    w.unmount();
  });

  it('shows the warming hint only after 4 s in starting', async () => {
    vi.useFakeTimers();
    vi.setSystemTime(new Date(2026, 0, 1, 12, 0, 0));
    const r = makeRecipe('brave-search', {
      envKeys: [],
      configOptions: [
        { name: 'mode', display: 'Mode', kind: 'string', required: false, description: '', default: 'fast' },
      ],
    });
    const starting = () => makeStatus('brave-search', { enabled: true, state: 'starting', keysPresent: true });
    const l = enabled(r, { status: starting() });
    const { client, spies } = makeClient([l], new Map([['brave-search', [starting(), starting(), starting(), starting(), starting()]]]));
    spies.install.mockImplementation(async () => starting());
    const w = mountDetail(client, l);
    await flushPromises();

    await w.get('[data-testid=recipe-edit-config-brave-search]').trigger('click');
    await flushPromises();
    const install = w.findComponent(ModalStub).props('install') as (
      id: string,
      env: Record<string, string>,
      config: Record<string, unknown>,
    ) => Promise<RecipeStatus>;
    await install('brave-search', {}, { mode: 'fast' });
    await flushPromises();
    expect(w.find('[data-testid=recipe-warming-brave-search]').exists()).toBe(false);
    await vi.advanceTimersByTimeAsync(5000);
    await flushPromises();
    expect(w.find('[data-testid=recipe-warming-brave-search]').exists()).toBe(true);
    w.unmount();
  });

  it('uses only design tokens — no raw hex / rgba in markup', async () => {
    const l = enabled(makeRecipe('brave-search'));
    const { client } = makeClient([l]);
    const w = mountDetail(client, l);
    await flushPromises();
    expect(w.html()).not.toMatch(/#[0-9a-fA-F]{3,8}\b/);
    expect(w.html()).not.toMatch(/rgba?\s*\(/i);
  });
});

describe('McpRecipeDetail — filesystem workspace (directory flow)', () => {
  function fsRecipe(): Recipe {
    return makeRecipe('filesystem', {
      displayName: 'Filesystem',
      category: 'filesystem',
      envKeys: [],
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
    });
  }

  it('a running filesystem recipe offers Open workspace, which opens the first allowed directory', async () => {
    const l = enabled(fsRecipe());
    const { client } = makeClient([l]);
    client.tools.recipes.config = vi.fn(async () => ({
      allowed_directories: ['/Users/me/.harness/agent-workspace', '/tmp/extra'],
    }));
    const openInOSBrowser = vi.fn(async () => undefined);
    client.shell = { ...client.shell, openInOSBrowser } as any;
    const w = mountDetail(client, l);
    await flushPromises();
    await flushPromises();

    await w.get('[data-testid=recipe-open-workspace-filesystem]').trigger('click');
    await flushPromises();
    expect(openInOSBrowser).toHaveBeenCalledWith('/Users/me/.harness/agent-workspace');
  });

  it('hides Open workspace while not running', async () => {
    const l = enabled(fsRecipe(), {
      status: makeStatus('filesystem', { enabled: true, state: 'starting', keysPresent: true }),
    });
    const { client } = makeClient([l]);
    const w = mountDetail(client, l);
    await flushPromises();
    expect(w.find('[data-testid=recipe-open-workspace-filesystem]').exists()).toBe(false);
  });
});

describe('McpRecipeDetail — not yet installed', () => {
  it('beginInstall opens the key-prompt flow (keys required) without installing on its own', async () => {
    const l = makeListing(makeRecipe('brave-search'));
    const { client, spies } = makeClient([l]);
    const w = mountDetail(client, l);
    await flushPromises();

    await w.get('[data-testid=recipe-configure-install-brave-search]').trigger('click');
    await flushPromises();
    const modal = w.findComponent(ModalStub);
    expect(modal.props('open')).toBe(true);
    expect((modal.props('recipe') as Recipe).id).toBe('brave-search');
    expect(spies.install).not.toHaveBeenCalled();
    // The installed-only affordances are absent.
    expect(w.find('[data-testid=recipe-delete-btn-brave-search]').exists()).toBe(false);
  });
});

describe('McpRecipeDetail — edit recipe (custom-recipe authoring)', () => {
  it('Edit opens AddMCPServerModal on the recipe', async () => {
    const l = enabled(makeRecipe('brave-search'));
    const { client } = makeClient([l]);
    const w = mountDetail(client, l);
    await flushPromises();
    await w.get('[data-testid=recipe-edit-btn-brave-search]').trigger('click');
    await flushPromises();
    expect(w.find('[data-testid=add-mcp-modal]').exists()).toBe(true);
    expect(w.find('[data-testid=add-mcp-tab-custom]').attributes('aria-selected')).toBe('true');
  });
});

describe('McpRecipeDetail — org-provisioned recipe is read-only', () => {
  it('shows the org badge and hides Edit + Remove for a source=org recipe', async () => {
    const l = enabled(makeRecipe('org-slack'), { source: 'org' });
    const { client } = makeClient([l]);
    const w = mountDetail(client, l);
    await flushPromises();
    expect(w.find('[data-testid=recipe-org-badge-org-slack]').text()).toContain('Provisioned by your org');
    expect(w.find('[data-testid=recipe-edit-btn-org-slack]').exists()).toBe(false);
    expect(w.find('[data-testid=recipe-delete-btn-org-slack]').exists()).toBe(false);
  });

  it('keeps Edit + Remove for a shipped recipe (mutation-proof: not hiding everything)', async () => {
    const l = enabled(makeRecipe('brave-search'));
    const { client } = makeClient([l]);
    const w = mountDetail(client, l);
    await flushPromises();
    expect(w.find('[data-testid=recipe-org-badge-brave-search]').exists()).toBe(false);
    expect(w.find('[data-testid=recipe-edit-btn-brave-search]').exists()).toBe(true);
    expect(w.find('[data-testid=recipe-delete-btn-brave-search]').exists()).toBe(true);
  });
});
