import { describe, it, expect, vi, afterEach } from 'vitest';
import { mount, flushPromises } from '@vue/test-utils';
import AddMCPServerModal from '@/views/tools/AddMCPServerModal.vue';
import PasteConfigTab from '@/views/tools/PasteConfigTab.vue';
import CustomRecipeTab from '@/views/tools/CustomRecipeTab.vue';
import {
  createFakeHarnessClient,
  type HarnessClient,
} from '@/lib/harnessClient';
import { HarnessClientKey } from '@/lib/harnessClientContext';
import type { Recipe, MCPImportResponse } from '@/lib/types';

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

function mountModal(props: Record<string, unknown>, client?: Partial<HarnessClient>) {
  const fakeClient = createFakeHarnessClient(client);
  return mount(AddMCPServerModal, {
    props: { open: true, ...props },
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
  it('renders the two "Add your own" tabs and defaults to Paste config (the Registry browse tab is retired)', async () => {
    // install-framework-01DOGF0B WP04: browsing/installing catalog recipes
    // moved to the one list in CapabilitySurface.vue — there is exactly one
    // MCP browse-and-install path.
    const w = mountModal({});
    await flushPromises();

    expect(w.find('[data-testid="add-mcp-modal"]').exists()).toBe(true);
    expect(w.find('[data-testid="add-mcp-tab-registry"]').exists()).toBe(false);
    expect(w.find('[data-testid="add-mcp-tab-paste"]').exists()).toBe(true);
    expect(w.find('[data-testid="add-mcp-tab-custom"]').exists()).toBe(true);
    expect(w.find('[data-testid="paste-config-tab"]').exists()).toBe(true);
  });

  it('opens on the entry point it was launched from (initialTab)', async () => {
    const w = mountModal({ initialTab: 'custom' });
    await flushPromises();
    expect(w.find('[data-testid="custom-recipe-tab"]').exists()).toBe(true);
  });

  it('switches between Paste and Custom on click', async () => {
    const w = mountModal({});
    await flushPromises();

    await w.find('[data-testid="add-mcp-tab-custom"]').trigger('click');
    expect(w.find('[data-testid="custom-recipe-tab"]').exists()).toBe(true);
    expect(w.find('[data-testid="paste-config-tab"]').exists()).toBe(false);
    await w.find('[data-testid="add-mcp-tab-paste"]').trigger('click');
    expect(w.find('[data-testid="paste-config-tab"]').exists()).toBe(true);
  });

  it('starts on Custom tab when editRecipe is provided', async () => {
    const recipe = makeRecipe('my-recipe');
    const w = mountModal({ editRecipe: recipe });
    await flushPromises();

    expect(w.find('[data-testid="custom-recipe-tab"]').exists()).toBe(true);
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
    const w = mountPasteTab({ mcp: { listServers: vi.fn(async () => []), startStream: vi.fn(async () => 'sub'), stopStream: vi.fn(), importClaudeDesktopConfig: importFn } as any }) as any;

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
    const w = mountPasteTab({ mcp: { listServers: vi.fn(async () => []), startStream: vi.fn(async () => 'sub'), stopStream: vi.fn(), importClaudeDesktopConfig: importFn } as any }) as any;

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
    const w = mountPasteTab({ mcp: { listServers: vi.fn(async () => []), startStream: vi.fn(async () => 'sub'), stopStream: vi.fn(), importClaudeDesktopConfig: importFn } as any }) as any;

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
    const w = mountPasteTab({ mcp: { listServers: vi.fn(async () => []), startStream: vi.fn(async () => 'sub'), stopStream: vi.fn(), importClaudeDesktopConfig: importFn } as any }) as any;

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
    const w = mountPasteTab({ mcp: { listServers: vi.fn(async () => []), startStream: vi.fn(async () => 'sub'), stopStream: vi.fn(), importClaudeDesktopConfig: importFn } as any }) as any;

    await w.find('[data-testid="paste-config-textarea"]').setValue('{}');
    await w.find('[data-testid="paste-translate-btn"]').trigger('click');
    await flushPromises();

    expect(w.find('[data-testid="paste-entry-shadow-warning-brave-search"]').exists()).toBe(true);
    expect(w.find('[data-testid="paste-report-collision"]').exists()).toBe(true);
  });

  it('shows translate error on API failure', async () => {
    const importFn = vi.fn(async () => { throw new Error('Bad JSON'); });
    const w = mountPasteTab({ mcp: { listServers: vi.fn(async () => []), startStream: vi.fn(async () => 'sub'), stopStream: vi.fn(), importClaudeDesktopConfig: importFn } as any }) as any;

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

  it('Save calls client.mcp.saveCustomRecipe with the assembled payload and emits saved', async () => {
    // Mutation: restore the WP02-era `throw new Error(...)` stub in
    // save(). This assertion must fail — the throw never reaches
    // saveCustomRecipe, and the mock's call count stays 0.
    const saveCustomRecipe = vi.fn(async (req: any) => ({ id: req.id }));
    const w = mountCustomTab({}, { mcp: { saveCustomRecipe } as any });
    await w.find('[data-testid="custom-id-input"]').setValue('my-server');
    await w.find('[data-testid="custom-display-name-input"]').setValue('My Server');
    await w.find('[data-testid="custom-command-input"]').setValue('npx');
    await w.find('[data-testid="custom-args-input"]').setValue('-y foo');
    await flushPromises();

    await w.find('[data-testid="custom-save-btn"]').trigger('click');
    await flushPromises();

    expect(saveCustomRecipe).toHaveBeenCalledTimes(1);
    expect(saveCustomRecipe).toHaveBeenCalledWith({
      id: 'my-server',
      display_name: 'My Server',
      description: undefined,
      transport: 'stdio',
      command: ['npx', '-y', 'foo'],
    });
    expect(w.emitted('saved')).toBeTruthy();
    expect(w.find('[data-testid="custom-save-error"]').exists()).toBe(false);
  });

  it('Save surfaces a backend rejection as custom-save-error', async () => {
    const saveCustomRecipe = vi.fn(async () => {
      throw new Error('mcp: SaveCustomRecipe: recipe already exists');
    });
    const w = mountCustomTab({}, { mcp: { saveCustomRecipe } as any });
    await w.find('[data-testid="custom-id-input"]').setValue('my-server');
    await w.find('[data-testid="custom-display-name-input"]').setValue('My Server');
    await w.find('[data-testid="custom-command-input"]').setValue('npx');
    await flushPromises();

    await w.find('[data-testid="custom-save-btn"]').trigger('click');
    await flushPromises();

    expect(w.find('[data-testid="custom-save-error"]').text()).toContain(
      'recipe already exists',
    );
    expect(w.emitted('saved')).toBeFalsy();
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

  it('Test Connection on an UNSAVED draft asks the user to save, and calls nothing', async () => {
    // The draft path has no id on disk, so MCP_TestRecipe has nothing to
    // resolve. Assert on the recorded call (not just the rendered string):
    // the client must not be touched at all.
    const testRecipe = vi.fn();
    const w = mountCustomTab({}, { mcp: { testRecipe } as any });
    await w.find('[data-testid="custom-id-input"]').setValue('my-server');
    await w.find('[data-testid="custom-display-name-input"]').setValue('My Server');
    await w.find('[data-testid="custom-command-input"]').setValue('npx');

    await w.find('[data-testid="custom-test-btn"]').trigger('click');
    await flushPromises();

    expect(testRecipe).not.toHaveBeenCalled();
    expect(w.find('[data-testid="custom-test-result"]').exists()).toBe(false);
    expect(w.find('[data-testid="custom-test-error"]').text()).toContain(
      'Save this recipe first',
    );
  });

  it('Test Connection on the EDIT path issues a real MCP_TestRecipe call and renders the result', async () => {
    // AC-002: this is the flow a user actually takes — the row Edit button
    // lands here with initialRecipe set, and that id IS persisted. Before
    // this fix testConnection() short-circuited to an "unavailable for an
    // unsaved draft" error on every path including this one, so the button
    // could never succeed in any flow.
    //
    // Mutation: make testConnection() unconditionally assign testError
    // without consulting canTest (its pre-fix shape). This assertion must
    // fail — testRecipe's call count stays 0.
    const testRecipe = vi.fn(async () => ({
      ok: true,
      server_info: { name: 'My Existing Server', version: '1.0' },
      capabilities: { tools: { listChanged: false } },
      tool_count: 3,
      resource_count: -1,
      prompt_count: -1,
      duration_ms: 42,
    }));
    const recipe = makeRecipe('my-existing', {
      displayName: 'My Existing Server',
      argsTemplate: ['npx', '-y', 'thing'],
    });
    const w = mountCustomTab(
      { initialRecipe: recipe },
      { mcp: { testRecipe } as any },
    );
    await flushPromises();

    await w.find('[data-testid="custom-test-btn"]').trigger('click');
    await flushPromises();

    expect(testRecipe).toHaveBeenCalledTimes(1);
    expect(testRecipe).toHaveBeenCalledWith('my-existing', {}, {});
    expect(w.find('[data-testid="custom-test-error"]').exists()).toBe(false);
    expect(w.find('[data-testid="custom-test-result"]').text()).toContain(
      '3 tool(s)',
    );
  });

  it('Test Connection surfaces a failed round-trip as an error, not a success string', async () => {
    const testRecipe = vi.fn(async () => ({
      ok: false,
      server_info: { name: '', version: '' },
      capabilities: {},
      tool_count: -1,
      resource_count: -1,
      prompt_count: -1,
      duration_ms: 12,
      error: 'exec: "npx": executable file not found',
    }));
    const recipe = makeRecipe('my-existing', {
      displayName: 'My Existing Server',
      argsTemplate: ['npx'],
    });
    const w = mountCustomTab(
      { initialRecipe: recipe },
      { mcp: { testRecipe } as any },
    );
    await flushPromises();

    await w.find('[data-testid="custom-test-btn"]').trigger('click');
    await flushPromises();

    expect(w.find('[data-testid="custom-test-result"]').exists()).toBe(false);
    expect(w.find('[data-testid="custom-test-error"]').text()).toContain(
      'executable file not found',
    );
  });

  it('Test Connection becomes live after a successful save, without leaving the form', async () => {
    const testRecipe = vi.fn(async () => ({
      ok: true,
      server_info: { name: 'My Server', version: '1.0' },
      capabilities: {},
      tool_count: 1,
      resource_count: -1,
      prompt_count: -1,
      duration_ms: 7,
    }));
    const saveCustomRecipe = vi.fn(async (req: any) => ({ id: req.id }));
    const w = mountCustomTab({}, { mcp: { saveCustomRecipe, testRecipe } as any });
    await w.find('[data-testid="custom-id-input"]').setValue('my-server');
    await w.find('[data-testid="custom-display-name-input"]').setValue('My Server');
    await w.find('[data-testid="custom-command-input"]').setValue('npx');
    await flushPromises();

    await w.find('[data-testid="custom-save-btn"]').trigger('click');
    await flushPromises();

    await w.find('[data-testid="custom-test-btn"]').trigger('click');
    await flushPromises();

    expect(testRecipe).toHaveBeenCalledWith('my-server', {}, {});
    expect(w.find('[data-testid="custom-test-result"]').text()).toContain('1 tool(s)');
  });

  it('cancel button emits cancel', async () => {
    const w = mountCustomTab();
    await w.find('[data-testid="custom-cancel-btn"]').trigger('click');
    expect(w.emitted('cancel')).toBeTruthy();
  });
});
