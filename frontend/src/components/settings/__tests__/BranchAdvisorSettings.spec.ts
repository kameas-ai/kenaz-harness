/**
 * BranchAdvisorSettings.spec.ts — WP08 settings panel tests.
 *
 * Covers (per WP08 acceptance):
 *   1. Renders defaults from settings.get() (advisor off, confidence 0.85, tokens 2000).
 *   2. Toggle persists BranchAdvisorEnabled.
 *   3. Slider clamping: values outside [0.5, 0.99] are clamped before save.
 *   4. Number input clamping: values outside [500, 16000] show an error, no save.
 *   5. Debounced save is called for valid inputs.
 */
import { describe, it, expect, vi } from 'vitest';
import { mount, flushPromises } from '@vue/test-utils';
import BranchAdvisorSettings from '@/components/settings/BranchAdvisorSettings.vue';
import { createFakeHarnessClient } from '@/lib/harnessClient';
import { HarnessClientKey } from '@/lib/harnessClientContext';
import { _flushForTest as flushSettingsDebounce } from '@/lib/settings';
import type { Settings } from '@/lib/types';

// ── helpers ──────────────────────────────────────────────────────────────────

function buildSettings(overrides: Partial<Settings> = {}): Settings {
  return {
    schemaVersion: 1,
    lastRoute: '/sessions',
    theme: 'dark',
    accent: 'default',
    windowSize: { width: 1280, height: 800 },
    ...overrides,
  };
}

function mountPanel(overrides: Partial<Settings> = {}) {
  const settings = buildSettings(overrides);
  const set = vi.fn().mockResolvedValue(undefined);
  const client = createFakeHarnessClient({
    settings: {
      get: async () => settings,
      set,
      loadRoute: async () => settings.lastRoute,
      saveRoute: async () => undefined,
      logRouteChange: async () => undefined,
      loadTheme: async () => settings.theme,
      saveTheme: async () => undefined,
      getMemory: async () => false,
      setMemory: async () => undefined,
      getConfirmEach: async () => true,
      setConfirmEach: async () => undefined,
      getWebSearch: async () => false,
      setWebSearch: async () => undefined,
      getBash: async () => false,
      setBash: async () => undefined,
      getSaveArtifact: async () => true,
      setSaveArtifact: async () => undefined,
      getMaxAgentTurns: async () => 0,
      setMaxAgentTurns: async () => undefined,
      getPermissionMode: async () => 'normal' as const,
      setPermissionMode: async () => undefined,
      getPermissionCacheDangerousOps: async () => false,
      setPermissionCacheDangerousOps: async () => undefined,
      getBashAllowlistMigrated: async () => false,
      setBashAllowlistMigrated: async () => undefined,
      getPermissionsMigrationToastShown: async () => false,
      setPermissionsMigrationToastShown: async () => undefined,
      getCedarStrictCredentialMode: async () => false,
      setCedarStrictCredentialMode: async () => undefined,
      getFSRequestAccessEnabled: async () => true,
      setFSRequestAccessEnabled: async () => undefined,
    },
  });
  const w = mount(BranchAdvisorSettings, {
    global: { provide: { [HarnessClientKey as symbol]: client } },
  });
  return { w, set, client };
}

// ── tests ─────────────────────────────────────────────────────────────────────

describe('BranchAdvisorSettings (WP08)', () => {
  it('renders defaults: advisor off, confidence 0.85, tokens 2000', async () => {
    const { w } = mountPanel();
    await flushPromises();

    const toggle = w.find<HTMLInputElement>('[data-testid=branch-advisor-enabled-toggle]');
    expect(toggle.exists()).toBe(true);
    expect(toggle.element.checked).toBe(false);

    const slider = w.find<HTMLInputElement>('[data-testid=branch-advisor-confidence-slider]');
    expect(slider.exists()).toBe(true);
    expect(w.find('[data-testid=branch-advisor-confidence-value]').text()).toBe('0.85');

    const tokenInput = w.find<HTMLInputElement>('[data-testid=branch-advisor-max-tokens-input]');
    expect(tokenInput.exists()).toBe(true);
    expect(tokenInput.element.value).toBe('2000');
  });

  it('hydrates from persisted settings values', async () => {
    const { w } = mountPanel({
      branchAdvisorEnabled: true,
      branchAdvisorMinConfidence: 0.92,
      branchReintegrationMaxTokens: 4000,
    });
    await flushPromises();

    const toggle = w.find<HTMLInputElement>('[data-testid=branch-advisor-enabled-toggle]');
    expect(toggle.element.checked).toBe(true);

    expect(w.find('[data-testid=branch-advisor-confidence-value]').text()).toBe('0.92');

    const tokenInput = w.find<HTMLInputElement>('[data-testid=branch-advisor-max-tokens-input]');
    expect(tokenInput.element.value).toBe('4000');
  });

  it('toggle persists BranchAdvisorEnabled=true on first click', async () => {
    const { w, set, client } = mountPanel();
    await flushPromises();

    await w.find('[data-testid=branch-advisor-enabled-toggle]').trigger('change');
    const pending = flushSettingsDebounce();
    if (pending) await client.settings.set(pending);

    expect(set).toHaveBeenCalled();
    const saved = set.mock.calls[set.mock.calls.length - 1][0] as Settings;
    expect(saved.branchAdvisorEnabled).toBe(true);
  });

  it('toggle toggles back to false on second click', async () => {
    const { w, set, client } = mountPanel({ branchAdvisorEnabled: true });
    await flushPromises();

    await w.find('[data-testid=branch-advisor-enabled-toggle]').trigger('change');
    const pending = flushSettingsDebounce();
    if (pending) await client.settings.set(pending);

    expect(set).toHaveBeenCalled();
    const saved = set.mock.calls[set.mock.calls.length - 1][0] as Settings;
    expect(saved.branchAdvisorEnabled).toBe(false);
  });

  it('slider persists a valid confidence value', async () => {
    const { w, set, client } = mountPanel();
    await flushPromises();

    const slider = w.find<HTMLInputElement>('[data-testid=branch-advisor-confidence-slider]');
    slider.element.value = '0.75';
    await slider.trigger('input');

    const pending = flushSettingsDebounce();
    if (pending) await client.settings.set(pending);

    expect(set).toHaveBeenCalled();
    const saved = set.mock.calls[set.mock.calls.length - 1][0] as Settings;
    expect(saved.branchAdvisorMinConfidence).toBeCloseTo(0.75);
  });

  it('slider clamps a value below 0.5 to 0.5', async () => {
    const { w, set, client } = mountPanel();
    await flushPromises();

    const slider = w.find<HTMLInputElement>('[data-testid=branch-advisor-confidence-slider]');
    slider.element.value = '0.1';
    await slider.trigger('input');

    const pending = flushSettingsDebounce();
    if (pending) await client.settings.set(pending);

    const saved = set.mock.calls[set.mock.calls.length - 1][0] as Settings;
    expect(saved.branchAdvisorMinConfidence).toBeCloseTo(0.5);
    expect(w.find('[data-testid=branch-advisor-confidence-value]').text()).toBe('0.50');
  });

  it('slider clamps a value above 0.99 to 0.99', async () => {
    const { w, set, client } = mountPanel();
    await flushPromises();

    const slider = w.find<HTMLInputElement>('[data-testid=branch-advisor-confidence-slider]');
    slider.element.value = '1.5';
    await slider.trigger('input');

    const pending = flushSettingsDebounce();
    if (pending) await client.settings.set(pending);

    const saved = set.mock.calls[set.mock.calls.length - 1][0] as Settings;
    expect(saved.branchAdvisorMinConfidence).toBeCloseTo(0.99);
  });

  it('number input rejects values below 500 with an error and no save', async () => {
    const { w, set } = mountPanel();
    await flushPromises();
    set.mockClear();

    const input = w.find<HTMLInputElement>('[data-testid=branch-advisor-max-tokens-input]');
    input.element.value = '100';
    await input.trigger('input');
    await flushPromises();

    expect(w.find('[data-testid=branch-advisor-max-tokens-error]').exists()).toBe(true);
    flushSettingsDebounce();
    expect(set).not.toHaveBeenCalled();
  });

  it('number input rejects values above 16000 with an error and no save', async () => {
    const { w, set } = mountPanel();
    await flushPromises();
    set.mockClear();

    const input = w.find<HTMLInputElement>('[data-testid=branch-advisor-max-tokens-input]');
    input.element.value = '99999';
    await input.trigger('input');
    await flushPromises();

    expect(w.find('[data-testid=branch-advisor-max-tokens-error]').exists()).toBe(true);
    flushSettingsDebounce();
    expect(set).not.toHaveBeenCalled();
  });

  it('number input persists a valid in-range value and clears error', async () => {
    const { w, set, client } = mountPanel();
    await flushPromises();

    // First trigger an error.
    const input = w.find<HTMLInputElement>('[data-testid=branch-advisor-max-tokens-input]');
    input.element.value = '100';
    await input.trigger('input');
    await flushPromises();
    expect(w.find('[data-testid=branch-advisor-max-tokens-error]').exists()).toBe(true);

    // Now fix it.
    input.element.value = '8000';
    await input.trigger('input');
    await flushPromises();
    expect(w.find('[data-testid=branch-advisor-max-tokens-error]').exists()).toBe(false);

    const pending = flushSettingsDebounce();
    if (pending) await client.settings.set(pending);

    expect(set).toHaveBeenCalled();
    const saved = set.mock.calls[set.mock.calls.length - 1][0] as Settings;
    expect(saved.branchReintegrationMaxTokens).toBe(8000);
  });

  it('debounced save is not called for out-of-range token values', async () => {
    const { w, set } = mountPanel();
    await flushPromises();
    set.mockClear();

    const input = w.find<HTMLInputElement>('[data-testid=branch-advisor-max-tokens-input]');
    input.element.value = '499';
    await input.trigger('input');

    flushSettingsDebounce();
    expect(set).not.toHaveBeenCalled();
  });
});
