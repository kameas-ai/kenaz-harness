/**
 * CapabilitySurface — the one "Add capability" list + detail surface
 * (install-framework-01DOGF0B WP04).
 *
 * Pins:
 *   P-3 (mcp, UI half): the installed badge is whatever Capability_List's
 *        consumer-derived state says; a zero-input Install goes through
 *        Capability_Install and the list repaints from the consumer; a
 *        capability:installed / capability:uninstalled event (from ANY path)
 *        repaints it too. (Go half: TestInstallProvider_MCPRecipe_ConsumerSeesInstall.)
 *   P-5: signed out — local providers stay fully usable, and a fleet source
 *        the backend could not list renders as a reason row, never a hidden tab.
 *   P-6 (mcp flows): every per-kind MCP flow FR-4 lists is reachable from
 *        this surface — key/OAuth prompt, directory picker, paste config,
 *        custom recipe, built-in toggles (no-install home), org read-only
 *        rows. Per-server policy is pinned in ToolsView.test.ts (it sits
 *        under the surface in the same view).
 *
 * In-memory fake client, DELIBERATELY (WP-PI AC-PI-2): these pin which RPCs
 * the UI calls, not storage; the consumer round trip is the Go test above.
 */
import { describe, it, expect, vi } from 'vitest';
import { mount, flushPromises } from '@vue/test-utils';
import { defineComponent } from 'vue';
import { createMemoryHistory, createRouter } from 'vue-router';
import CapabilitySurface from '@/views/capabilities/CapabilitySurface.vue';
import RecipeKeyPromptModal from '@/views/tools/RecipeKeyPromptModal.vue';
import DirectoryPicker from '@/views/tools/DirectoryPicker.vue';
import PasteConfigTab from '@/views/tools/PasteConfigTab.vue';
import CustomRecipeTab from '@/views/tools/CustomRecipeTab.vue';
import { createFakeHarnessClient, type HarnessClient } from '@/lib/harnessClient';
import { HarnessClientKey } from '@/lib/harnessClientContext';
import { dispatchServedEvent } from '@/lib/useServedEvents';
import type {
  CapabilityItem,
  CapabilityListing,
  Recipe,
  RecipeListing,
  RecipeStatus,
} from '@/lib/types';

function recipe(id: string, overrides: Partial<Recipe> = {}): Recipe {
  return {
    id,
    displayName: id,
    description: `Recipe ${id}`,
    category: 'search',
    envKeys: [],
    capabilities: { tools: true, resources: false, prompts: false, sampling: false },
    docsUrl: '',
    ...overrides,
  };
}

function status(id: string, overrides: Partial<RecipeStatus> = {}): RecipeStatus {
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

function mcpItem(id: string, overrides: Partial<CapabilityItem> = {}): CapabilityItem {
  return {
    kind: 'mcp_recipe',
    id,
    name: id,
    description: `Recipe ${id}`,
    category: 'search',
    source: 'builtin',
    state: { installed: false, consumer: 'MCP supervisor' },
    requirements: [],
    ...overrides,
  };
}

interface Setup {
  client: HarnessClient;
  list: ReturnType<typeof vi.fn>;
  install: ReturnType<typeof vi.fn>;
  recipeInstall: ReturnType<typeof vi.fn>;
}

function setup(listings: CapabilityListing[], recipes: RecipeListing[] = []): Setup {
  let call = 0;
  const list = vi.fn(async () => {
    const l = listings[Math.min(call, listings.length - 1)];
    call++;
    return l;
  });
  const install = vi.fn(async (_kind: string, id: string) => mcpItem(id, { state: { installed: true } }));
  const recipeInstall = vi.fn(async (id: string) => status(id, { enabled: true, state: 'running' }));
  const base = createFakeHarnessClient();
  const client = createFakeHarnessClient({
    capabilities: { ...base.capabilities, list, install },
    tools: {
      ...base.tools,
      recipes: {
        ...base.tools.recipes,
        list: vi.fn(async () => recipes),
        install: recipeInstall,
        checkPrereqs: vi.fn(async () => []),
        config: vi.fn(async () => ({})),
      },
    } as any,
  });
  return { client, list, install, recipeInstall };
}

const router = () =>
  createRouter({
    history: createMemoryHistory(),
    routes: [
      { path: '/', component: defineComponent({ render: () => null }) },
      { path: '/knowledge/learned', component: defineComponent({ render: () => null }) },
    ],
  });

async function mountSurface(s: Setup) {
  const r = router();
  await r.push('/');
  await r.isReady();
  const w = mount(CapabilitySurface, {
    global: { plugins: [r], provide: { [HarnessClientKey as symbol]: s.client } },
    attachTo: document.body,
  });
  await flushPromises();
  return { w, r };
}

describe('CapabilitySurface — list (P-3, UI half)', () => {
  it('paints the installed badge from Capability_List consumer state, installed rows first', async () => {
    const s = setup([
      {
        items: [
          mcpItem('zeta', { state: { installed: true, consumer: 'MCP supervisor' } }),
          mcpItem('alpha'),
        ],
      },
    ]);
    const { w } = await mountSurface(s);
    expect(w.get('[data-testid=capability-state-mcp_recipe-zeta]').text()).toBe('Installed');
    expect(w.get('[data-testid=capability-state-mcp_recipe-alpha]').text()).toBe('Not installed');
    const order = w.findAll('[data-testid^=capability-row-mcp_recipe-]').map((r) => r.attributes('data-testid'));
    expect(order).toEqual(['capability-row-mcp_recipe-zeta', 'capability-row-mcp_recipe-alpha']);
    // An installed row offers no Install button.
    expect(w.find('[data-testid=capability-install-mcp_recipe-zeta]').exists()).toBe(false);
    w.unmount();
  });

  it('a zero-input Install goes through Capability_Install and repaints from the consumer', async () => {
    const s = setup([
      { items: [mcpItem('fetch')] },
      { items: [mcpItem('fetch', { state: { installed: true } })] },
    ]);
    const { w } = await mountSurface(s);
    await w.get('[data-testid=capability-install-mcp_recipe-fetch]').trigger('click');
    await flushPromises();
    expect(s.install).toHaveBeenCalledWith('mcp_recipe', 'fetch', '');
    expect(s.recipeInstall).not.toHaveBeenCalled();
    expect(w.get('[data-testid=capability-state-mcp_recipe-fetch]').text()).toBe('Installed');
    w.unmount();
  });

  it('an install error renders on the row', async () => {
    const s = setup([{ items: [mcpItem('fetch')] }]);
    s.install.mockRejectedValueOnce(new Error('install: the install did not reach its consumer'));
    const { w } = await mountSurface(s);
    await w.get('[data-testid=capability-install-mcp_recipe-fetch]').trigger('click');
    await flushPromises();
    expect(w.get('[data-testid=capability-row-error-mcp_recipe-fetch]').text()).toContain('did not reach its consumer');
    w.unmount();
  });

  it('capability:installed and capability:uninstalled from any path repaint the list', async () => {
    const s = setup([
      { items: [mcpItem('fetch')] },
      { items: [mcpItem('fetch', { state: { installed: true } })] },
      { items: [mcpItem('fetch')] },
    ]);
    const { w } = await mountSurface(s);
    expect(s.list).toHaveBeenCalledTimes(1);
    dispatchServedEvent('capability:installed', { kind: 'mcp_recipe', id: 'fetch', installed: true, via: 'flow', verified: false });
    await flushPromises();
    expect(s.list).toHaveBeenCalledTimes(2);
    expect(w.get('[data-testid=capability-state-mcp_recipe-fetch]').text()).toBe('Installed');
    dispatchServedEvent('capability:uninstalled', { kind: 'mcp_recipe', id: 'fetch', installed: false, via: 'uninstall', verified: false });
    await flushPromises();
    expect(w.get('[data-testid=capability-state-mcp_recipe-fetch]').text()).toBe('Not installed');
    w.unmount();
  });

  it('search reaches keywords (recipe aliases); source and kind chips filter', async () => {
    const s = setup([
      {
        items: [
          mcpItem('fetch', { keywords: ['http-get'], source: 'builtin' }),
          mcpItem('brave', { source: 'registry' }),
        ],
      },
    ]);
    const { w } = await mountSurface(s);
    await w.get('[data-testid=capability-search]').setValue('http-get');
    expect(w.findAll('[data-testid^=capability-row-]').map((r) => r.attributes('data-testid'))).toEqual([
      'capability-row-mcp_recipe-fetch',
    ]);
    await w.get('[data-testid=capability-search]').setValue('');
    await w.get('[data-testid=capability-source-chip-registry]').trigger('click');
    expect(w.find('[data-testid=capability-row-mcp_recipe-brave]').exists()).toBe(true);
    expect(w.find('[data-testid=capability-row-mcp_recipe-fetch]').exists()).toBe(false);
    // Built-in tools are source=builtin: hidden under the Registry chip.
    expect(w.find('[data-testid=websearch-tool-row]').exists()).toBe(false);
    await w.get('[data-testid=capability-source-chip-all]').trigger('click');
    await w.get('[data-testid=capability-kind-chip-builtin]').trigger('click');
    expect(w.find('[data-testid=websearch-tool-row]').exists()).toBe(true);
    expect(w.find('[data-testid^=capability-row-mcp_recipe-]').exists()).toBe(false);
    w.unmount();
  });
});

describe('CapabilitySurface — signed out (P-5)', () => {
  it('local providers stay usable and an unreachable fleet source is a reason row, not a hidden tab', async () => {
    const s = setup([
      {
        items: [mcpItem('fetch')],
        unavailable: [{ kind: 'mcp_recipe', source: 'org_catalog', reason: 'signed_out', message: 'not signed in' }],
      },
    ]);
    const { w } = await mountSurface(s);
    const row = w.get('[data-testid=capability-unavailable-mcp_recipe-org_catalog]');
    expect(row.text()).toContain('Org catalog');
    expect(row.text()).toContain('Sign in');
    // The Org catalog chip is still offered — never hidden.
    expect(w.find('[data-testid=capability-source-chip-org_catalog]').exists()).toBe(true);
    // Local MCP install still works signed out.
    await w.get('[data-testid=capability-install-mcp_recipe-fetch]').trigger('click');
    await flushPromises();
    expect(s.install).toHaveBeenCalledWith('mcp_recipe', 'fetch', '');
    w.unmount();
  });

  it('a provider that failed outright renders its error as a row', async () => {
    const s = setup([{ items: [], unavailable: [{ kind: 'mcp_recipe', source: '', reason: 'error', message: 'tools: no catalog configured' }] }]);
    const { w } = await mountSurface(s);
    expect(w.get('[data-testid=capability-unavailable-mcp_recipe-all]').text()).toContain('tools: no catalog configured');
    w.unmount();
  });
});

describe('CapabilitySurface — per-kind MCP flows are reachable (P-6)', () => {
  it('key / OAuth prompt: Install on a row with unmet requirements opens RecipeKeyPromptModal, not a blind install', async () => {
    const r = recipe('brave', {
      envKeys: [{ name: 'BRAVE_API_KEY', display: 'Brave API key', docsUrl: '', required: true }],
    });
    const s = setup(
      [
        {
          items: [
            mcpItem('brave', {
              requirements: [{ kind: 'key', name: 'BRAVE_API_KEY', display: 'Brave API key', required: true, satisfied: false }],
            }),
          ],
        },
      ],
      [{ recipe: r, enabled: false, keysPresent: false, status: status('brave'), source: 'registry' }],
    );
    const { w } = await mountSurface(s);
    await w.get('[data-testid=capability-install-mcp_recipe-brave]').trigger('click');
    await flushPromises();
    const modal = w.findComponent(RecipeKeyPromptModal);
    expect(modal.exists()).toBe(true);
    expect((modal.props('recipe') as Recipe).id).toBe('brave');
    expect(s.install).not.toHaveBeenCalled();
    w.unmount();
  });

  it('directory picker: a directory requirement routes to the key prompt, which renders DirectoryPicker', async () => {
    const r = recipe('filesystem', {
      category: 'filesystem',
      configOptions: [
        { name: 'allowed_directories', display: 'Allowed directories', kind: 'directory_list', required: true, description: '' },
      ],
    });
    const s = setup(
      [
        {
          items: [
            mcpItem('filesystem', {
              category: 'filesystem',
              requirements: [{ kind: 'directory', name: 'allowed_directories', required: true, satisfied: false }],
            }),
          ],
        },
      ],
      [{ recipe: r, enabled: false, keysPresent: true, status: status('filesystem'), source: 'shipped' }],
    );
    const { w } = await mountSurface(s);
    await w.get('[data-testid=capability-install-mcp_recipe-filesystem]').trigger('click');
    await flushPromises();
    expect(w.findComponent(DirectoryPicker).exists()).toBe(true);
    w.unmount();
  });

  it('paste config and custom recipe are "Add your own" entry points', async () => {
    const s = setup([{ items: [] }]);
    const { w } = await mountSurface(s);
    await w.get('[data-testid=capability-entry-mcp-paste]').trigger('click');
    await flushPromises();
    expect(w.findComponent(PasteConfigTab).exists()).toBe(true);
    await w.get('[data-testid=add-mcp-modal-close]').trigger('click');
    await flushPromises();
    await w.get('[data-testid=capability-entry-mcp-custom]').trigger('click');
    await flushPromises();
    expect(w.findComponent(CustomRecipeTab).exists()).toBe(true);
    // The retired Registry browse tab is gone from the modal.
    expect(w.find('[data-testid=add-mcp-tab-registry]').exists()).toBe(false);
    w.unmount();
  });

  it('org-provisioned rows are read-only in the detail pane', async () => {
    const r = recipe('org-wiki');
    const s = setup(
      [{ items: [mcpItem('org-wiki', { source: 'org_catalog', read_only: true, read_only_reason: 'Provisioned by your org', state: { installed: true } })] }],
      [{ recipe: r, enabled: true, keysPresent: true, status: status('org-wiki', { enabled: true, state: 'running' }), source: 'org' }],
    );
    const { w } = await mountSurface(s);
    await w.get('[data-testid=capability-row-mcp_recipe-org-wiki] button').trigger('click');
    await flushPromises();
    expect(w.get('[data-testid=recipe-org-badge-org-wiki]').text()).toContain('Provisioned by your org');
    expect(w.find('[data-testid=recipe-delete-btn-org-wiki]').exists()).toBe(false);
    w.unmount();
  });

  it('long tail: browse automation filters to that category; request-a-connector opens the issue form', async () => {
    const s = setup([{ items: [mcpItem('zapier', { category: 'automation' }), mcpItem('fetch')] }]);
    const open = vi.fn();
    s.client.openExternalURL = open;
    const { w } = await mountSurface(s);
    await w.get('[data-testid=capability-browse-automation]').trigger('click');
    expect(w.find('[data-testid=capability-row-mcp_recipe-zapier]').exists()).toBe(true);
    expect(w.find('[data-testid=capability-row-mcp_recipe-fetch]').exists()).toBe(false);
    await w.get('[data-testid=capability-search]').setValue('linear');
    await w.get('[data-testid=capability-request-connector]').trigger('click');
    expect(open).toHaveBeenCalledWith(expect.stringContaining('linear'));
    w.unmount();
  });
});

describe('CapabilitySurface — built-in tools keep a no-install home (FR-4)', () => {
  it('renders a toggle row per built-in, with KenazToolsPanel defaults (save artifact on when the read fails)', async () => {
    const s = setup([{ items: [] }]);
    s.client.settings.getSaveArtifact = async () => {
      throw new Error('store glitch');
    };
    const { w } = await mountSurface(s);
    for (const id of ['websearch', 'webfetch', 'bash', 'saveartifact', 'fs-read', 'fs-write', 'todo']) {
      expect(w.find(`[data-testid=${id}-tool-row]`).exists()).toBe(true);
      expect(w.find(`[data-testid=${id}-toggle]`).exists()).toBe(true);
    }
    expect((w.get('[data-testid=fs-read-toggle]').element as HTMLInputElement).checked).toBe(false);
    expect((w.get('[data-testid=saveartifact-toggle]').element as HTMLInputElement).checked).toBe(true);
    // A toggle, not an install: no Install button on a built-in row.
    expect(w.get('[data-testid=fs-read-tool-row]').text()).not.toContain('Install');
    w.unmount();
  });

  it('toggling calls the settings setter; a failure reverts and shows the error on the row', async () => {
    const s = setup([{ items: [] }]);
    const setFSReadEnabled = vi.fn(async () => undefined);
    s.client.settings.setFSReadEnabled = setFSReadEnabled;
    s.client.settings.setFSWriteEnabled = vi.fn(async () => {
      throw new Error('store write failed');
    });
    s.client.settings.getFSWriteEnabled = async () => false;
    const { w } = await mountSurface(s);

    const read = w.get('[data-testid=fs-read-toggle]');
    (read.element as HTMLInputElement).checked = true;
    await read.trigger('change');
    await flushPromises();
    expect(setFSReadEnabled).toHaveBeenCalledWith(true);

    const write = w.get('[data-testid=fs-write-toggle]');
    (write.element as HTMLInputElement).checked = true;
    await write.trigger('change');
    await flushPromises();
    expect((write.element as HTMLInputElement).checked).toBe(false);
    expect(w.get('[data-testid=fs-write-tool-row]').text()).toContain('store write failed');
    w.unmount();
  });

  it('reflects the stored value and lists the gated tools in the detail pane', async () => {
    const s = setup([{ items: [] }]);
    s.client.settings.getFSReadEnabled = async () => true;
    const { w } = await mountSurface(s);
    expect((w.get('[data-testid=fs-read-toggle]').element as HTMLInputElement).checked).toBe(true);
    await w.get('[data-testid=fs-read-tool-row] button').trigger('click');
    const detail = w.get('[data-testid=builtin-detail-fs-read]').text();
    for (const t of ['kenaz__read_file', 'kenaz__list_dir', 'kenaz__glob', 'kenaz__grep', 'kenaz__list_open_worklist']) {
      expect(detail).toContain(t);
    }
    w.unmount();
  });

  it('carries the memory pointer to Knowledge › Learned (no memory switch here)', async () => {
    const s = setup([{ items: [] }]);
    const { w, r } = await mountSurface(s);
    expect(w.find('[aria-label="Enable memory tool"]').exists()).toBe(false);
    expect(w.get('[data-testid=memory-moved-pointer]').text()).toContain('Knowledge › Learned');
    await w.get('[data-testid=memory-view-link]').trigger('click');
    await flushPromises();
    expect(r.currentRoute.value.path).toBe('/knowledge/learned');
    w.unmount();
  });

  it('uses only design tokens — no raw hex / rgba in markup', async () => {
    const s = setup([{ items: [mcpItem('fetch')] }]);
    const { w } = await mountSurface(s);
    expect(w.html()).not.toMatch(/#[0-9a-fA-F]{3,8}\b/);
    expect(w.html()).not.toMatch(/rgba?\s*\(/i);
    w.unmount();
  });
});
