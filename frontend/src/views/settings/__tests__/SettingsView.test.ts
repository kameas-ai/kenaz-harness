import { describe, it, expect, vi } from 'vitest';
import { mount, flushPromises } from '@vue/test-utils';
import SettingsView from '@/views/settings/SettingsView.vue';
import { createFakeHarnessClient } from '@/lib/harnessClient';
import { HarnessClientKey } from '@/lib/harnessClientContext';
import type { Attachment, MemoryEmbedderEligibility, Settings, Theme } from '@/lib/types';

function provide(
  overrides: Partial<Settings> = {},
  attachmentRows: Attachment[] = [],
  attachmentsListSpy?: ReturnType<typeof vi.fn>,
) {
  const settings: Settings = {
    schemaVersion: 1,
    lastRoute: '/sessions',
    theme: 'dark',
    accent: 'default',
    windowSize: { width: 1280, height: 800 },
    ...overrides,
  };
  const saveTheme = vi.fn().mockResolvedValue(undefined);
  const set = vi.fn().mockResolvedValue(undefined);
  const client = createFakeHarnessClient({
    settings: {
      get: async () => settings,
      set,
      loadRoute: async () => settings.lastRoute,
      saveRoute: async () => undefined,
      logRouteChange: async () => undefined,
      loadTheme: async () => settings.theme,
      saveTheme,
      getMemory: async () => false,
      setMemory: async () => undefined,
      getWebFetchEnabled: async () => false,
      setWebFetchEnabled: async () => undefined,
    } as any,
    appInfo: async () => ({
      build: '0.1.0-test',
      commit: 'abcdef0',
      buildTime: '2026-04-25T00:00:00Z',
      goVersion: 'go1.23.0',
      platform: 'darwin/arm64',
      windowSize: settings.windowSize,
    }),
    attachments: {
      list: attachmentsListSpy ?? (async () => [...attachmentRows]),
      listResolved: async () => [],
      add: async () => attachmentRows[0] ?? ({} as Attachment),
      remove: async () => undefined,
      reorder: async () => undefined,
      refresh: async () => attachmentRows[0] ?? ({} as Attachment),
    } as any,
  });
  return { client, saveTheme, set };
}

describe('SettingsView (FR-001b numbered-section header)', () => {
  it('renders the canvas head with section number 06', async () => {
    const { client } = provide();
    const w = mount(SettingsView, {
      global: { provide: { [HarnessClientKey as symbol]: client } },
    });
    await flushPromises();
    expect(w.text()).toContain('06');
    expect(w.text()).toContain('SETTINGS');
    expect(w.text()).toContain('App preferences');
  });

  it('exposes a theme selector with system / dark / light options', async () => {
    const { client } = provide();
    const w = mount(SettingsView, {
      global: { provide: { [HarnessClientKey as symbol]: client } },
    });
    await flushPromises();
    expect(w.text()).toContain('System');
    expect(w.text()).toContain('Dark');
    expect(w.text()).toContain('Light');
  });

  it('persists a theme change via saveTheme', async () => {
    const { client, saveTheme } = provide();
    const w = mount(SettingsView, {
      global: { provide: { [HarnessClientKey as symbol]: client } },
    });
    await flushPromises();
    const buttons = w.findAll('button[role=radio]');
    const lightBtn = buttons.find((b) => b.text().includes('Light'));
    expect(lightBtn).toBeTruthy();
    await lightBtn!.trigger('click');
    await flushPromises();
    expect(saveTheme).toHaveBeenCalledWith<Theme[]>('light');
  });

  it('renders harness build info from AppInfo', async () => {
    const { client } = provide();
    const w = mount(SettingsView, {
      global: { provide: { [HarnessClientKey as symbol]: client } },
    });
    await flushPromises();
    expect(w.text()).toContain('0.1.0-test');
    expect(w.text()).toContain('abcdef0');
    expect(w.text()).toContain('darwin/arm64');
  });

  it('renders the schema-version + last-route row (privacy invariant #5)', async () => {
    const { client } = provide({ lastRoute: '/audit' });
    const w = mount(SettingsView, {
      global: { provide: { [HarnessClientKey as symbol]: client } },
    });
    await flushPromises();
    expect(w.text()).toContain('Schema version');
    expect(w.text()).toContain('/audit');
  });

  it('uses only design tokens — no raw hex/rgba', async () => {
    const { client } = provide();
    const w = mount(SettingsView, {
      global: { provide: { [HarnessClientKey as symbol]: client } },
    });
    await flushPromises();
    const html = w.html();
    expect(html).not.toMatch(/#[0-9a-fA-F]{3,8}\b/);
    expect(html).not.toMatch(/rgba?\s*\(/i);
  });

  it('renders the global-context section and lists global-scope attachments (A3)', async () => {
    const rows: Attachment[] = [
      {
        id: 'g-1',
        scopeKind: 'global',
        scopeId: '',
        contentSource: 'library:always-read.md',
        content: 'always-read body',
        kind: 'system',
        position: 0,
        createdAt: '',
      },
    ];
    const list = vi.fn(async (filter: { scopeKind: string; scopeId?: string }) => {
      expect(filter.scopeKind).toBe('global');
      return [...rows];
    });
    const { client } = provide({}, rows, list);
    const w = mount(SettingsView, {
      global: { provide: { [HarnessClientKey as symbol]: client } },
    });
    await flushPromises();
    expect(list).toHaveBeenCalled();
    const section = w.find('[data-testid=global-context-section]');
    expect(section.exists()).toBe(true);
    expect(w.find('[data-testid=global-attachments-list]').exists()).toBe(
      true,
    );
    expect(w.text()).toContain('always-read.md');
    expect(w.find('[data-testid=global-add-context]').exists()).toBe(true);
  });

  it('renders the global-context empty hint when no global attachments exist', async () => {
    const { client } = provide({}, []);
    const w = mount(SettingsView, {
      global: { provide: { [HarnessClientKey as symbol]: client } },
    });
    await flushPromises();
    expect(
      w.find('[data-testid=global-attachments-empty]').exists(),
    ).toBe(true);
  });

  // ── WP05: auto-title toggle ───────────────────────────────────────────

  it('renders the auto-title toggle (data-testid="auto-title-toggle")', async () => {
    const { client } = provide();
    const w = mount(SettingsView, {
      global: { provide: { [HarnessClientKey as symbol]: client } },
    });
    await flushPromises();
    expect(w.find('[data-testid="auto-title-toggle"]').exists()).toBe(true);
  });

  it('auto-title toggle is checked by default (autoTitleEnabled=true)', async () => {
    const getAutoTitleEnabled = vi.fn().mockResolvedValue(true);
    const { client: baseClient } = provide();
    const client = {
      ...baseClient,
      settings: {
        ...baseClient.settings,
        getAutoTitleEnabled,
        setAutoTitleEnabled: vi.fn().mockResolvedValue(undefined),
      },
    };
    const w = mount(SettingsView, {
      global: { provide: { [HarnessClientKey as symbol]: client } },
    });
    await flushPromises();
    const toggle = w.find<HTMLInputElement>('[data-testid="auto-title-toggle"]');
    expect((toggle.element as HTMLInputElement).checked).toBe(true);
  });

  it('calls setAutoTitleEnabled(false) when user unchecks the toggle', async () => {
    const setAutoTitleEnabled = vi.fn().mockResolvedValue(undefined);
    const { client: baseClient } = provide();
    const client = {
      ...baseClient,
      settings: {
        ...baseClient.settings,
        getAutoTitleEnabled: vi.fn().mockResolvedValue(true),
        setAutoTitleEnabled,
      },
    };
    const w = mount(SettingsView, {
      global: { provide: { [HarnessClientKey as symbol]: client } },
    });
    await flushPromises();
    await w.find('[data-testid="auto-title-toggle"]').trigger('change');
    await flushPromises();
    expect(setAutoTitleEnabled).toHaveBeenCalledWith(false);
  });

  // ── system-prompt-layers WP04: chat custom instructions ───────────────

  it('renders the chat custom-instructions textarea', async () => {
    const { client } = provide();
    const w = mount(SettingsView, {
      global: { provide: { [HarnessClientKey as symbol]: client } },
    });
    await flushPromises();
    expect(w.find('[data-testid="chat-custom-instructions-section"]').exists()).toBe(true);
    expect(w.find('[data-testid="chat-custom-instructions-input"]').exists()).toBe(true);
  });

  it('loads the persisted custom-instructions value into the textarea', async () => {
    const getChatCustomInstructions = vi.fn().mockResolvedValue('Answer in British English.');
    const { client: baseClient } = provide();
    const client = {
      ...baseClient,
      settings: {
        ...baseClient.settings,
        getChatCustomInstructions,
        setChatCustomInstructions: vi.fn().mockResolvedValue(undefined),
      },
    };
    const w = mount(SettingsView, {
      global: { provide: { [HarnessClientKey as symbol]: client } },
    });
    await flushPromises();
    const ta = w.find<HTMLTextAreaElement>('[data-testid="chat-custom-instructions-input"]');
    expect((ta.element as HTMLTextAreaElement).value).toBe('Answer in British English.');
  });

  it('persists the custom-instructions value on change (round-trip)', async () => {
    const setChatCustomInstructions = vi.fn().mockResolvedValue(undefined);
    const { client: baseClient } = provide();
    const client = {
      ...baseClient,
      settings: {
        ...baseClient.settings,
        getChatCustomInstructions: vi.fn().mockResolvedValue(''),
        setChatCustomInstructions,
      },
    };
    const w = mount(SettingsView, {
      global: { provide: { [HarnessClientKey as symbol]: client } },
    });
    await flushPromises();
    const ta = w.find<HTMLTextAreaElement>('[data-testid="chat-custom-instructions-input"]');
    await ta.setValue('Prefer tables over prose.');
    await ta.trigger('change');
    await flushPromises();
    expect(setChatCustomInstructions).toHaveBeenCalledWith('Prefer tables over prose.');
  });
});

// ── v0.5.5: no-eligible-embedder-provider banner ──────────────────────────

describe('SettingsView — no-eligible-embedder-provider banner', () => {
  /**
   * Build a client with a custom embedderEligibility implementation and
   * all other minimal stubs satisfied.
   */
  function buildClient(eligibility: MemoryEmbedderEligibility) {
    const { client: base } = provide();
    return {
      ...base,
      memory: {
        ...base.memory,
        embedderEligibility: vi.fn().mockResolvedValue(eligibility),
      },
    };
  }

  it('hides the banner when eligibility.hasEligible is true', async () => {
    const client = buildClient({
      hasEligible: true,
      allProfiles: 1,
      eligibleProfiles: 1,
      skippedKinds: [],
    });
    const w = mount(SettingsView, {
      global: { provide: { [HarnessClientKey as symbol]: client } },
    });
    await flushPromises();
    expect(w.find('[data-testid="no-embedder-provider-banner"]').exists()).toBe(false);
  });

  it('shows the banner when eligibility.hasEligible is false', async () => {
    const client = buildClient({
      hasEligible: false,
      allProfiles: 1,
      eligibleProfiles: 0,
      skippedKinds: ['anthropic'],
    });
    const w = mount(SettingsView, {
      global: { provide: { [HarnessClientKey as symbol]: client } },
    });
    await flushPromises();
    const banner = w.find('[data-testid="no-embedder-provider-banner"]');
    expect(banner.exists()).toBe(true);
    expect(banner.text()).toContain('No memory provider configured');
  });

  it('renders skipped kinds in the banner', async () => {
    const client = buildClient({
      hasEligible: false,
      allProfiles: 2,
      eligibleProfiles: 0,
      skippedKinds: ['anthropic', 'bedrock'],
    });
    const w = mount(SettingsView, {
      global: { provide: { [HarnessClientKey as symbol]: client } },
    });
    await flushPromises();
    const list = w.find('[data-testid="no-embedder-skipped-kinds"]');
    expect(list.exists()).toBe(true);
    expect(list.text()).toContain('Anthropic');
    expect(list.text()).toContain('Bedrock');
  });

  it('renders the "Add OpenRouter provider" button when no eligible provider', async () => {
    const client = buildClient({
      hasEligible: false,
      allProfiles: 1,
      eligibleProfiles: 0,
      skippedKinds: ['anthropic'],
    });
    const w = mount(SettingsView, {
      global: { provide: { [HarnessClientKey as symbol]: client } },
    });
    await flushPromises();
    expect(
      w.find('[data-testid="no-embedder-add-openrouter"]').exists(),
    ).toBe(true);
    expect(
      w.find('[data-testid="no-embedder-see-all-providers"]').exists(),
    ).toBe(true);
  });

  it('does not show banner when no profiles exist (allProfiles=0, hasEligible=true default)', async () => {
    // The fake client returns hasEligible=true by default — no banner shown.
    const { client } = provide();
    const w = mount(SettingsView, {
      global: { provide: { [HarnessClientKey as symbol]: client } },
    });
    await flushPromises();
    expect(w.find('[data-testid="no-embedder-provider-banner"]').exists()).toBe(false);
  });
});

// ── model-authored-graphs-01PMGA01 UNIT-6: the FR-006 consent dial ──────
//
// No dedicated Settings_Get/SetGraphAuthoringEnabled binding exists — the
// toggle reads and writes through the generic Settings_Get/Settings_Set
// round trip, exactly like autoCollapseBranchesInSidebar. These tests
// pin that the control reflects the REAL persisted value (not a local
// default) and writes through client.settings.set, per AC-009.
describe('SettingsView — graph authoring consent dial (UNIT-6, AC-009)', () => {
  it('defaults the toggle off when graphAuthoringEnabled is absent', async () => {
    const { client } = provide();
    const w = mount(SettingsView, {
      global: { provide: { [HarnessClientKey as symbol]: client } },
    });
    await flushPromises();
    const toggle = w.get('[data-testid="graph-authoring-enabled-toggle"]');
    expect(toggle.attributes('aria-checked')).toBe('false');
  });

  it('reflects a persisted graphAuthoringEnabled=true rather than a local default', async () => {
    const { client } = provide({ graphAuthoringEnabled: true });
    const w = mount(SettingsView, {
      global: { provide: { [HarnessClientKey as symbol]: client } },
    });
    await flushPromises();
    const toggle = w.get('[data-testid="graph-authoring-enabled-toggle"]');
    expect(toggle.attributes('aria-checked')).toBe('true');
  });

  it('writes graphAuthoringEnabled through client.settings.set on click', async () => {
    const { client, set } = provide();
    const w = mount(SettingsView, {
      global: { provide: { [HarnessClientKey as symbol]: client } },
    });
    await flushPromises();
    await w.get('[data-testid="graph-authoring-enabled-toggle"]').trigger('click');
    await flushPromises();
    expect(set).toHaveBeenCalledWith(
      expect.objectContaining({ graphAuthoringEnabled: true }),
    );
  });
});
