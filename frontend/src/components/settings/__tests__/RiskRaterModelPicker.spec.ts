/**
 * RiskRaterModelPicker.spec.ts — risk-rated-autonomy-01PMRA01 owner
 * ruling 1/2 component tests.
 *
 * Covers the mission's "UI component test" proof requirement: the
 * picker renders benchmark numbers for a known (benchmarked) model, and
 * an explicit "Unbenchmarked" badge for a model the vendored data has no
 * row for — never a blank cell. Also covers selection persisting
 * Settings.riskRaterModel and reverting to the provider-default option.
 */
import { describe, it, expect, vi } from 'vitest';
import { mount, flushPromises } from '@vue/test-utils';
import RiskRaterModelPicker from '@/components/settings/RiskRaterModelPicker.vue';
import { createFakeHarnessClient } from '@/lib/harnessClient';
import { HarnessClientKey } from '@/lib/harnessClientContext';
import { _flushForTest as flushSettingsDebounce } from '@/lib/settings';
import type { Provider, RiskRaterBenchmark, Settings } from '@/lib/types';

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

const FAKE_PROVIDERS: Provider[] = [
  {
    id: 'or1',
    name: 'OpenRouter',
    tier: 'personal',
    kind: 'openrouter',
    model: 'qwen/qwen-2.5-7b-instruct',
    models: ['qwen/qwen-2.5-7b-instruct', 'some/totally-unbenchmarked-model'],
  },
];

const FAKE_BENCHMARK: RiskRaterBenchmark = {
  provenance: {
    benchmark_date: '2026-09-15',
    calls: 234,
    method: 'test fixture',
  },
  models: [
    {
      id: 'qwen/qwen-2.5-7b-instruct',
      label: 'Qwen 2.5 7B Instruct',
      measured_via: 'openrouter',
      data_available: true,
      bands_exact: 4,
      bands_adjacent: 1,
      bands_miss: 0,
      injection_raw_score: 100,
      injection_raw_note: '',
      reliability_5s_pct: 96.2,
      reliability_30s_pct: 100.0,
      median_latency_ms: 467.9,
      p90_latency_ms: 851.7,
      cost_per_rating_usd: 0.0000592,
      notes: 'openrouter provider default.',
    },
  ],
};

function mountPicker(overrides: Partial<Settings> = {}) {
  const settings = buildSettings(overrides);
  const set = vi.fn().mockResolvedValue(undefined);
  const client = createFakeHarnessClient({
    settings: {
      get: async () => settings,
      set,
      getRiskRaterBenchmark: async () => FAKE_BENCHMARK,
      // eslint-disable-next-line @typescript-eslint/no-explicit-any
    } as any,
    llm: {
      listProviders: async () => FAKE_PROVIDERS,
      // eslint-disable-next-line @typescript-eslint/no-explicit-any
    } as any,
  });
  const w = mount(RiskRaterModelPicker, {
    global: { provide: { [HarnessClientKey as symbol]: client } },
  });
  return { w, set, client };
}

describe('RiskRaterModelPicker', () => {
  it('renders benchmark numbers for a known (benchmarked) model', async () => {
    const { w } = mountPicker();
    await flushPromises();

    const row = w.find('[data-testid="risk-rater-model-row-or1::qwen/qwen-2.5-7b-instruct"]');
    expect(row.exists()).toBe(true);
    expect(row.text()).toContain('4/1/0'); // bands exact/adjacent/miss
    expect(row.text()).toContain('96.2%'); // reliability@5s
    expect(row.text()).toContain('468ms'); // median latency
    expect(row.text()).toContain('$0.000059'); // cost/rating
    // A benchmarked row must NOT show the unbenchmarked badge.
    expect(
      w.find('[data-testid="risk-rater-unbenchmarked-badge-or1::qwen/qwen-2.5-7b-instruct"]').exists(),
    ).toBe(false);
  });

  it('renders an explicit Unbenchmarked badge for a model with no benchmark row, never blank cells', async () => {
    const { w } = mountPicker();
    await flushPromises();

    const row = w.find(
      '[data-testid="risk-rater-model-row-or1::some/totally-unbenchmarked-model"]',
    );
    expect(row.exists()).toBe(true);
    const badge = w.find(
      '[data-testid="risk-rater-unbenchmarked-badge-or1::some/totally-unbenchmarked-model"]',
    );
    expect(badge.exists()).toBe(true);
    expect(badge.text()).toMatch(/unbenchmarked/i);
    // The mutation check the mission calls for: removing the v-else
    // badge branch would either blank these cells or throw reading
    // undefined `.bands_exact` etc. off a null `bench` — assert the
    // benchmark numeric strings that ONLY the benchmarked branch would
    // produce are absent from this row.
    expect(row.text()).not.toMatch(/\d+\/\d+\/\d+/);
    expect(row.text()).not.toContain('%');
  });

  it('defaults to "use provider default" when no explicit setting is persisted', async () => {
    const { w } = mountPicker();
    await flushPromises();

    const defaultRadio = w.find<HTMLInputElement>(
      '[data-testid="risk-rater-model-radio-default"]',
    );
    expect(defaultRadio.element.checked).toBe(true);
  });

  it('hydrates the selected radio from a persisted riskRaterModel setting', async () => {
    const { w } = mountPicker({
      riskRaterModel: { providerId: 'or1', modelId: 'qwen/qwen-2.5-7b-instruct' },
    });
    await flushPromises();

    const radio = w.find<HTMLInputElement>(
      '[data-testid="risk-rater-model-radio-or1::qwen/qwen-2.5-7b-instruct"]',
    );
    expect(radio.element.checked).toBe(true);
    const defaultRadio = w.find<HTMLInputElement>(
      '[data-testid="risk-rater-model-radio-default"]',
    );
    expect(defaultRadio.element.checked).toBe(false);
  });

  it('selecting a model persists Settings.riskRaterModel', async () => {
    const { w, set, client } = mountPicker();
    await flushPromises();

    const radio = w.find<HTMLInputElement>(
      '[data-testid="risk-rater-model-radio-or1::qwen/qwen-2.5-7b-instruct"]',
    );
    radio.element.checked = true;
    await radio.trigger('change');

    const pending = flushSettingsDebounce();
    if (pending) await client.settings.set(pending);

    expect(set).toHaveBeenCalled();
    const saved = set.mock.calls[set.mock.calls.length - 1][0] as Settings;
    expect(saved.riskRaterModel).toEqual({
      providerId: 'or1',
      modelId: 'qwen/qwen-2.5-7b-instruct',
    });
  });

  it('selecting the default option clears a previously-persisted explicit setting', async () => {
    const { w, set, client } = mountPicker({
      riskRaterModel: { providerId: 'or1', modelId: 'qwen/qwen-2.5-7b-instruct' },
    });
    await flushPromises();

    const defaultRadio = w.find<HTMLInputElement>(
      '[data-testid="risk-rater-model-radio-default"]',
    );
    defaultRadio.element.checked = true;
    await defaultRadio.trigger('change');

    const pending = flushSettingsDebounce();
    if (pending) await client.settings.set(pending);

    expect(set).toHaveBeenCalled();
    const saved = set.mock.calls[set.mock.calls.length - 1][0] as Settings;
    expect(saved.riskRaterModel).toBeUndefined();
  });

  it('shows the benchmark provenance line', async () => {
    const { w } = mountPicker();
    await flushPromises();

    const prov = w.find('[data-testid="risk-rater-benchmark-provenance"]');
    expect(prov.exists()).toBe(true);
    expect(prov.text()).toContain('234 calls');
    expect(prov.text()).toContain('2026-09-15');
  });
});
