import { describe, it, expect, vi, beforeEach } from 'vitest';
import { mount, flushPromises } from '@vue/test-utils';
import RecipeKeyPromptModal from '@/views/tools/RecipeKeyPromptModal.vue';
import type { ConfigOption, Recipe, RecipeStatus } from '@/lib/types';
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
