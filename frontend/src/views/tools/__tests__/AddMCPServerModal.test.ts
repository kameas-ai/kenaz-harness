import { describe, it, expect, vi, afterEach } from 'vitest';
import { mount, flushPromises } from '@vue/test-utils';
import AddMCPServerModal from '@/views/tools/AddMCPServerModal.vue';
import RegistryTab from '@/views/tools/RegistryTab.vue';
import PasteConfigTab from '@/views/tools/PasteConfigTab.vue';
import CustomRecipeTab from '@/views/tools/CustomRecipeTab.vue';
import {
  createFakeHarnessClient,
  type HarnessClient,
} from '@/lib/harnessClient';
import { HarnessClientKey } from '@/lib/harnessClientContext';
import type {
  Recipe,
  RecipeListing,
  RecipeStatus,
  MCPImportResponse,
} from '@/lib/types';

// ── helpers ────────────────────────────────────────────────────────────

function makeRecipe(id: string, overrides: Partial<Recipe> = {}): Recipe {
  return {
    id,
    displayName: id,
    description: `Recipe ${id}`,
    category: 'search',
    envKeys: [],
    capabilities: {
      tools: true,
      resources: false,
      prompts: false,
      sampling: false,
    },
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

function makeListing(recipe: Recipe, overrides: Partial<RecipeListing> = {}): RecipeListing {
  return {
    recipe,
    enabled: false,
    keysPresent: false,
    status: makeStatus(recipe.id),
    ...overrides,
  };
}

function mountModal(props: Record<string, unknown>, client?: Partial<HarnessClient>) {
  const fakeClient = createFakeHarnessClient(client);
  return mount(AddMCPServerModal, {
    props: { open: true, ...props },
    global: {
      provide: { [HarnessClientKey as symbol]: fakeClient },
    },
  });
}

function mountRegistryTab(client?: Partial<HarnessClient>) {
  const fakeClient = createFakeHarnessClient(client);
  return mount(RegistryTab, {
    global: {
      provide: { [HarnessClientKey as symbol]: fakeClient },
    },
  });
}

function mountPasteTab(client?: Partial<HarnessClient>) {
  const fakeClient = createFakeHarnessClient(client);
  return mount(PasteConfigTab, {
    global: {
      provide: { [HarnessClientKey as symbol]: fakeClient },
    },
  });
}

function mountCustomTab(props: Record<string, unknown> = {}, client?: Partial<HarnessClient>) {
  const fakeClient = createFakeHarnessClient(client);
  return mount(CustomRecipeTab, {
    props,
    global: {
      provide: { [HarnessClientKey as symbol]: fakeClient },
    },
  });
}

// ── AddMCPServerModal — tabs render and switch ─────────────────────────

describe('AddMCPServerModal — tabs', () => {
  it('renders all three tabs and defaults to Registry', async () => {
    const w = mountModal({});
    await flushPromises();

    expect(w.find('[data-testid="add-mcp-modal"]').exists()).toBe(true);
    expect(w.find('[data-testid="add-mcp-tab-registry"]').exists()).toBe(true);
    expect(w.find('[data-testid="add-mcp-tab-paste"]').exists()).toBe(true);
    expect(w.find('[data-testid="add-mcp-tab-custom"]').exists()).toBe(true);
    // Registry tab is active by default
    expect(w.find('[data-testid="registry-tab"]').exists()).toBe(true);
  });

  it('switches to Paste tab on click', async () => {
    const w = mountModal({});
    await flushPromises();

    await w.find('[data-testid="add-mcp-tab-paste"]').trigger('click');
    expect(w.find('[data-testid="paste-config-tab"]').exists()).toBe(true);
    expect(w.find('[data-testid="registry-tab"]').exists()).toBe(false);
  });

  it('switches to Custom tab on click', async () => {
    const w = mountModal({});
    await flushPromises();

    await w.find('[data-testid="add-mcp-tab-custom"]').trigger('click');
    expect(w.find('[data-testid="custom-recipe-tab"]').exists()).toBe(true);
    expect(w.find('[data-testid="registry-tab"]').exists()).toBe(false);
  });

  it('starts on Custom tab when editRecipe is provided', async () => {
    const recipe = makeRecipe('my-recipe');
    const w = mountModal({ editRecipe: recipe });
    await flushPromises();

    expect(w.find('[data-testid="custom-recipe-tab"]').exists()).toBe(true);
    expect(w.find('[data-testid="registry-tab"]').exists()).toBe(false);
  });

  it('emits close when Close button is clicked', async () => {
    const w = mountModal({});
    await w.find('[data-testid="add-mcp-modal-close"]').trigger('click');
    expect(w.emitted('close')).toBeTruthy();
  });

  it('emits close when Escape is pressed', async () => {
    const w = mountModal({});
    await w.trigger('keydown', { key: 'Escape' });
    expect(w.emitted('close')).toBeTruthy();
  });

  it('does not render when open is false', async () => {
    const fakeClient = createFakeHarnessClient();
    const w = mount(AddMCPServerModal, {
      props: { open: false },
      global: { provide: { [HarnessClientKey as symbol]: fakeClient } },
    });
    expect(w.find('[data-testid="add-mcp-modal"]').exists()).toBe(false);
  });
});

// ── RegistryTab ────────────────────────────────────────────────────────

describe('RegistryTab — list and install', () => {
  afterEach(() => {
    vi.restoreAllMocks();
  });

  it('shows only non-enabled recipes after load', async () => {
    const recipes = [
      makeListing(makeRecipe('brave-search'), { enabled: false }),
      makeListing(makeRecipe('filesystem'), { enabled: true }),
    ];
    const list = vi.fn(async () => recipes);
    const w = mountRegistryTab({ tools: { recipes: { list, install: vi.fn(), uninstall: vi.fn(), forgetKey: vi.fn(), status: vi.fn(), config: vi.fn(async () => ({})) } } });

    await flushPromises();

    // Only non-enabled recipes appear
    expect(w.find('[data-testid="registry-row-brave-search"]').exists()).toBe(true);
    expect(w.find('[data-testid="registry-row-filesystem"]').exists()).toBe(false);
  });

  it('shows empty message when all recipes are enabled', async () => {
    const recipes = [
      makeListing(makeRecipe('brave-search'), { enabled: true }),
    ];
    const list = vi.fn(async () => recipes);
    const w = mountRegistryTab({ tools: { recipes: { list, install: vi.fn(), uninstall: vi.fn(), forgetKey: vi.fn(), status: vi.fn(), config: vi.fn(async () => ({})) } } });
    await flushPromises();

    expect(w.find('[data-testid="registry-empty"]').exists()).toBe(true);
  });

  it('shows error when list fails', async () => {
    const list = vi.fn(async () => { throw new Error('Network error'); });
    const w = mountRegistryTab({ tools: { recipes: { list, install: vi.fn(), uninstall: vi.fn(), forgetKey: vi.fn(), status: vi.fn(), config: vi.fn(async () => ({})) } } });
    await flushPromises();

    expect(w.find('[data-testid="registry-error"]').text()).toContain('Network error');
  });

  it('Install button calls install and emits installed', async () => {
    const recipes = [makeListing(makeRecipe('brave-search'), { enabled: false })];
    const installFn = vi.fn(async () => makeStatus('brave-search', { enabled: true, state: 'running' }));
    const list = vi.fn(async () => recipes);
    const w = mountRegistryTab({
      tools: {
        recipes: {
          list,
          install: installFn,
          uninstall: vi.fn(),
          forgetKey: vi.fn(),
          status: vi.fn(),
          config: vi.fn(async () => ({})),
        },
      },
    });
    await flushPromises();

    await w.find('[data-testid="registry-install-brave-search"]').trigger('click');
    await flushPromises();

    expect(installFn).toHaveBeenCalledWith('brave-search', {}, {});
    expect(w.emitted('installed')).toBeTruthy();
  });

  it('shows row error when install fails', async () => {
    const recipes = [makeListing(makeRecipe('brave-search'), { enabled: false })];
    const installFn = vi.fn(async () => { throw new Error('Install failed'); });
    const list = vi.fn(async () => recipes);
    const w = mountRegistryTab({
      tools: {
        recipes: {
          list,
          install: installFn,
          uninstall: vi.fn(),
          forgetKey: vi.fn(),
          status: vi.fn(),
          config: vi.fn(async () => ({})),
        },
      },
    });
    await flushPromises();

    await w.find('[data-testid="registry-install-brave-search"]').trigger('click');
    await flushPromises();

    expect(w.find('[data-testid="registry-row-error-brave-search"]').text()).toContain('Install failed');
  });
});

// ── PasteConfigTab — dry-run + import ─────────────────────────────────

describe('PasteConfigTab — dry-run and import', () => {
  afterEach(() => {
    vi.restoreAllMocks();
  });

  it('Translate button is disabled when textarea is empty', async () => {
    const w = mountPasteTab();
    const btn = w.find('[data-testid="paste-translate-btn"]');
    expect(btn.attributes('disabled')).toBeDefined();
  });

  it('Translate button calls dry-run and shows entries', async () => {
    const dryRunResponse: MCPImportResponse = {
      report: {
        entries: [
          {
            id: 'brave-search',
            original_name: 'brave-search',
            status: 'kept',
            recipe: makeRecipe('brave-search'),
          },
          {
            id: 'bad-server',
            original_name: 'bad-server',
            status: 'unsupported',
            reason: 'HTTP transport not supported',
            recipe: makeRecipe('bad-server'),
          },
        ],
        kept_count: 1,
        unsupported_count: 1,
        malformed_count: 0,
        collision_count: 0,
      },
    };

    const importFn = vi.fn(async () => dryRunResponse);
    const w = mountPasteTab({ mcp: { listServers: vi.fn(async () => []), startStream: vi.fn(async () => 'sub'), stopStream: vi.fn(), importClaudeDesktopConfig: importFn } });

    // Fill in textarea
    const textarea = w.find('[data-testid="paste-config-textarea"]');
    await textarea.setValue('{"mcpServers": {"brave-search": {}}}');

    await w.find('[data-testid="paste-translate-btn"]').trigger('click');
    await flushPromises();

    expect(importFn).toHaveBeenCalledWith(
      expect.objectContaining({ dry_run: true }),
    );

    // Entries table appears
    expect(w.find('[data-testid="paste-entries-table"]').exists()).toBe(true);
    expect(w.find('[data-testid="paste-entry-brave-search"]').exists()).toBe(true);
    expect(w.find('[data-testid="paste-entry-status-brave-search"]').text()).toContain('kept');
    expect(w.find('[data-testid="paste-entry-status-bad-server"]').text()).toContain('unsupported');
  });

  it('pre-selects kept entries and import button shows count', async () => {
    const dryRunResponse: MCPImportResponse = {
      report: {
        entries: [
          {
            id: 'brave-search',
            original_name: 'brave-search',
            status: 'kept',
            recipe: makeRecipe('brave-search'),
          },
        ],
        kept_count: 1,
        unsupported_count: 0,
        malformed_count: 0,
        collision_count: 0,
      },
    };
    const importFn = vi.fn(async () => dryRunResponse);
    const w = mountPasteTab({ mcp: { listServers: vi.fn(async () => []), startStream: vi.fn(async () => 'sub'), stopStream: vi.fn(), importClaudeDesktopConfig: importFn } });

    await w.find('[data-testid="paste-config-textarea"]').setValue('{}');
    await w.find('[data-testid="paste-translate-btn"]').trigger('click');
    await flushPromises();

    const importBtn = w.find('[data-testid="paste-import-btn"]');
    expect(importBtn.text()).toContain('Import 1');
  });

  it('Import button calls with dryRun:false and selected keepIds', async () => {
    const response: MCPImportResponse = {
      report: {
        entries: [
          {
            id: 'brave-search',
            original_name: 'brave-search',
            status: 'kept',
            recipe: makeRecipe('brave-search'),
          },
        ],
        kept_count: 1,
        unsupported_count: 0,
        malformed_count: 0,
        collision_count: 0,
      },
    };
    const importFn = vi.fn(async () => response);
    const w = mountPasteTab({ mcp: { listServers: vi.fn(async () => []), startStream: vi.fn(async () => 'sub'), stopStream: vi.fn(), importClaudeDesktopConfig: importFn } });

    await w.find('[data-testid="paste-config-textarea"]').setValue('{}');
    await w.find('[data-testid="paste-translate-btn"]').trigger('click');
    await flushPromises();

    await w.find('[data-testid="paste-import-btn"]').trigger('click');
    await flushPromises();

    // Should have been called twice: once dry_run:true, once dry_run:false
    expect(importFn).toHaveBeenCalledTimes(2);
    expect(importFn).toHaveBeenLastCalledWith(
      expect.objectContaining({ dry_run: false, keep_ids: expect.arrayContaining(['brave-search']) }),
    );
  });

  it('shows skip-confirm dialog when unsupported entries present', async () => {
    const response: MCPImportResponse = {
      report: {
        entries: [
          {
            id: 'brave-search',
            original_name: 'brave-search',
            status: 'kept',
            recipe: makeRecipe('brave-search'),
          },
          {
            id: 'bad',
            original_name: 'bad',
            status: 'unsupported',
            reason: 'HTTP transport not supported',
            recipe: makeRecipe('bad'),
          },
        ],
        kept_count: 1,
        unsupported_count: 1,
        malformed_count: 0,
        collision_count: 0,
      },
    };
    const importFn = vi.fn(async () => response);
    const w = mountPasteTab({ mcp: { listServers: vi.fn(async () => []), startStream: vi.fn(async () => 'sub'), stopStream: vi.fn(), importClaudeDesktopConfig: importFn } });

    await w.find('[data-testid="paste-config-textarea"]').setValue('{}');
    await w.find('[data-testid="paste-translate-btn"]').trigger('click');
    await flushPromises();

    await w.find('[data-testid="paste-import-btn"]').trigger('click');
    await flushPromises();

    // Skip confirm dialog should appear
    expect(w.find('[data-testid="paste-skip-confirm"]').exists()).toBe(true);

    // Confirm skip
    await w.find('[data-testid="paste-skip-confirm-yes"]').trigger('click');
    await flushPromises();

    expect(importFn).toHaveBeenCalledTimes(2);
    expect(importFn).toHaveBeenLastCalledWith(
      expect.objectContaining({ dry_run: false }),
    );
  });

  it('collision_warning entries show shadow warning', async () => {
    const response: MCPImportResponse = {
      report: {
        entries: [
          {
            id: 'brave-search',
            original_name: 'brave-search',
            status: 'collision_warning',
            reason: 'Collides with existing recipe brave-search',
            recipe: makeRecipe('brave-search'),
          },
        ],
        kept_count: 0,
        unsupported_count: 0,
        malformed_count: 0,
        collision_count: 1,
      },
    };
    const importFn = vi.fn(async () => response);
    const w = mountPasteTab({ mcp: { listServers: vi.fn(async () => []), startStream: vi.fn(async () => 'sub'), stopStream: vi.fn(), importClaudeDesktopConfig: importFn } });

    await w.find('[data-testid="paste-config-textarea"]').setValue('{}');
    await w.find('[data-testid="paste-translate-btn"]').trigger('click');
    await flushPromises();

    expect(w.find('[data-testid="paste-entry-shadow-warning-brave-search"]').exists()).toBe(true);
    expect(w.find('[data-testid="paste-report-collision"]').exists()).toBe(true);
  });

  it('shows translate error on API failure', async () => {
    const importFn = vi.fn(async () => { throw new Error('Bad JSON'); });
    const w = mountPasteTab({ mcp: { listServers: vi.fn(async () => []), startStream: vi.fn(async () => 'sub'), stopStream: vi.fn(), importClaudeDesktopConfig: importFn } });

    await w.find('[data-testid="paste-config-textarea"]').setValue('bad json');
    await w.find('[data-testid="paste-translate-btn"]').trigger('click');
    await flushPromises();

    expect(w.find('[data-testid="paste-translate-error"]').text()).toContain('Bad JSON');
  });
});

// ── CustomRecipeTab — form validation ─────────────────────────────────

describe('CustomRecipeTab — form validation', () => {
  it('Save button is disabled when ID is empty', async () => {
    const w = mountCustomTab();
    const btn = w.find('[data-testid="custom-save-btn"]');
    expect(btn.attributes('disabled')).toBeDefined();
  });

  it('shows ID format error for invalid id', async () => {
    const w = mountCustomTab();
    await w.find('[data-testid="custom-id-input"]').setValue('My Invalid ID');
    expect(w.find('[data-testid="custom-id-error"]').exists()).toBe(true);
  });

  it('shows command error for stdio with empty command', async () => {
    const w = mountCustomTab();
    await w.find('[data-testid="custom-id-input"]').setValue('my-server');
    await w.find('[data-testid="custom-display-name-input"]').setValue('My Server');
    // stdio is default; command is empty
    expect(w.find('[data-testid="custom-command-error"]').exists()).toBe(true);
  });

  it('Save button becomes enabled with valid stdio form', async () => {
    const w = mountCustomTab();
    await w.find('[data-testid="custom-id-input"]').setValue('my-server');
    await w.find('[data-testid="custom-display-name-input"]').setValue('My Server');
    await w.find('[data-testid="custom-command-input"]').setValue('npx');
    const btn = w.find('[data-testid="custom-save-btn"]');
    expect(btn.attributes('disabled')).toBeUndefined();
  });

  it('shows shadow warning when id collides with existingIds', async () => {
    const w = mountCustomTab({ existingIds: ['brave-search'] });
    await w.find('[data-testid="custom-id-input"]').setValue('brave-search');
    expect(w.find('[data-testid="custom-shadow-warning"]').exists()).toBe(true);
    expect(w.find('[data-testid="custom-shadow-warning"]').text()).toContain('shadow');
  });

  it('does NOT show shadow warning for non-colliding id', async () => {
    const w = mountCustomTab({ existingIds: ['brave-search'] });
    await w.find('[data-testid="custom-id-input"]').setValue('my-new-server');
    expect(w.find('[data-testid="custom-shadow-warning"]').exists()).toBe(false);
  });

  it('Save shows WP10 stub error (backend gap)', async () => {
    const w = mountCustomTab();
    await w.find('[data-testid="custom-id-input"]').setValue('my-server');
    await w.find('[data-testid="custom-display-name-input"]').setValue('My Server');
    await w.find('[data-testid="custom-command-input"]').setValue('npx');
    await flushPromises();

    await w.find('[data-testid="custom-save-btn"]').trigger('click');
    await flushPromises();

    expect(w.find('[data-testid="custom-save-error"]').text()).toContain('WP10');
  });

  it('shows http URL field when transport is http', async () => {
    const w = mountCustomTab();
    await w.find('[data-testid="custom-transport-http"]').setValue(true);
    await w.vm.$nextTick();
    expect(w.find('[data-testid="custom-url-input"]').exists()).toBe(true);
    expect(w.find('[data-testid="custom-command-input"]').exists()).toBe(false);
  });

  it('shows SSE-specific post url field when transport is sse', async () => {
    const w = mountCustomTab();
    const sseRadio = w.find('[data-testid="custom-transport-sse"]');
    await sseRadio.setValue(true);
    await w.vm.$nextTick();
    expect(w.find('[data-testid="custom-post-url-input"]').exists()).toBe(true);
  });

  it('shows WP10 stub notice in the UI', async () => {
    const w = mountCustomTab();
    expect(w.find('[data-testid="custom-save-stub-notice"]').exists()).toBe(true);
  });

  it('pre-fills form when initialRecipe is provided', async () => {
    const recipe = makeRecipe('my-existing', {
      displayName: 'My Existing Server',
      argsTemplate: ['npx', '-y', '@foo/bar'],
    });
    const w = mountCustomTab({ initialRecipe: recipe });
    await w.vm.$nextTick();

    const idInput = w.find('[data-testid="custom-id-input"]').element as HTMLInputElement;
    const nameInput = w.find('[data-testid="custom-display-name-input"]').element as HTMLInputElement;
    expect(idInput.value).toBe('my-existing');
    expect(nameInput.value).toBe('My Existing Server');
  });

  it('Test Connection button is disabled when form is invalid', async () => {
    const w = mountCustomTab();
    // No id or command filled
    const testBtn = w.find('[data-testid="custom-test-btn"]');
    expect(testBtn.attributes('disabled')).toBeDefined();
  });

  it('Test Connection shows WP10 gap message', async () => {
    const w = mountCustomTab();
    await w.find('[data-testid="custom-id-input"]').setValue('my-server');
    await w.find('[data-testid="custom-display-name-input"]').setValue('My Server');
    await w.find('[data-testid="custom-command-input"]').setValue('npx');

    await w.find('[data-testid="custom-test-btn"]').trigger('click');
    await flushPromises();

    expect(w.find('[data-testid="custom-test-result"]').text()).toContain('WP10');
  });

  it('cancel button emits cancel', async () => {
    const w = mountCustomTab();
    await w.find('[data-testid="custom-cancel-btn"]').trigger('click');
    expect(w.emitted('cancel')).toBeTruthy();
  });
});
