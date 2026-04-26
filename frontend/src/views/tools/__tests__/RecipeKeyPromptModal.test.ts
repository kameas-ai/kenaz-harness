import { describe, it, expect, vi } from 'vitest';
import { mount, flushPromises } from '@vue/test-utils';
import RecipeKeyPromptModal from '@/views/tools/RecipeKeyPromptModal.vue';
import type { Recipe, RecipeStatus } from '@/lib/types';

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
      props: { open: true, recipe, install },
    });
    await flushPromises();

    await w
      .get('[data-testid=recipe-key-input-BRAVE_API_KEY]')
      .setValue('sk-test-123');
    await w.get('[data-testid=recipe-key-modal-submit]').trigger('click');
    await flushPromises();

    expect(install).toHaveBeenCalledWith('brave-search', {
      BRAVE_API_KEY: 'sk-test-123',
    });
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
      props: { open: true, recipe, install },
    });
    await flushPromises();
    const html = w.html();
    expect(html).not.toMatch(/#[0-9a-fA-F]{3,8}\b/);
    expect(html).not.toMatch(/rgba?\s*\(/i);
  });
});
