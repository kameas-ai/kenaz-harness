<script setup lang="ts">
/**
 * RiskRaterModelPicker — Settings sub-section for risk-rated-autonomy-
 * 01PMRA01's rating-model choice (owner ruling 1, 2026-09-15:
 * "selectable + configurable... user-choosable from the configured
 * provider's available models").
 *
 * Renders one row per (provider, model) available across the user's
 * configured LLM providers, with the vendored benchmark data (owner
 * ruling 2: bands/injection/reliability/latency/cost + provenance,
 * core/policy/risk/data/rater_benchmark.json via
 * Settings_GetRiskRaterBenchmark) shown beside each model. A model with
 * no matching benchmark row renders an explicit "Unbenchmarked" badge —
 * never a blank cell (the silent-lie rule this mission's spec calls
 * out).
 *
 * "Use provider default (recommended)" is the first, pre-selected
 * option — persists Settings.riskRaterModel as unset, which lets the
 * backend's three-rung ladder (core/policy/risk.ResolveRaterModel)
 * pick per owner ruling 3.
 */
import { computed, onMounted, ref } from 'vue';
import { useHarnessClient } from '@/lib/useHarnessAPI';
import { debouncedSave } from '@/lib/settings';
import type { Provider, RiskRaterBenchmark, RiskRaterBenchmarkModel, Settings } from '@/lib/types';

const client = useHarnessClient();

const settings = ref<Settings>({
  schemaVersion: 1,
  lastRoute: '/sessions',
  theme: 'system',
  accent: 'default',
  windowSize: { width: 1280, height: 800 },
});
const providers = ref<Provider[]>([]);
const benchmark = ref<RiskRaterBenchmark | null>(null);
const loadError = ref<string | null>(null);

const selectedProviderId = ref('');
const selectedModelId = ref('');

onMounted(async () => {
  try {
    settings.value = await client.settings.get();
  } catch {
    // Keep defaults on error.
  }
  selectedProviderId.value = settings.value.riskRaterModel?.providerId ?? '';
  selectedModelId.value = settings.value.riskRaterModel?.modelId ?? '';

  try {
    providers.value = await client.llm.listProviders();
  } catch {
    providers.value = [];
  }
  try {
    benchmark.value = await client.settings.getRiskRaterBenchmark();
  } catch (err) {
    loadError.value = err instanceof Error ? err.message : String(err);
    benchmark.value = null;
  }
});

interface ModelRow {
  value: string; // `${providerId}::${modelId}`
  providerId: string;
  modelId: string;
  providerLabel: string;
  bench: RiskRaterBenchmarkModel | null;
}

/**
 * findBenchmarkRow mirrors core/policy/risk.FindBenchmarkRow: match by
 * exact id first, then by alias (the openrouter-measured rows' Aliases
 * list carries the bare id a direct (non-openrouter) provider profile
 * uses for the SAME underlying model, e.g. "gpt-4o-mini" aliasing
 * "openai/gpt-4o-mini").
 */
function findBenchmarkRow(
  rows: RiskRaterBenchmarkModel[],
  modelId: string,
): RiskRaterBenchmarkModel | null {
  const exact = rows.find((r) => r.id === modelId);
  if (exact) return exact;
  const aliased = rows.find((r) => (r.aliases ?? []).includes(modelId));
  return aliased ?? null;
}

const modelRows = computed<ModelRow[]>(() => {
  const rows: ModelRow[] = [];
  const benchModels = benchmark.value?.models ?? [];
  for (const p of providers.value) {
    const models = p.models && p.models.length > 0 ? p.models : [p.model];
    for (const m of models) {
      if (!m) continue;
      rows.push({
        value: `${p.id}::${m}`,
        providerId: p.id,
        modelId: m,
        providerLabel: p.name || p.id,
        bench: findBenchmarkRow(benchModels, m),
      });
    }
  }
  return rows;
});

const selectedValue = computed<string>(() => {
  if (!selectedProviderId.value || !selectedModelId.value) return '';
  return `${selectedProviderId.value}::${selectedModelId.value}`;
});

function selectRow(row: ModelRow | null): void {
  if (!row) {
    selectedProviderId.value = '';
    selectedModelId.value = '';
  } else {
    selectedProviderId.value = row.providerId;
    selectedModelId.value = row.modelId;
  }
  debouncedSave(client, {
    ...settings.value,
    riskRaterModel:
      selectedProviderId.value && selectedModelId.value
        ? { providerId: selectedProviderId.value, modelId: selectedModelId.value }
        : undefined,
  });
}

function onRadioChange(evt: Event): void {
  const v = (evt.target as HTMLInputElement).value;
  if (!v) {
    selectRow(null);
    return;
  }
  const row = modelRows.value.find((r) => r.value === v) ?? null;
  selectRow(row);
}

function fmtPct(n: number): string {
  return `${n.toFixed(1)}%`;
}

function fmtMs(n: number): string {
  return n >= 1000 ? `${(n / 1000).toFixed(2)}s` : `${n.toFixed(0)}ms`;
}

function fmtCost(n: number): string {
  return `$${n.toFixed(6)}`;
}
</script>

<template>
  <div class="grid gap-4" data-testid="risk-rater-model-picker">
    <section>
      <h2 class="font-ui text-[11px] uppercase tracking-[0.18em] text-ink-subtle">
        Risk rating model
      </h2>
      <p class="mt-1 font-ui text-[11px] text-ink-muted">
        When a tool call falls outside Cedar's hard allow/deny rules, this model
        rates its risk 0-100 against your autonomy tier's threshold. Pick a
        model, or use the provider default (a small, fast model measured for
        this job — see the benchmark below).
      </p>
      <p
        v-if="benchmark"
        class="mt-1 font-ui text-[10px] text-ink-subtle"
        data-testid="risk-rater-benchmark-provenance"
      >
        Benchmark: {{ benchmark.provenance.calls }} calls, measured
        {{ benchmark.provenance.benchmark_date }}.
      </p>
      <p
        v-if="loadError"
        class="mt-1 font-ui text-[11px] text-signal-danger"
        role="alert"
        data-testid="risk-rater-benchmark-load-error"
      >
        Could not load benchmark data: {{ loadError }}
      </p>

      <div class="mt-3 overflow-x-auto">
        <table class="w-full border-collapse font-ui text-[11px]" data-testid="risk-rater-model-table">
          <thead>
            <tr class="border-b border-border text-left text-ink-subtle">
              <th class="w-8 py-1"><span class="sr-only">Select</span></th>
              <th class="py-1 pr-3">Model</th>
              <th class="py-1 pr-3">Bands (exact/adj/miss)</th>
              <th class="py-1 pr-3">Reliability@5s</th>
              <th class="py-1 pr-3">Median latency</th>
              <th class="py-1 pr-3">Cost / rating</th>
            </tr>
          </thead>
          <tbody>
            <tr
              class="border-b border-border-muted"
              data-testid="risk-rater-model-row-default"
            >
              <td class="py-1.5">
                <input
                  type="radio"
                  name="risk-rater-model"
                  value=""
                  :checked="selectedValue === ''"
                  aria-label="Use provider default (recommended)"
                  data-testid="risk-rater-model-radio-default"
                  @change="onRadioChange"
                />
              </td>
              <td class="py-1.5 pr-3 text-ink" colspan="5">
                Use provider default (recommended)
              </td>
            </tr>
            <tr
              v-for="row in modelRows"
              :key="row.value"
              class="border-b border-border-muted"
              :data-testid="`risk-rater-model-row-${row.value}`"
            >
              <td class="py-1.5">
                <input
                  type="radio"
                  name="risk-rater-model"
                  :value="row.value"
                  :checked="selectedValue === row.value"
                  :aria-label="`${row.providerLabel} • ${row.modelId}`"
                  :data-testid="`risk-rater-model-radio-${row.value}`"
                  @change="onRadioChange"
                />
              </td>
              <td class="py-1.5 pr-3 text-ink">
                {{ row.providerLabel }} • {{ row.modelId }}
              </td>
              <template v-if="row.bench && row.bench.data_available">
                <td class="py-1.5 pr-3 font-mono text-ink">
                  {{ row.bench.bands_exact }}/{{ row.bench.bands_adjacent }}/{{ row.bench.bands_miss }}
                </td>
                <td class="py-1.5 pr-3 font-mono text-ink">
                  {{ fmtPct(row.bench.reliability_5s_pct) }}
                </td>
                <td class="py-1.5 pr-3 font-mono text-ink">
                  {{ fmtMs(row.bench.median_latency_ms) }}
                </td>
                <td class="py-1.5 pr-3 font-mono text-ink">
                  {{ fmtCost(row.bench.cost_per_rating_usd) }}
                </td>
              </template>
              <template v-else>
                <td class="py-1.5 pr-3 text-ink-muted" colspan="4">
                  <span
                    class="rounded-sm border border-border-muted bg-surface-2 px-1.5 py-0.5 font-ui text-[10px] uppercase tracking-wide text-ink-subtle"
                    :data-testid="`risk-rater-unbenchmarked-badge-${row.value}`"
                  >
                    Unbenchmarked
                  </span>
                </td>
              </template>
            </tr>
          </tbody>
        </table>
      </div>
    </section>
  </div>
</template>
