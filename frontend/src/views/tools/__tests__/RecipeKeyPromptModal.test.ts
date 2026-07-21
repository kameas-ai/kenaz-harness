import { describe, it, expect, vi, beforeEach } from 'vitest';
import { mount, flushPromises } from '@vue/test-utils';
import RecipeKeyPromptModal from '@/views/tools/RecipeKeyPromptModal.vue';
import type { ConfigOption, MissingPrereq, Recipe, RecipeStatus } from '@/lib/types';
import { createFakeHarnessClient } from '@/lib/harnessClient';
import { provideFakeClient } from '@/lib/harnessClientContext';
import { _resetToastQueue, useToastQueue } from '@/composables/useToastQueue';

const withFakeClient = {
  global: {
    plugins: [
      {
        install(app: import('vue').App) {
          provideFakeClient(app);
        },
      },
    ],
  },
};

function makeRecipe(overrides: Partial<Recipe> = {}): Recipe {
  return {
    id: 'brave-search',
    displayName: 'Brave Search',
    description: 'Web + local search via the Brave Search API.',
    category: 'search',
    envKeys: [
      {
        name: 'BRAVE_API_KEY',
        display: 'Brave Search API Key',
        docsUrl: 'https://api.search.brave.com/app/keys',
        required: true,
      },
    ],
    capabilities: {
      tools: true,
      resources: false,
      prompts: false,
      sampling: false,
    },
    docsUrl: 'https://example.com/brave',
    ...overrides,
  };
}

function okStatus(id: string): RecipeStatus {
  return {
    id,
    enabled: true,
    state: 'starting',
    restartAttempts: 0,
    keysPresent: true,
    pid: 0,
    toolCount: 0,
    resourceCount: 0,
    promptCount: 0,
  };
}

describe('RecipeKeyPromptModal', () => {
  it('disables submit until every required key is filled', async () => {
    const recipe = makeRecipe();
    const install = vi.fn(async () => okStatus(recipe.id));
    const w = mount(RecipeKeyPromptModal, {
      ...withFakeClient,
      props: { open: true, recipe, install },
    });
    await flushPromises();

    const submit = w.get('[data-testid=recipe-key-modal-submit]');
    expect((submit.element as HTMLButtonElement).disabled).toBe(true);

    await w
      .get('[data-testid=recipe-key-input-BRAVE_API_KEY]')
      .setValue('sk-test');
    expect((submit.element as HTMLButtonElement).disabled).toBe(false);
  });

  it('non-required keys do not block submit', async () => {
    const recipe = makeRecipe({
      envKeys: [
        {
          name: 'OPTIONAL_KEY',
          display: 'Optional',
          required: false,
          docsUrl: 'https://example.com/opt',
        },
      ],
    });
    const install = vi.fn(async () => okStatus(recipe.id));
    const w = mount(RecipeKeyPromptModal, {
      ...withFakeClient,
      props: { open: true, recipe, install },
    });
    await flushPromises();

    const submit = w.get('[data-testid=recipe-key-modal-submit]');
    expect((submit.element as HTMLButtonElement).disabled).toBe(false);
  });

  it('docs link wires the EnvKey.docsUrl with target=_blank rel=noopener', async () => {
    const recipe = makeRecipe();
    const install = vi.fn(async () => okStatus(recipe.id));
    const w = mount(RecipeKeyPromptModal, {
      ...withFakeClient,
      props: { open: true, recipe, install },
    });
    await flushPromises();

    const link = w.get('[data-testid=recipe-key-docs-BRAVE_API_KEY]');
    expect(link.attributes('href')).toBe(
      'https://api.search.brave.com/app/keys',
    );
    expect(link.attributes('target')).toBe('_blank');
    expect(link.attributes('rel')).toBe('noopener');
  });

  it('omits the docs link when EnvKey.docsUrl is undefined', async () => {
    const recipe = makeRecipe({
      envKeys: [
        {
          name: 'BRAVE_API_KEY',
          display: 'Brave Search API Key',
          required: true,
        },
      ],
    });
    const install = vi.fn(async () => okStatus(recipe.id));
    const w = mount(RecipeKeyPromptModal, {
      ...withFakeClient,
      props: { open: true, recipe, install },
    });
    await flushPromises();
    expect(w.find('[data-testid=recipe-key-docs-BRAVE_API_KEY]').exists()).toBe(
      false,
    );
  });

  it('submit calls install(id, env) and emits installed', async () => {
    const recipe = makeRecipe();
    const install = vi.fn(async () => okStatus(recipe.id));
    const w = mount(RecipeKeyPromptModal, {
      ...withFakeClient,
      props: { open: true, recipe, install },
    });
    await flushPromises();

    await w
      .get('[data-testid=recipe-key-input-BRAVE_API_KEY]')
      .setValue('sk-test-123');
    await w.get('[data-testid=recipe-key-modal-submit]').trigger('click');
    await flushPromises();

    expect(install).toHaveBeenCalledWith(
      'brave-search',
      { BRAVE_API_KEY: 'sk-test-123' },
      {},
    );
    const events = w.emitted('installed');
    expect(events).toBeTruthy();
    expect(events?.[0]?.[0]).toEqual(okStatus(recipe.id));
    expect(w.emitted('close')).toBeTruthy();
  });

  it('rpc rejection surfaces an inline banner with the error message', async () => {
    const recipe = makeRecipe();
    const install = vi.fn(async () => {
      throw new Error('keychain unavailable');
    });
    const w = mount(RecipeKeyPromptModal, {
      ...withFakeClient,
      props: { open: true, recipe, install },
    });
    await flushPromises();

    await w
      .get('[data-testid=recipe-key-input-BRAVE_API_KEY]')
      .setValue('sk-test-123');
    await w.get('[data-testid=recipe-key-modal-submit]').trigger('click');
    await flushPromises();

    const banner = w.find('[data-testid=recipe-key-modal-error]');
    expect(banner.exists()).toBe(true);
    expect(banner.text()).toContain('keychain unavailable');
    expect(w.emitted('installed')).toBeUndefined();
  });

  it('Esc key closes the modal', async () => {
    const recipe = makeRecipe();
    const install = vi.fn(async () => okStatus(recipe.id));
    const w = mount(RecipeKeyPromptModal, {
      ...withFakeClient,
      props: { open: true, recipe, install },
    });
    await flushPromises();

    await w
      .get('[data-testid=recipe-key-prompt-modal]')
      .trigger('keydown', { key: 'Escape' });

    expect(w.emitted('close')).toBeTruthy();
  });

  it('Enter on the last input submits when required keys are filled', async () => {
    const recipe = makeRecipe();
    const install = vi.fn(async () => okStatus(recipe.id));
    const w = mount(RecipeKeyPromptModal, {
      ...withFakeClient,
      props: { open: true, recipe, install },
    });
    await flushPromises();

    const input = w.get('[data-testid=recipe-key-input-BRAVE_API_KEY]');
    await input.setValue('sk-test-789');
    await input.trigger('keydown', { key: 'Enter' });
    await flushPromises();

    expect(install).toHaveBeenCalledTimes(1);
  });

  it('uses only design tokens — no raw hex / rgba', async () => {
    const recipe = makeRecipe();
    const install = vi.fn(async () => okStatus(recipe.id));
    const w = mount(RecipeKeyPromptModal, {
      ...withFakeClient,
      props: { open: true, recipe, install },
    });
    await flushPromises();
    const html = w.html();
    expect(html).not.toMatch(/#[0-9a-fA-F]{3,8}\b/);
    expect(html).not.toMatch(/rgba?\s*\(/i);
  });

  // ── ConfigOptions extension (WP03) ────────────────────────────────

  function fsRecipe(overrides: Partial<Recipe> = {}): Recipe {
    const allowedDirsOpt: ConfigOption = {
      name: 'allowed_directories',
      display: 'Allowed directories',
      kind: 'directory_list',
      default: ['${DATA_DIR}/agent-workspace'],
      required: true,
      description: 'Directories the model is allowed to read and write.',
    };
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
      configOptions: [allowedDirsOpt],
      ...overrides,
    };
  }

  it('renders both API Keys AND Configuration sections when both exist', async () => {
    const recipe = makeRecipe({
      configOptions: [
        {
          name: 'mode',
          display: 'Mode',
          kind: 'string',
          required: false,
          description: 'Optional mode flag.',
          default: 'fast',
        },
      ],
    });
    const install = vi.fn(async () => okStatus(recipe.id));
    const w = mount(RecipeKeyPromptModal, {
      ...withFakeClient,
      props: { open: true, recipe, install },
    });
    await flushPromises();
    expect(w.find('[data-testid=recipe-modal-env-section]').exists()).toBe(
      true,
    );
    expect(
      w.find('[data-testid=recipe-modal-config-section]').exists(),
    ).toBe(true);
  });

  it('hides API Keys section for filesystem (config only)', async () => {
    const recipe = fsRecipe();
    const install = vi.fn(async () => okStatus(recipe.id));
    const w = mount(RecipeKeyPromptModal, {
      ...withFakeClient,
      props: { open: true, recipe, install },
    });
    await flushPromises();
    expect(w.find('[data-testid=recipe-modal-env-section]').exists()).toBe(
      false,
    );
    expect(
      w.find('[data-testid=recipe-modal-config-section]').exists(),
    ).toBe(true);
  });

  it('pre-fills directory_list with the recipe default (literal token)', async () => {
    const recipe = fsRecipe();
    const install = vi.fn(async () => okStatus(recipe.id));
    const w = mount(RecipeKeyPromptModal, {
      ...withFakeClient,
      props: { open: true, recipe, install },
    });
    await flushPromises();
    expect(
      w.find('[data-testid=dirpicker-chip-0]').text(),
    ).toContain('${DATA_DIR}/agent-workspace');
    // hint surfaced for ${DATA_DIR}-tokenised defaults.
    expect(
      w.find('[data-testid=recipe-config-hint-allowed_directories]').text(),
    ).toContain('workspace folder');
  });

  it('submit-disabled until at least one directory is set for required directory_list', async () => {
    const recipe = fsRecipe({
      configOptions: [
        {
          name: 'allowed_directories',
          display: 'Allowed directories',
          kind: 'directory_list',
          required: true,
          description: 'd',
          // no default → empty list at open time
        },
      ],
    });
    const install = vi.fn(async () => okStatus(recipe.id));
    const w = mount(RecipeKeyPromptModal, {
      ...withFakeClient,
      props: { open: true, recipe, install },
    });
    await flushPromises();
    const submit = w.get('[data-testid=recipe-key-modal-submit]');
    expect((submit.element as HTMLButtonElement).disabled).toBe(true);
  });

  it('submit forwards env + config to install(id, env, config)', async () => {
    const recipe = fsRecipe();
    const install = vi.fn(async () => okStatus(recipe.id));
    const w = mount(RecipeKeyPromptModal, {
      ...withFakeClient,
      props: { open: true, recipe, install },
    });
    await flushPromises();

    await w.get('[data-testid=recipe-key-modal-submit]').trigger('click');
    await flushPromises();

    expect(install).toHaveBeenCalledTimes(1);
    const args = (install.mock.calls as unknown as unknown[][])[0];
    expect(args[0]).toBe('filesystem');
    expect(args[1]).toEqual({});
    expect(args[2]).toEqual({
      allowed_directories: ['${DATA_DIR}/agent-workspace'],
    });
  });

  it('honors initialConfig pre-fill (overrides recipe default)', async () => {
    const recipe = fsRecipe();
    const install = vi.fn(async () => okStatus(recipe.id));
    const w = mount(RecipeKeyPromptModal, {
      ...withFakeClient,
      props: {
        open: true,
        recipe,
        install,
        initialConfig: {
          allowed_directories: ['/Users/me/code', '/tmp/scratch'],
        },
      },
    });
    await flushPromises();
    expect(w.get('[data-testid=dirpicker-chip-0]').text()).toContain(
      '/Users/me/code',
    );
    expect(w.get('[data-testid=dirpicker-chip-1]').text()).toContain(
      '/tmp/scratch',
    );
    await w.get('[data-testid=recipe-key-modal-submit]').trigger('click');
    await flushPromises();
    const args = (install.mock.calls as unknown as unknown[][])[0];
    expect(args[2]).toEqual({
      allowed_directories: ['/Users/me/code', '/tmp/scratch'],
    });
  });

  // ── WP03: Install recommended policy button ──────────────────────

  describe('recommended policy install button (WP03)', () => {
    beforeEach(() => {
      _resetToastQueue();
    });

    it('shows install button when recommendedPolicyTemplate is set', async () => {
      const recipe = fsRecipe({ recommendedPolicyTemplate: 'filesystem-full-recommended.cedar' });
      const install = vi.fn(async () => okStatus(recipe.id));
      const w = mount(RecipeKeyPromptModal, {
        ...withFakeClient,
        props: { open: true, recipe, install },
      });
      await flushPromises();
      expect(w.find('[data-testid=recipe-modal-install-policy-btn]').exists()).toBe(true);
    });

    it('does not show install button when recommendedPolicyTemplate is absent', async () => {
      const recipe = fsRecipe();
      const install = vi.fn(async () => okStatus(recipe.id));
      const w = mount(RecipeKeyPromptModal, {
        ...withFakeClient,
        props: { open: true, recipe, install },
      });
      await flushPromises();
      expect(w.find('[data-testid=recipe-modal-install-policy-btn]').exists()).toBe(false);
    });

    it('calls installTemplate and shows success toast on click', async () => {
      const templateName = 'filesystem-full-recommended.cedar';
      const recipe = fsRecipe({ recommendedPolicyTemplate: templateName });
      const install = vi.fn(async () => okStatus(recipe.id));

      // Provide a fake client with a recording installTemplate.
      const installTemplateMock = vi.fn().mockResolvedValue({
        name: templateName,
        bytes: 42,
        embedded: false,
        parse_ok: true,
        source: '',
        read_only: false,
      });

      const w = mount(RecipeKeyPromptModal, {
        global: {
          plugins: [
            {
              install(app: import('vue').App) {
                provideFakeClient(app, {
                  cedarPolicy: {
                    listPolicies: vi.fn().mockResolvedValue([]),
                    reloadPolicies: vi.fn().mockResolvedValue(undefined),
                    recentDecisions: vi.fn().mockResolvedValue([]),
                    writeSnippet: vi.fn().mockResolvedValue(undefined),
                    revokeSnippet: vi.fn().mockResolvedValue(undefined),
                    resolvePropose: vi.fn().mockResolvedValue(undefined),
                    getPolicy: vi.fn().mockResolvedValue({ name: templateName, bytes: 0, embedded: false, parse_ok: true, source: '', read_only: false }),
                    savePolicy: vi.fn().mockResolvedValue({ ok: true }),
                    deletePolicy: vi.fn().mockResolvedValue(undefined),
                    validatePolicy: vi.fn().mockResolvedValue({ ok: true }),
                    installTemplate: installTemplateMock,
                  },
                });
              },
            },
          ],
        },
        props: { open: true, recipe, install },
      });
      await flushPromises();

      await w.find('[data-testid=recipe-modal-install-policy-btn]').trigger('click');
      await flushPromises();

      expect(installTemplateMock).toHaveBeenCalledWith(templateName, templateName);

      // Button replaced by "Installed" badge.
      expect(w.find('[data-testid=recipe-modal-install-policy-btn]').exists()).toBe(false);
      expect(w.find('[data-testid=recipe-modal-policy-installed-badge]').exists()).toBe(true);

      // Toast pushed.
      const { toasts } = useToastQueue();
      expect(toasts.some((t) => t.message.includes(templateName))).toBe(true);
    });

    it('shows "already installed" toast and marks installed when policy already exists', async () => {
      const templateName = 'filesystem-full-recommended.cedar';
      const recipe = fsRecipe({ recommendedPolicyTemplate: templateName });
      const install = vi.fn(async () => okStatus(recipe.id));

      const installTemplateMock = vi.fn().mockRejectedValue(
        new Error('cedarpolicy: policy file already exists'),
      );

      const w = mount(RecipeKeyPromptModal, {
        global: {
          plugins: [
            {
              install(app: import('vue').App) {
                provideFakeClient(app, {
                  cedarPolicy: {
                    listPolicies: vi.fn().mockResolvedValue([]),
                    reloadPolicies: vi.fn().mockResolvedValue(undefined),
                    recentDecisions: vi.fn().mockResolvedValue([]),
                    writeSnippet: vi.fn().mockResolvedValue(undefined),
                    revokeSnippet: vi.fn().mockResolvedValue(undefined),
                    resolvePropose: vi.fn().mockResolvedValue(undefined),
                    getPolicy: vi.fn().mockResolvedValue({ name: templateName, bytes: 0, embedded: false, parse_ok: true, source: '', read_only: false }),
                    savePolicy: vi.fn().mockResolvedValue({ ok: true }),
                    deletePolicy: vi.fn().mockResolvedValue(undefined),
                    validatePolicy: vi.fn().mockResolvedValue({ ok: true }),
                    installTemplate: installTemplateMock,
                  },
                });
              },
            },
          ],
        },
        props: { open: true, recipe, install },
      });
      await flushPromises();

      await w.find('[data-testid=recipe-modal-install-policy-btn]').trigger('click');
      await flushPromises();

      // "Already installed" toast.
      const { toasts } = useToastQueue();
      expect(toasts.some((t) => t.message.toLowerCase().includes('already installed'))).toBe(true);

      // Button transitions to installed badge even on "already exists".
      expect(w.find('[data-testid=recipe-modal-policy-installed-badge]').exists()).toBe(true);
    });

    it('button rearms on next open (supports re-install after delete)', async () => {
      const templateName = 'filesystem-full-recommended.cedar';
      const recipe = fsRecipe({ recommendedPolicyTemplate: templateName });
      const install = vi.fn(async () => okStatus(recipe.id));

      const installTemplateMock = vi.fn().mockResolvedValue({
        name: templateName,
        bytes: 42,
        embedded: false,
        parse_ok: true,
        source: '',
        read_only: false,
      });

      const w = mount(RecipeKeyPromptModal, {
        global: {
          plugins: [
            {
              install(app: import('vue').App) {
                provideFakeClient(app, {
                  cedarPolicy: {
                    listPolicies: vi.fn().mockResolvedValue([]),
                    reloadPolicies: vi.fn().mockResolvedValue(undefined),
                    recentDecisions: vi.fn().mockResolvedValue([]),
                    writeSnippet: vi.fn().mockResolvedValue(undefined),
                    revokeSnippet: vi.fn().mockResolvedValue(undefined),
                    resolvePropose: vi.fn().mockResolvedValue(undefined),
                    getPolicy: vi.fn().mockResolvedValue({ name: templateName, bytes: 0, embedded: false, parse_ok: true, source: '', read_only: false }),
                    savePolicy: vi.fn().mockResolvedValue({ ok: true }),
                    deletePolicy: vi.fn().mockResolvedValue(undefined),
                    validatePolicy: vi.fn().mockResolvedValue({ ok: true }),
                    installTemplate: installTemplateMock,
                  },
                });
              },
            },
          ],
        },
        props: { open: true, recipe, install },
      });
      await flushPromises();

      // First open: install.
      await w.find('[data-testid=recipe-modal-install-policy-btn]').trigger('click');
      await flushPromises();
      expect(w.find('[data-testid=recipe-modal-policy-installed-badge]').exists()).toBe(true);

      // Simulate close then reopen (simulating "user deleted via /policy, reopens modal").
      await w.setProps({ open: false });
      await flushPromises();
      await w.setProps({ open: true });
      await flushPromises();

      // Button should be re-armed.
      expect(w.find('[data-testid=recipe-modal-install-policy-btn]').exists()).toBe(true);
      expect(w.find('[data-testid=recipe-modal-policy-installed-badge]').exists()).toBe(false);
    });
  });
});

// ── primary_auth UX tests ─────────────────────────────────────────────────
describe('RecipeKeyPromptModal — primary_auth UX', () => {
  function makeDeviceCodeRecipe(overrides: Partial<Recipe> = {}): Recipe {
    return {
      id: 'outlook',
      displayName: 'Outlook',
      description: 'MS365 mail via device code.',
      category: 'fetch',
      primaryAuth: 'device_code',
      envKeys: [
        { name: 'MS365_MCP_CLIENT_ID', display: 'Azure client ID', required: false },
        { name: 'MS365_MCP_TENANT_ID', display: 'Azure tenant ID', required: false },
      ],
      capabilities: { tools: true, resources: false, prompts: false, sampling: false },
      ...overrides,
    };
  }

  it('shows device-code banner when primaryAuth === "device_code"', async () => {
    const recipe = makeDeviceCodeRecipe();
    const install = vi.fn(async () => okStatus(recipe.id));
    const w = mount(RecipeKeyPromptModal, {
      global: { plugins: [{ install: (app) => provideFakeClient(app) }] },
      props: { open: true, recipe, install },
    });
    await flushPromises();
    expect(w.find('[data-testid=recipe-modal-device-code-section]').exists()).toBe(true);
  });

  it('collapses optional env keys under Advanced for device_code recipes', async () => {
    const recipe = makeDeviceCodeRecipe();
    const install = vi.fn(async () => okStatus(recipe.id));
    const w = mount(RecipeKeyPromptModal, {
      global: { plugins: [{ install: (app) => provideFakeClient(app) }] },
      props: { open: true, recipe, install },
    });
    await flushPromises();

    // Primary env-section should NOT be visible (all keys are optional + device_code).
    expect(w.find('[data-testid=recipe-modal-env-section]').exists()).toBe(false);
    // Advanced section should exist and be collapsed by default.
    const advanced = w.find('[data-testid=recipe-modal-advanced-section]');
    expect(advanced.exists()).toBe(true);
    // The details element should NOT have the open attribute initially.
    expect((advanced.element as HTMLDetailsElement).open).toBe(false);
  });

  it('does not collapse required env keys under Advanced', async () => {
    const recipe = makeDeviceCodeRecipe({
      envKeys: [
        { name: 'REQUIRED_TOKEN', display: 'Required token', required: true },
      ],
    });
    const install = vi.fn(async () => okStatus(recipe.id));
    const w = mount(RecipeKeyPromptModal, {
      global: { plugins: [{ install: (app) => provideFakeClient(app) }] },
      props: { open: true, recipe, install },
    });
    await flushPromises();

    // Required key should render directly (not under Advanced).
    expect(w.find('[data-testid=recipe-modal-env-section]').exists()).toBe(true);
    expect(w.find('[data-testid=recipe-modal-advanced-section]').exists()).toBe(false);
  });

  it('does not show device-code banner for keys-primary recipes', async () => {
    const recipe: Recipe = {
      id: 'brave-search',
      displayName: 'Brave Search',
      description: 'Web search.',
      category: 'search',
      primaryAuth: 'keys',
      envKeys: [{ name: 'BRAVE_API_KEY', display: 'API Key', required: true }],
      capabilities: { tools: true, resources: false, prompts: false, sampling: false },
    };
    const install = vi.fn(async () => okStatus(recipe.id));
    const w = mount(RecipeKeyPromptModal, {
      global: { plugins: [{ install: (app) => provideFakeClient(app) }] },
      props: { open: true, recipe, install },
    });
    await flushPromises();
    expect(w.find('[data-testid=recipe-modal-device-code-section]').exists()).toBe(false);
    expect(w.find('[data-testid=recipe-modal-env-section]').exists()).toBe(true);
    expect(w.find('[data-testid=recipe-modal-advanced-section]').exists()).toBe(false);
  });
});

// ── prereq pre-flight banner tests ───────────────────────────────────────
describe('RecipeKeyPromptModal — prereq pre-flight', () => {
  function withPrereqStub(missing: MissingPrereq[]) {
    return {
      global: {
        plugins: [
          {
            install(app: import('vue').App) {
              const client = createFakeHarnessClient({
                tools: {
                  recipes: {
                    list: async () => [],
                    install: async () => okStatus('test'),
                    signIn: async () => okStatus('test'),
                    uninstall: async () => {},
                    forgetKey: async () => {},
                    status: async () => okStatus('test'),
                    config: async () => ({}),
                    checkPrereqs: async () => missing,
                    beginDeviceAuth: async () => ({
                      userCode: 'ABCD-1234',
                      verificationUri: 'https://github.com/login/device',
                      expiresIn: 900,
                    }),
                    pollDeviceAuth: async () => okStatus('test'),
                  },
                  pickDirectory: async () => '',
                  requestAdditionalAllowedDir: async () => ({ granted: false, expanded: '', message: '' }),
                },
              } as Partial<import('@/lib/harnessClient').HarnessClient>);
              app.provide(
                Symbol.for('kenaz.harnessClient') as import('vue').InjectionKey<import('@/lib/harnessClient').HarnessClient>,
                client,
              );
            },
          },
        ],
      },
    };
  }

  function baseRecipe(): Recipe {
    return {
      id: 'fetch',
      displayName: 'Fetch',
      description: 'HTTP fetch.',
      category: 'fetch',
      primaryAuth: 'none',
      envKeys: [],
      capabilities: { tools: true, resources: false, prompts: false, sampling: false },
    };
  }

  it('shows prereq banner when checkPrereqs returns missing runtimes', async () => {
    const missing: MissingPrereq[] = [
      { name: 'uv / uvx', installHint: 'brew install uv' },
    ];
    const recipe = baseRecipe();
    const install = vi.fn(async () => okStatus(recipe.id));
    const w = mount(RecipeKeyPromptModal, {
      ...withPrereqStub(missing),
      props: { open: true, recipe, install },
    });
    await flushPromises();

    const banner = w.find('[data-testid=recipe-modal-prereq-banner]');
    expect(banner.exists()).toBe(true);
    expect(banner.text()).toContain('uv / uvx');
    expect(banner.text()).toContain('brew install uv');
  });

  it('does not show prereq banner when all runtimes are present', async () => {
    const recipe = baseRecipe();
    const install = vi.fn(async () => okStatus(recipe.id));
    const w = mount(RecipeKeyPromptModal, {
      ...withPrereqStub([]),
      props: { open: true, recipe, install },
    });
    await flushPromises();

    expect(w.find('[data-testid=recipe-modal-prereq-banner]').exists()).toBe(false);
  });
});

// ── file prereq guided-setup section tests (Gmail / gcp-oauth.keys.json) ────
describe('RecipeKeyPromptModal — file prereq guided setup', () => {
  /**
   * Mounts the modal with a fake harness client that returns the given
   * missingPrereqs from checkPrereqs and records pickFile calls.
   */
  function withFilePrereqClient(
    missing: MissingPrereq[],
    opts: {
      pickFileMock?: ReturnType<typeof vi.fn>;
      checkPrereqsSecondCall?: MissingPrereq[];
    } = {},
  ) {
    const pickFileMock = opts.pickFileMock ?? vi.fn().mockResolvedValue('');
    let callCount = 0;
    const checkPrereqsMock = vi.fn().mockImplementation(async () => {
      callCount++;
      if (callCount === 1) return missing;
      return opts.checkPrereqsSecondCall ?? [];
    });

    const client = createFakeHarnessClient({
      tools: {
        recipes: {
          list: async () => [],
          install: async () => okStatus('gmail'),
          signIn: async () => okStatus('gmail'),
          uninstall: async () => {},
          forgetKey: async () => {},
          status: async () => okStatus('gmail'),
          config: async () => ({}),
          checkPrereqs: checkPrereqsMock,
          beginDeviceAuth: async () => ({
            userCode: 'ABCD-1234',
            verificationUri: 'https://github.com/login/device',
            expiresIn: 900,
          }),
          pollDeviceAuth: async () => okStatus('gmail'),
        },
        pickDirectory: async () => '',
        requestAdditionalAllowedDir: async () => ({ granted: false, expanded: '', message: '' }),
      },
      shell: {
        openInOSBrowser: async () => {},
        pathComplete: async () => [],
        readFile: async () => ({ dataBase64: '', mediaType: '' }),
        pickFile: pickFileMock,
      },
    } as Partial<import('@/lib/harnessClient').HarnessClient>);

    return {
      global: {
        plugins: [
          {
            install(app: import('vue').App) {
              app.provide(
                Symbol.for('kenaz.harnessClient') as import('vue').InjectionKey<import('@/lib/harnessClient').HarnessClient>,
                client,
              );
            },
          },
        ],
      },
      pickFileMock,
      checkPrereqsMock,
    };
  }

  function makeGmailFilePrereq(): MissingPrereq {
    return {
      name: 'Gmail OAuth credentials file',
      installHint: 'create a Google Cloud OAuth client and save JSON to ~/.gmail-mcp/gcp-oauth.keys.json',
      kind: 'file',
      fileSetupGuide: {
        targetPath: '/Users/testuser/.gmail-mcp/gcp-oauth.keys.json',
        steps: [
          'Open the Google Cloud Console and create or select a project.',
          'Enable the Gmail API.',
          'Create an OAuth 2.0 client (Desktop app type).',
          'Download the credentials JSON.',
          'Use the file picker below to place the downloaded file.',
        ],
        docsUrl: 'https://developers.google.com/gmail/api/quickstart/go',
      },
    };
  }

  function gmailRecipe(): Recipe {
    return {
      id: 'gmail',
      displayName: 'Gmail',
      description: 'Read, search, and send Gmail messages.',
      category: 'communication',
      primaryAuth: 'none',
      envKeys: [],
      capabilities: { tools: true, resources: false, prompts: false, sampling: false },
      warning: 'One-time Google Cloud setup required.',
    };
  }

  it('shows the guided setup section when a file prereq is missing', async () => {
    const filePrereq = makeGmailFilePrereq();
    const { global } = withFilePrereqClient([filePrereq]);
    const recipe = gmailRecipe();
    const install = vi.fn(async () => okStatus(recipe.id));

    const w = mount(RecipeKeyPromptModal, {
      global,
      props: { open: true, recipe, install },
    });
    await flushPromises();

    // The guided setup section should be visible.
    const section = w.find(`[data-testid="recipe-modal-file-prereq-${filePrereq.name}"]`);
    expect(section.exists()).toBe(true);

    // The raw prereq banner should NOT be shown for file-kind prereqs.
    expect(w.find('[data-testid=recipe-modal-prereq-banner]').exists()).toBe(false);
  });

  it('renders all setup steps in the guided section', async () => {
    const filePrereq = makeGmailFilePrereq();
    const { global } = withFilePrereqClient([filePrereq]);
    const recipe = gmailRecipe();
    const install = vi.fn(async () => okStatus(recipe.id));

    const w = mount(RecipeKeyPromptModal, {
      global,
      props: { open: true, recipe, install },
    });
    await flushPromises();

    const steps = w.findAll('[data-testid^="recipe-modal-file-prereq-step-"]');
    expect(steps).toHaveLength(filePrereq.fileSetupGuide!.steps.length);
    // First step text should match.
    expect(steps[0].text()).toContain('Google Cloud Console');
  });

  it('shows the native file picker button for file prereqs', async () => {
    const filePrereq = makeGmailFilePrereq();
    const { global } = withFilePrereqClient([filePrereq]);
    const recipe = gmailRecipe();
    const install = vi.fn(async () => okStatus(recipe.id));

    const w = mount(RecipeKeyPromptModal, {
      global,
      props: { open: true, recipe, install },
    });
    await flushPromises();

    const pickerBtn = w.find('[data-testid=recipe-modal-file-prereq-pick-btn]');
    expect(pickerBtn.exists()).toBe(true);
    expect(pickerBtn.text()).toContain('Select credentials file');
  });

  it('shows the target path in the guided section', async () => {
    const filePrereq = makeGmailFilePrereq();
    const { global } = withFilePrereqClient([filePrereq]);
    const recipe = gmailRecipe();
    const install = vi.fn(async () => okStatus(recipe.id));

    const w = mount(RecipeKeyPromptModal, {
      global,
      props: { open: true, recipe, install },
    });
    await flushPromises();

    const pathEl = w.find('[data-testid=recipe-modal-file-prereq-target-path]');
    expect(pathEl.exists()).toBe(true);
    expect(pathEl.text()).toContain('gcp-oauth.keys.json');
  });

  it('shows docs link when docsUrl is set', async () => {
    const filePrereq = makeGmailFilePrereq();
    const { global } = withFilePrereqClient([filePrereq]);
    const recipe = gmailRecipe();
    const install = vi.fn(async () => okStatus(recipe.id));

    const w = mount(RecipeKeyPromptModal, {
      global,
      props: { open: true, recipe, install },
    });
    await flushPromises();

    const docsLink = w.find('[data-testid=recipe-modal-file-prereq-docs-link]');
    expect(docsLink.exists()).toBe(true);
    expect(docsLink.attributes('href')).toContain('developers.google.com');
    expect(docsLink.attributes('target')).toBe('_blank');
    expect(docsLink.attributes('rel')).toContain('noopener');
  });

  it('clicking the file picker button calls shell.pickFile with JSON filter', async () => {
    const filePrereq = makeGmailFilePrereq();
    const pickFileMock = vi.fn().mockResolvedValue('/Users/me/Downloads/client_secret.json');
    // Second checkPrereqs call returns empty (file now present).
    const { global } = withFilePrereqClient([filePrereq], {
      pickFileMock,
      checkPrereqsSecondCall: [],
    });
    const recipe = gmailRecipe();
    const install = vi.fn(async () => okStatus(recipe.id));

    const w = mount(RecipeKeyPromptModal, {
      global,
      props: { open: true, recipe, install },
    });
    await flushPromises();

    await w.find('[data-testid=recipe-modal-file-prereq-pick-btn]').trigger('click');
    await flushPromises();

    expect(pickFileMock).toHaveBeenCalledOnce();
    const [title, , filters] = pickFileMock.mock.calls[0];
    expect(title).toContain('Gmail OAuth credentials file');
    expect(filters).toContain('*.json');
  });

  it('shows placed confirmation after successful file selection', async () => {
    const filePrereq = makeGmailFilePrereq();
    const pickFileMock = vi.fn().mockResolvedValue('/Users/me/Downloads/client_secret.json');
    const { global } = withFilePrereqClient([filePrereq], {
      pickFileMock,
      checkPrereqsSecondCall: [], // file is now present
    });
    const recipe = gmailRecipe();
    const install = vi.fn(async () => okStatus(recipe.id));

    const w = mount(RecipeKeyPromptModal, {
      global,
      props: { open: true, recipe, install },
    });
    await flushPromises();

    await w.find('[data-testid=recipe-modal-file-prereq-pick-btn]').trigger('click');
    await flushPromises();

    // Placed confirmation should appear; picker button should be gone.
    const placed = w.find('[data-testid=recipe-modal-file-prereq-placed]');
    expect(placed.exists()).toBe(true);
    expect(w.find('[data-testid=recipe-modal-file-prereq-pick-btn]').exists()).toBe(false);
  });

  it('does NOT show the guided section when no file prereq is missing', async () => {
    // Empty prereqs — no file prereq.
    const { global } = withFilePrereqClient([]);
    const recipe = gmailRecipe();
    const install = vi.fn(async () => okStatus(recipe.id));

    const w = mount(RecipeKeyPromptModal, {
      global,
      props: { open: true, recipe, install },
    });
    await flushPromises();

    expect(w.find('[data-testid=recipe-modal-file-prereq-pick-btn]').exists()).toBe(false);
    expect(w.find('[data-testid=recipe-modal-prereq-banner]').exists()).toBe(false);
  });

  it('shows both runtime banner AND guided section when both prereqs are missing', async () => {
    const runtimePrereq: MissingPrereq = { name: 'uv / uvx', installHint: 'brew install uv' };
    const filePrereq = makeGmailFilePrereq();
    const { global } = withFilePrereqClient([runtimePrereq, filePrereq]);
    const recipe = gmailRecipe();
    const install = vi.fn(async () => okStatus(recipe.id));

    const w = mount(RecipeKeyPromptModal, {
      global,
      props: { open: true, recipe, install },
    });
    await flushPromises();

    // Runtime banner present.
    const banner = w.find('[data-testid=recipe-modal-prereq-banner]');
    expect(banner.exists()).toBe(true);
    expect(banner.text()).toContain('uv / uvx');

    // Guided file section also present.
    expect(w.find('[data-testid=recipe-modal-file-prereq-pick-btn]').exists()).toBe(true);
  });
});

// ── path-picker button tests ──────────────────────────────────────────────
describe('RecipeKeyPromptModal — string config path picker', () => {
  function makePathRecipe(): Recipe {
    return {
      id: 'sqlite',
      displayName: 'SQLite',
      description: 'SQLite db.',
      category: 'filesystem',
      primaryAuth: 'none',
      envKeys: [],
      configOptions: [
        {
          name: 'db_path',
          display: 'Database file path',
          kind: 'string',
          required: true,
          description: 'Path to the SQLite file.',
        },
      ],
      capabilities: { tools: true, resources: false, prompts: false, sampling: false },
    };
  }

  it('shows Browse button for path-like string config options', async () => {
    const recipe = makePathRecipe();
    const install = vi.fn(async () => okStatus(recipe.id));
    const w = mount(RecipeKeyPromptModal, {
      global: { plugins: [{ install: (app) => provideFakeClient(app) }] },
      props: { open: true, recipe, install },
    });
    await flushPromises();

    expect(w.find('[data-testid=recipe-config-string-picker-db_path]').exists()).toBe(true);
  });

  it('does not show Browse button for non-path string options', async () => {
    const recipe: Recipe = {
      id: 'slack',
      displayName: 'Slack',
      description: 'Slack.',
      category: 'fetch',
      primaryAuth: 'keys',
      envKeys: [{ name: 'SLACK_BOT_TOKEN', display: 'Bot token', required: true }],
      configOptions: [
        {
          name: 'workspace_name',
          display: 'Workspace name',
          kind: 'string',
          required: false,
          description: 'Optional workspace display name.',
        },
      ],
      capabilities: { tools: true, resources: false, prompts: false, sampling: false },
    };
    const install = vi.fn(async () => okStatus(recipe.id));
    const w = mount(RecipeKeyPromptModal, {
      global: { plugins: [{ install: (app) => provideFakeClient(app) }] },
      props: { open: true, recipe, install },
    });
    await flushPromises();

    expect(w.find('[data-testid=recipe-config-string-picker-workspace_name]').exists()).toBe(false);
  });
});
