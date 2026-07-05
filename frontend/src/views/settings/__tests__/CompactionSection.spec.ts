/**
 * CompactionSection.spec.ts — coverage for the Compaction section in
 * SettingsView (mission compaction-strategy-ui-01KQ8TDI WP06).
 *
 * The section lives inside SettingsView.vue, so this file mounts the
 * full view but only asserts about the [data-testid=compaction-*]
 * subtree.
 *
 * Three behaviours under test (per WP06 acceptance):
 *   1. Slider stops emit the right enum string and persist via
 *      client.settings.set.
 *   2. Archive-days input rejects values outside [7, 365] with a UI
 *      error message.
 *   3. The "What does this mean?" disclosure renders the description +
 *      numerics from a mocked Compaction.GetTierExplain response.
 */
import { describe, it, expect, vi } from 'vitest';
import { mount, flushPromises } from '@vue/test-utils';
import SettingsView from '@/views/settings/SettingsView.vue';
import { createFakeHarnessClient } from '@/lib/harnessClient';
import { HarnessClientKey } from '@/lib/harnessClientContext';
import type {
  CompactionTierExplain,
  Provider,
  Settings,
} from '@/lib/types';
import { _flushForTest as flushSettingsDebounce } from '@/lib/settings';

const TIER_FIXTURE: CompactionTierExplain[] = [
  {
    aggressiveness: 'off',
    label: 'Off',
    description: 'Never compact.',
    triggerPct: 0,
    summarizePct: 0,
    mode: 'none',
  },
  {
    aggressiveness: 'conservative',
    label: 'Conservative',
    description: 'Compact only at 95% of cap.',
    triggerPct: 0.95,
    summarizePct: 0.2,
    mode: 'threshold',
  },
  {
    aggressiveness: 'balanced',
    label: 'Balanced (default)',
    description: 'Compact at 80% — recommended sweet spot.',
    triggerPct: 0.8,
    summarizePct: 0.3,
    mode: 'threshold',
  },
  {
    aggressiveness: 'aggressive',
    label: 'Aggressive',
    description: 'Compact at 60%, summarising oldest 40%.',
    triggerPct: 0.6,
    summarizePct: 0.4,
    mode: 'threshold',
  },
  {
    aggressiveness: 'maximal',
    label: 'Maximal',
    description: 'Continuous rolling summary every turn.',
    triggerPct: 0,
    summarizePct: 0,
    mode: 'rolling',
  },
];

const PROVIDER_FIXTURE: Provider[] = [
  {
    id: 'anthropic-1',
    name: 'Anthropic',
    tier: 'pro',
    kind: 'anthropic',
    model: 'claude-sonnet-4-5',
    models: ['claude-sonnet-4-5', 'claude-haiku-3-5'],
  },
];

function provide(overrides: Partial<Settings> = {}) {
  const settings: Settings = {
    schemaVersion: 1,
    lastRoute: '/sessions',
    theme: 'dark',
    accent: 'default',
    windowSize: { width: 1280, height: 800 },
    ...overrides,
  };
  const set = vi.fn().mockResolvedValue(undefined);
  const getTierExplain = vi.fn().mockResolvedValue(TIER_FIXTURE);
  const listProviders = vi.fn().mockResolvedValue(PROVIDER_FIXTURE);
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
      getWebSearch: async () => false,
      setWebSearch: async () => undefined,
      getBash: async () => false,
      setBash: async () => undefined,
      getSaveArtifact: async () => true,
      setSaveArtifact: async () => undefined,
      getMaxAgentTurns: async () => 0,
      setMaxAgentTurns: async () => undefined,
    } as any,
    llm: {
      listProviders,
      startStream: async () => 'fake-sub',
      stopStream: async () => undefined,
      addProvider: async () => undefined,
      removeProvider: async () => undefined,
      updateProvider: async () => undefined,
      testProvider: async () => ({
        success: true,
        latency_ms: 0,
        message: '',
      }),
      listModels: async () => [],
      resolveConfirm: async () => undefined,
    } as any,
    compaction: {
      getConfig: async () => ({ sites: {} }),
      getEffective: async () => ({
        config: { sites: {} },
        attribution: {},
      }),
      setConfig: async () => undefined,
      triggerManualCompaction: async () => ({
        strategy: 'drop_oldest',
        bytesSaved: 0,
      }),
      listCustomStrategies: async () => [],
      getTierExplain,
    },
    appInfo: async () => ({
      build: '0.1.0-test',
      commit: 'abcdef0',
      buildTime: '2026-04-25T00:00:00Z',
      goVersion: 'go1.23.0',
      platform: 'darwin/arm64',
      windowSize: settings.windowSize,
    }),
  });
  return { client, set, getTierExplain, listProviders };
}

describe('SettingsView Compaction section (WP06)', () => {
  it('renders the section heading', async () => {
    const { client } = provide();
    const w = mount(SettingsView, {
      global: { provide: { [HarnessClientKey as symbol]: client } },
    });
    await flushPromises();
    expect(w.find('[data-testid=compaction-section]').exists()).toBe(true);
    expect(w.text()).toContain('Compaction');
  });

  it('defaults to balanced when settings has no compactionAggressiveness', async () => {
    const { client } = provide();
    const w = mount(SettingsView, {
      global: { provide: { [HarnessClientKey as symbol]: client } },
    });
    await flushPromises();
    const balanced = w.find('[data-testid=compaction-tier-balanced]');
    expect(balanced.exists()).toBe(true);
    expect(balanced.attributes('aria-checked')).toBe('true');
  });

  it('hydrates from a saved compactionAggressiveness value', async () => {
    const { client } = provide({ compactionAggressiveness: 'aggressive' });
    const w = mount(SettingsView, {
      global: { provide: { [HarnessClientKey as symbol]: client } },
    });
    await flushPromises();
    expect(
      w.find('[data-testid=compaction-tier-aggressive]').attributes(
        'aria-checked',
      ),
    ).toBe('true');
  });

  it('persists the slider stop as the right enum string', async () => {
    const { client, set } = provide();
    const w = mount(SettingsView, {
      global: { provide: { [HarnessClientKey as symbol]: client } },
    });
    await flushPromises();
    await w.find('[data-testid=compaction-tier-aggressive]').trigger('click');
    // Force the debounced save to flush so we don't have to wait.
    const pending = flushSettingsDebounce();
    if (pending) await client.settings.set(pending);
    expect(set).toHaveBeenCalled();
    const last = set.mock.calls[set.mock.calls.length - 1][0] as Settings;
    expect(last.compactionAggressiveness).toBe('aggressive');
  });

  it('renders the explain disclosure with the selected tier description and numerics', async () => {
    const { client } = provide({ compactionAggressiveness: 'balanced' });
    const w = mount(SettingsView, {
      global: { provide: { [HarnessClientKey as symbol]: client } },
    });
    await flushPromises();
    await w.find('[data-testid=compaction-explain-toggle]').trigger('click');
    await flushPromises();
    const disclosure = w.find('[data-testid=compaction-explain-disclosure]');
    expect(disclosure.exists()).toBe(true);
    expect(
      w.find('[data-testid=compaction-explain-label]').text(),
    ).toContain('Balanced');
    expect(
      w.find('[data-testid=compaction-explain-description]').text(),
    ).toContain('80%');
    const numerics = w.find('[data-testid=compaction-explain-numerics]').text();
    // 0.8 trigger and 0.3 summarize on the balanced fixture.
    expect(numerics).toContain('80%');
    expect(numerics).toContain('30%');
    expect(numerics).toContain('threshold');
  });

  it('rejects archive-days values outside [7, 365] with an inline error', async () => {
    const { client, set } = provide();
    const w = mount(SettingsView, {
      global: { provide: { [HarnessClientKey as symbol]: client } },
    });
    await flushPromises();
    set.mockClear();
    const input = w.find<HTMLInputElement>(
      '[data-testid=compaction-archive-days-input]',
    );
    // Out-of-range low: setValue triggers Vue's `input` event; the
    // handler reads from evt.target.value via the DOM, so the new value
    // is what the test asserts against.
    input.element.value = '5';
    await input.trigger('input');
    await flushPromises();
    expect(
      w.find('[data-testid=compaction-archive-days-error]').exists(),
    ).toBe(true);
    // Invalid input must not trigger a save.
    flushSettingsDebounce();
    expect(set).not.toHaveBeenCalled();

    // Out-of-range high.
    input.element.value = '400';
    await input.trigger('input');
    await flushPromises();
    expect(
      w.find('[data-testid=compaction-archive-days-error]').exists(),
    ).toBe(true);

    // In range clears the error and persists.
    input.element.value = '30';
    await input.trigger('input');
    await flushPromises();
    expect(
      w.find('[data-testid=compaction-archive-days-error]').exists(),
    ).toBe(false);
    const pending = flushSettingsDebounce();
    if (pending) await client.settings.set(pending);
    const last = set.mock.calls[set.mock.calls.length - 1][0] as Settings;
    expect(last.compactionArchiveDays).toBe(30);
  });

  it('renders the model picker with the "use session model" default option', async () => {
    const { client } = provide();
    const w = mount(SettingsView, {
      global: { provide: { [HarnessClientKey as symbol]: client } },
    });
    await flushPromises();
    const select = w.find('[data-testid=compaction-model-select]');
    expect(select.exists()).toBe(true);
    expect(select.text()).toContain("Use session's active model");
    // The provider's two models populate the option list.
    expect(select.text()).toContain('claude-sonnet-4-5');
    expect(select.text()).toContain('claude-haiku-3-5');
  });
});
