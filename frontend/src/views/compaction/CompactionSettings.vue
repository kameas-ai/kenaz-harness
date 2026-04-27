<script setup lang="ts">
/**
 * CompactionSettings — configurable compaction settings view (mission
 * agent-kernel-graph; Bundle D WP12/WP13).
 *
 * Surfaces three things:
 *   - Per-site strategy picker (pre_call / post_tool / manual). Four
 *     strategies: summary, drop_oldest, semantic_cluster,
 *     custom_subgraph.
 *   - Per-scope config (global / project / session) with effective
 *     value + contributing-layer attribution.
 *   - Manual "compact now" trigger.
 *
 * The view always reads / writes through the harness client so the
 * Wails boundary stays the only state path.
 */
import { computed, onMounted, ref, watch } from 'vue';
import CanvasHead from '@/shell/CanvasHead.vue';
import { useHarnessClient } from '@/lib/useHarnessAPI';
import type {
  CompactionConfig,
  CompactionCustomStrategy,
  CompactionEffectiveConfig,
  CompactionLayer,
  CompactionSite,
  CompactionSiteConfig,
  CompactionStrategy,
} from '@/lib/types';

interface Props {
  // Optional pre-selected scope. When omitted the view starts at
  // global / "" so the user explicitly picks a project/session.
  initialLayer?: CompactionLayer;
  initialScopeId?: string;
  // sessionId is required for the manual-trigger button. When absent
  // the button is disabled.
  sessionId?: string;
}

const props = defineProps<Props>();

const client = useHarnessClient();

const layer = ref<CompactionLayer>(props.initialLayer ?? 'global');
const scopeId = ref<string>(props.initialScopeId ?? '');

const config = ref<CompactionConfig>({ sites: {} });
const effective = ref<CompactionEffectiveConfig>({
  config: { sites: {} },
  attribution: {},
});
const customStrategies = ref<readonly CompactionCustomStrategy[]>([]);

const loading = ref(false);
const saving = ref(false);
const triggering = ref(false);
const error = ref<string | null>(null);
const lastTriggerSummary = ref<string | null>(null);

const sites: readonly CompactionSite[] = ['pre_call', 'post_tool', 'manual'];
const layers: readonly CompactionLayer[] = [
  'global',
  'project',
  'session',
  'run',
  'node',
];
const strategies: readonly CompactionStrategy[] = [
  'summary',
  'drop_oldest',
  'semantic_cluster',
  'custom_subgraph',
];

const layerNeedsScopeId = computed(() => layer.value !== 'global');
const layerScopeIdLabel = computed(() => {
  switch (layer.value) {
    case 'project':
      return 'Project ID';
    case 'session':
      return 'Session ID';
    case 'run':
      return 'Run ID';
    case 'node':
      return 'RunID/NodeID';
    default:
      return '';
  }
});

const triggerScope = computed(() => {
  // Manual trigger always uses the layer/scope-id pair as a hint;
  // prop-supplied sessionId is the canonical session.
  return {
    projectId: layer.value === 'project' ? scopeId.value : undefined,
    sessionId: props.sessionId,
  };
});

async function refresh() {
  loading.value = true;
  error.value = null;
  try {
    const [cfg, eff, strs] = await Promise.all([
      client.compaction.getConfig(layer.value, scopeId.value),
      client.compaction.getEffective({
        projectId: layer.value === 'project' ? scopeId.value : undefined,
        sessionId:
          layer.value === 'session'
            ? scopeId.value
            : props.sessionId ?? undefined,
      }),
      client.compaction.listCustomStrategies(),
    ]);
    config.value = cfg.sites ? { sites: { ...cfg.sites } } : { sites: {} };
    effective.value = eff;
    customStrategies.value = strs;
  } catch (err) {
    error.value = err instanceof Error ? err.message : String(err);
  } finally {
    loading.value = false;
  }
}

function emptySite(): CompactionSiteConfig {
  return { enabled: true };
}

function siteConfig(site: CompactionSite): CompactionSiteConfig {
  return config.value.sites?.[site] ?? emptySite();
}

function effectiveSiteConfig(site: CompactionSite): CompactionSiteConfig {
  return effective.value.config.sites?.[site] ?? emptySite();
}

function attributionFor(
  site: CompactionSite,
  field: string,
): CompactionLayer | '' {
  return effective.value.attribution?.[site]?.[field] ?? '';
}

function updateSiteConfig(
  site: CompactionSite,
  patch: Partial<CompactionSiteConfig>,
) {
  const sites = { ...(config.value.sites ?? {}) };
  const current = sites[site] ?? emptySite();
  sites[site] = { ...current, ...patch };
  config.value = { sites };
}

async function save() {
  if (layerNeedsScopeId.value && !scopeId.value.trim()) {
    error.value = `${layerScopeIdLabel.value} required.`;
    return;
  }
  saving.value = true;
  error.value = null;
  try {
    await client.compaction.setConfig(
      layer.value,
      scopeId.value.trim(),
      config.value,
    );
    await refresh();
  } catch (err) {
    error.value = err instanceof Error ? err.message : String(err);
  } finally {
    saving.value = false;
  }
}

async function triggerManual(strategy?: CompactionStrategy) {
  if (!props.sessionId) {
    error.value = 'A sessionId is required to trigger manual compaction.';
    return;
  }
  triggering.value = true;
  error.value = null;
  lastTriggerSummary.value = null;
  try {
    const res = await client.compaction.triggerManualCompaction(
      props.sessionId,
      { strategy },
    );
    if (res.skipped) {
      lastTriggerSummary.value = `Skipped (${res.reason ?? 'no reason'})`;
    } else {
      lastTriggerSummary.value = `Compacted via ${res.strategy}: ${res.bytesSaved} bytes saved.`;
    }
  } catch (err) {
    error.value = err instanceof Error ? err.message : String(err);
  } finally {
    triggering.value = false;
  }
}

function siteLabel(site: CompactionSite): string {
  switch (site) {
    case 'pre_call':
      return 'Pre-call (token budget)';
    case 'post_tool':
      return 'Post-tool result trim';
    case 'manual':
      return 'Manual trigger';
  }
}

function strategyLabel(s: CompactionStrategy): string {
  switch (s) {
    case 'summary':
      return 'Summary (LLM)';
    case 'drop_oldest':
      return 'Drop oldest';
    case 'semantic_cluster':
      return 'Semantic cluster';
    case 'custom_subgraph':
      return 'Custom subgraph';
  }
}

watch([layer, scopeId], () => {
  void refresh();
});

onMounted(() => {
  void refresh();
});

defineExpose({ refresh, save, triggerManual });
</script>

<template>
  <div>
    <CanvasHead
      number="12"
      section="COMPACTION"
      title="Compaction"
      subtitle="Configure when and how the kernel shrinks long-running context. Cascading scopes: global → project → session → run → node."
    >
      <template #trailing>
        <button
          v-if="props.sessionId"
          type="button"
          class="rounded-sm border border-accent bg-surface-2 px-3 py-1 font-ui text-[12px] uppercase tracking-[0.18em] text-accent disabled:opacity-50"
          data-testid="compaction-trigger-now"
          :disabled="triggering"
          @click="triggerManual()"
        >
          {{ triggering ? 'Compacting…' : 'Compact now' }}
        </button>
      </template>
    </CanvasHead>

    <div class="px-6 py-4 max-w-4xl space-y-6">
      <div
        v-if="error"
        class="rounded-md border border-signal-danger bg-surface-1 px-3 py-2 font-ui text-[12px] text-signal-danger"
        role="alert"
        data-testid="compaction-error"
      >
        {{ error }}
      </div>
      <div
        v-if="lastTriggerSummary"
        class="rounded-md border border-border-muted bg-surface-1 px-3 py-2 font-ui text-[12px] text-ink-muted"
        data-testid="compaction-trigger-summary"
      >
        {{ lastTriggerSummary }}
      </div>

      <section class="rounded-md border border-border-muted bg-surface-1 p-4">
        <h3 class="mb-3 font-ui text-[13px] uppercase tracking-[0.18em] text-ink-dim">
          Scope
        </h3>
        <div class="flex flex-wrap items-end gap-4">
          <label class="flex flex-col font-ui text-[12px] text-ink-muted">
            Layer
            <select
              v-model="layer"
              class="mt-1 rounded-sm border border-border-muted bg-surface-2 px-2 py-1 text-[12px] text-ink"
              data-testid="compaction-layer-select"
            >
              <option v-for="l in layers" :key="l" :value="l">{{ l }}</option>
            </select>
          </label>
          <label
            v-if="layerNeedsScopeId"
            class="flex flex-col font-ui text-[12px] text-ink-muted"
          >
            {{ layerScopeIdLabel }}
            <input
              v-model="scopeId"
              type="text"
              class="mt-1 rounded-sm border border-border-muted bg-surface-2 px-2 py-1 text-[12px] text-ink"
              data-testid="compaction-scope-id"
            />
          </label>
          <button
            type="button"
            class="rounded-sm border border-border-muted bg-surface-2 px-3 py-1 font-ui text-[12px] uppercase tracking-[0.18em] text-ink-muted hover:bg-surface-3 disabled:opacity-50"
            data-testid="compaction-save"
            :disabled="saving"
            @click="save"
          >
            {{ saving ? 'Saving…' : 'Save layer config' }}
          </button>
        </div>
      </section>

      <section
        v-for="site in sites"
        :key="site"
        class="rounded-md border border-border-muted bg-surface-1 p-4"
        :data-testid="`compaction-site-${site}`"
      >
        <header class="mb-3 flex items-center justify-between">
          <h3 class="font-ui text-[13px] uppercase tracking-[0.18em] text-ink-dim">
            {{ siteLabel(site) }}
          </h3>
          <span
            v-if="effectiveSiteConfig(site).strategy"
            class="rounded-sm border border-border-muted px-2 py-0 font-ui text-[10px] uppercase tracking-[0.18em] text-ink-dim"
            :data-testid="`compaction-effective-${site}`"
          >
            Effective: {{ strategyLabel(effectiveSiteConfig(site).strategy!) }}
            <span
              v-if="attributionFor(site, 'strategy')"
              class="ml-1 text-accent"
              :data-testid="`compaction-attribution-${site}-strategy`"
            >
              [{{ attributionFor(site, 'strategy') }}]
            </span>
          </span>
        </header>

        <div class="grid grid-cols-2 gap-3">
          <label class="flex flex-col font-ui text-[12px] text-ink-muted">
            <span>Enabled</span>
            <input
              type="checkbox"
              :checked="siteConfig(site).enabled"
              class="mt-1 h-4 w-4"
              :data-testid="`compaction-${site}-enabled`"
              @change="
                updateSiteConfig(site, {
                  enabled: ($event.target as HTMLInputElement).checked,
                })
              "
            />
          </label>
          <label class="flex flex-col font-ui text-[12px] text-ink-muted">
            <span>Strategy</span>
            <select
              :value="siteConfig(site).strategy ?? ''"
              class="mt-1 rounded-sm border border-border-muted bg-surface-2 px-2 py-1 text-[12px] text-ink"
              :data-testid="`compaction-${site}-strategy`"
              @change="
                updateSiteConfig(site, {
                  strategy:
                    (($event.target as HTMLSelectElement).value as CompactionStrategy) ||
                    undefined,
                })
              "
            >
              <option value="">— inherit —</option>
              <option v-for="s in strategies" :key="s" :value="s">
                {{ strategyLabel(s) }}
              </option>
            </select>
          </label>
          <label
            v-if="site === 'pre_call'"
            class="flex flex-col font-ui text-[12px] text-ink-muted"
          >
            <span>Pre-call threshold (0.0–1.0)</span>
            <input
              type="number"
              step="0.05"
              min="0"
              max="1"
              :value="siteConfig(site).preCallThreshold ?? ''"
              class="mt-1 rounded-sm border border-border-muted bg-surface-2 px-2 py-1 text-[12px] text-ink"
              :data-testid="`compaction-${site}-threshold`"
              @input="
                updateSiteConfig(site, {
                  preCallThreshold:
                    Number.parseFloat(($event.target as HTMLInputElement).value) ||
                    undefined,
                })
              "
            />
          </label>
          <label
            v-if="site === 'post_tool'"
            class="flex flex-col font-ui text-[12px] text-ink-muted"
          >
            <span>Tool result max bytes</span>
            <input
              type="number"
              :value="siteConfig(site).toolResultMaxBytes ?? ''"
              class="mt-1 rounded-sm border border-border-muted bg-surface-2 px-2 py-1 text-[12px] text-ink"
              :data-testid="`compaction-${site}-max-bytes`"
              @input="
                updateSiteConfig(site, {
                  toolResultMaxBytes:
                    Number.parseInt(($event.target as HTMLInputElement).value, 10) ||
                    undefined,
                })
              "
            />
          </label>
          <label class="flex flex-col font-ui text-[12px] text-ink-muted">
            <span>Max recursion depth</span>
            <input
              type="number"
              min="0"
              :value="siteConfig(site).maxRecursionDepth ?? ''"
              class="mt-1 rounded-sm border border-border-muted bg-surface-2 px-2 py-1 text-[12px] text-ink"
              :data-testid="`compaction-${site}-max-depth`"
              @input="
                updateSiteConfig(site, {
                  maxRecursionDepth:
                    Number.parseInt(($event.target as HTMLInputElement).value, 10) ||
                    undefined,
                })
              "
            />
          </label>
          <label
            v-if="siteConfig(site).strategy === 'custom_subgraph'"
            class="col-span-2 flex flex-col font-ui text-[12px] text-ink-muted"
          >
            <span>Custom strategy</span>
            <select
              :value="siteConfig(site).customGraphId ?? ''"
              class="mt-1 rounded-sm border border-border-muted bg-surface-2 px-2 py-1 text-[12px] text-ink"
              :data-testid="`compaction-${site}-custom-graph`"
              @change="
                updateSiteConfig(site, {
                  customGraphId:
                    ($event.target as HTMLSelectElement).value || undefined,
                })
              "
            >
              <option value="">— pick a graph —</option>
              <option
                v-for="cs in customStrategies"
                :key="cs.graphId"
                :value="cs.graphId"
              >
                {{ cs.name }}
              </option>
            </select>
          </label>
        </div>
      </section>
    </div>
  </div>
</template>
