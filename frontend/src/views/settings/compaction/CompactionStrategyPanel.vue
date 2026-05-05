<script setup lang="ts">
/**
 * CompactionStrategyPanel — strategy-authoring UI for the Settings hub
 * (mission compaction-strategy-ui-01KQ8TD8).
 *
 * Surfaces in the Settings "Compaction" tab (?tab=compaction). Four
 * sections:
 *
 *   1. Strategy picker — dropdown of built-in strategies + "custom", plus
 *      per-strategy parameter form (threshold tokens, threshold messages,
 *      summary length).
 *   2. Live preview — "what gets compacted" using a sample session timeline
 *      driven by the EffectiveConfig cascade.
 *   3. Aggressiveness tier — the five-stop tier dial (mirrors the one on the
 *      General tab so both entry points are coherent).
 *   4. Per-scope override — project-level / session-level can opt in to a
 *      different strategy than the global default.
 *
 * All reads go through client.compaction.getEffective /
 * client.compaction.getConfig. Writes go through client.compaction.setConfig
 * for the per-site config and client.settings.set for the tier field.
 *
 * Cedar: saves are gated by the same settings-write path used everywhere in
 * SettingsView — no new Cedar action is needed beyond the existing
 * harness:settings:write grant.
 */
import { computed, onMounted, ref, watch } from 'vue';
import { useHarnessClient } from '@/lib/useHarnessAPI';
import { debouncedSave } from '@/lib/settings';
import type {
  CompactionAggressiveness,
  CompactionConfig,
  CompactionCustomStrategy,
  CompactionEffectiveConfig,
  CompactionLayer,
  CompactionSite,
  CompactionSiteConfig,
  CompactionStrategy,
  CompactionTierExplain,
  Settings,
} from '@/lib/types';

const client = useHarnessClient();

// ── aggressiveness tier ─────────────────────────────────────────────────

const COMPACTION_TIERS: ReadonlyArray<CompactionAggressiveness> = [
  'off',
  'conservative',
  'balanced',
  'aggressive',
  'maximal',
];

const compactionTier = ref<CompactionAggressiveness>('balanced');
const tierExplainRows = ref<CompactionTierExplain[]>([]);
const explainOpen = ref(false);
const settings = ref<Settings>({
  schemaVersion: 1,
  lastRoute: '/sessions',
  theme: 'system',
  accent: 'default',
  windowSize: { width: 1280, height: 800 },
  memoryEnabled: false,
  confirmEachDisabled: false,
});

const selectedTierExplain = computed<CompactionTierExplain | null>(() => {
  return (
    tierExplainRows.value.find(
      (row) => row.aggressiveness === compactionTier.value,
    ) ?? null
  );
});

function setCompactionTier(t: CompactionAggressiveness) {
  compactionTier.value = t;
  debouncedSave(client, {
    ...settings.value,
    compactionAggressiveness: t,
  });
}

// ── per-site strategy config (global layer) ─────────────────────────────

const sites: readonly CompactionSite[] = ['pre_call', 'post_tool', 'manual'];
const strategies: readonly CompactionStrategy[] = [
  'summary',
  'drop_oldest',
  'semantic_cluster',
  'custom_subgraph',
];

const globalConfig = ref<CompactionConfig>({ sites: {} });
const effectiveConfig = ref<CompactionEffectiveConfig>({
  config: { sites: {} },
  attribution: {},
});
const customStrategies = ref<readonly CompactionCustomStrategy[]>([]);

const loading = ref(false);
const saving = ref(false);
const error = ref<string | null>(null);
const saveSuccess = ref(false);

function emptySite(): CompactionSiteConfig {
  return { enabled: true };
}

function siteConfig(site: CompactionSite): CompactionSiteConfig {
  return globalConfig.value.sites?.[site] ?? emptySite();
}

function effectiveSiteConfig(site: CompactionSite): CompactionSiteConfig {
  return effectiveConfig.value.config.sites?.[site] ?? emptySite();
}

function attributionFor(site: CompactionSite, field: string): CompactionLayer | '' {
  return effectiveConfig.value.attribution?.[site]?.[field] ?? '';
}

function updateSiteConfig(site: CompactionSite, patch: Partial<CompactionSiteConfig>) {
  const sitesCopy = { ...(globalConfig.value.sites ?? {}) };
  const current = sitesCopy[site] ?? emptySite();
  sitesCopy[site] = { ...current, ...patch };
  globalConfig.value = { sites: sitesCopy };
}

async function saveGlobalConfig() {
  saving.value = true;
  error.value = null;
  saveSuccess.value = false;
  try {
    await client.compaction.setConfig('global', '', globalConfig.value);
    await refreshEffective();
    saveSuccess.value = true;
    setTimeout(() => { saveSuccess.value = false; }, 2000);
  } catch (err) {
    error.value = err instanceof Error ? err.message : String(err);
  } finally {
    saving.value = false;
  }
}

// ── per-scope override ──────────────────────────────────────────────────

type OverrideScope = 'project' | 'session';

const overrideScope = ref<OverrideScope>('project');
const overrideScopeId = ref('');
const overrideConfig = ref<CompactionConfig>({ sites: {} });
const overrideEffective = ref<CompactionEffectiveConfig>({
  config: { sites: {} },
  attribution: {},
});
const overrideLoading = ref(false);
const overrideSaving = ref(false);
const overrideError = ref<string | null>(null);
const overrideSaveSuccess = ref(false);

const overrideLayer = computed<CompactionLayer>(() =>
  overrideScope.value === 'project' ? 'project' : 'session',
);

const overrideScopeIdLabel = computed(() =>
  overrideScope.value === 'project' ? 'Project ID' : 'Session ID',
);

async function loadOverrideConfig() {
  if (!overrideScopeId.value.trim()) return;
  overrideLoading.value = true;
  overrideError.value = null;
  try {
    const [cfg, eff] = await Promise.all([
      client.compaction.getConfig(overrideLayer.value, overrideScopeId.value.trim()),
      client.compaction.getEffective({
        projectId: overrideScope.value === 'project' ? overrideScopeId.value.trim() : undefined,
        sessionId: overrideScope.value === 'session' ? overrideScopeId.value.trim() : undefined,
      }),
    ]);
    overrideConfig.value = cfg.sites ? { sites: { ...cfg.sites } } : { sites: {} };
    overrideEffective.value = eff;
  } catch (err) {
    overrideError.value = err instanceof Error ? err.message : String(err);
  } finally {
    overrideLoading.value = false;
  }
}

function updateOverrideSiteConfig(site: CompactionSite, patch: Partial<CompactionSiteConfig>) {
  const sitesCopy = { ...(overrideConfig.value.sites ?? {}) };
  const current = sitesCopy[site] ?? emptySite();
  sitesCopy[site] = { ...current, ...patch };
  overrideConfig.value = { sites: sitesCopy };
}

function overrideSiteConfig(site: CompactionSite): CompactionSiteConfig {
  return overrideConfig.value.sites?.[site] ?? emptySite();
}

function overrideEffectiveSiteConfig(site: CompactionSite): CompactionSiteConfig {
  return overrideEffective.value.config.sites?.[site] ?? emptySite();
}

function overrideAttributionFor(site: CompactionSite, field: string): CompactionLayer | '' {
  return overrideEffective.value.attribution?.[site]?.[field] ?? '';
}

async function saveOverrideConfig() {
  if (!overrideScopeId.value.trim()) {
    overrideError.value = `${overrideScopeIdLabel.value} required.`;
    return;
  }
  overrideSaving.value = true;
  overrideError.value = null;
  overrideSaveSuccess.value = false;
  try {
    await client.compaction.setConfig(
      overrideLayer.value,
      overrideScopeId.value.trim(),
      overrideConfig.value,
    );
    await loadOverrideConfig();
    overrideSaveSuccess.value = true;
    setTimeout(() => { overrideSaveSuccess.value = false; }, 2000);
  } catch (err) {
    overrideError.value = err instanceof Error ? err.message : String(err);
  } finally {
    overrideSaving.value = false;
  }
}

// ── live preview ────────────────────────────────────────────────────────

/**
 * A fixed sample "session" used to show what the effective cascade would
 * compact. Each entry is a rough turn label + approximate token size.
 */
const SAMPLE_TURNS = [
  { id: 1, label: 'User: "Scaffold a REST API for orders"', tokens: 320 },
  { id: 2, label: 'Assistant: Full scaffold with routes, models, tests', tokens: 2400 },
  { id: 3, label: 'User: "Add JWT auth"', tokens: 180 },
  { id: 4, label: 'Assistant: JWT middleware + token utilities', tokens: 1800 },
  { id: 5, label: 'User: "Write integration tests"', tokens: 140 },
  { id: 6, label: 'Assistant: Full integration test suite', tokens: 3100 },
  { id: 7, label: 'User: "Add rate limiting"', tokens: 120 },
  { id: 8, label: 'Assistant: Rate limiting with Redis', tokens: 1600 },
];
const SAMPLE_TOTAL_TOKENS = SAMPLE_TURNS.reduce((s, t) => s + t.tokens, 0);
const MOCK_CONTEXT_CAP = 16000;

/**
 * Derived preview: given the effective cascade (tier + strategy), simulate
 * which turns would be compacted on the next pre_call trigger.
 *
 * Rules applied (mirrors core/compaction policy.go):
 *  - off    → nothing compacted
 *  - rolling → all but the last `recentN` are folded (use recentN=2 for preview)
 *  - threshold → if usagePct >= triggerPct, compact oldest `summarizePct` fraction
 */
const previewResult = computed(() => {
  const usagePct = SAMPLE_TOTAL_TOKENS / MOCK_CONTEXT_CAP;
  const tier = tierExplainRows.value.find(r => r.aggressiveness === compactionTier.value);
  if (!tier) return { compacted: [] as number[], kept: SAMPLE_TURNS.map(t => t.id), note: 'Loading tier…' };

  if (tier.mode === 'none') {
    return { compacted: [] as number[], kept: SAMPLE_TURNS.map(t => t.id), note: 'Off — nothing compacted.' };
  }

  const preCallSiteEff = effectiveSiteConfig('pre_call');
  const threshold = preCallSiteEff.preCallThreshold ?? tier.triggerPct;

  if (tier.mode === 'threshold') {
    if (usagePct < threshold) {
      return {
        compacted: [] as number[],
        kept: SAMPLE_TURNS.map(t => t.id),
        note: `Usage ${Math.round(usagePct * 100)}% < trigger ${Math.round(threshold * 100)}% — no compaction needed.`,
      };
    }
    // Compact oldest summarizePct fraction of turns.
    const compactCount = Math.ceil(SAMPLE_TURNS.length * tier.summarizePct);
    const compacted = SAMPLE_TURNS.slice(0, compactCount).map(t => t.id);
    const kept = SAMPLE_TURNS.slice(compactCount).map(t => t.id);
    return {
      compacted,
      kept,
      note: `${Math.round(usagePct * 100)}% usage ≥ ${Math.round(threshold * 100)}% trigger — ${compacted.length} oldest turn${compacted.length !== 1 ? 's' : ''} compacted.`,
    };
  }

  // rolling — keep last 2 turns
  const recentN = 2;
  const compacted = SAMPLE_TURNS.slice(0, SAMPLE_TURNS.length - recentN).map(t => t.id);
  const kept = SAMPLE_TURNS.slice(SAMPLE_TURNS.length - recentN).map(t => t.id);
  return { compacted, kept, note: `Rolling mode — all but the last ${recentN} turns summarised on every exchange.` };
});

// ── strategy + field labels ─────────────────────────────────────────────

function siteLabel(site: CompactionSite): string {
  switch (site) {
    case 'pre_call': return 'Pre-call (token budget)';
    case 'post_tool': return 'Post-tool result trim';
    case 'manual': return 'Manual trigger';
  }
}

function strategyLabel(s: CompactionStrategy | undefined | ''): string {
  switch (s) {
    case 'summary': return 'Summary (LLM)';
    case 'drop_oldest': return 'Drop oldest';
    case 'semantic_cluster': return 'Semantic cluster';
    case 'custom_subgraph': return 'Custom subgraph';
    default: return '— inherit —';
  }
}

function summaryLengthLabel(s: CompactionStrategy | undefined): string {
  switch (s) {
    case 'drop_oldest': return 'Keep recent N turns';
    case 'semantic_cluster': return 'Cluster count';
    case 'summary': return 'Summary model';
    default: return '';
  }
}

// ── data loading ────────────────────────────────────────────────────────

async function refreshEffective() {
  try {
    const eff = await client.compaction.getEffective({});
    effectiveConfig.value = eff;
  } catch {
    // Keep last-known-good.
  }
}

async function refresh() {
  loading.value = true;
  error.value = null;
  try {
    const [settingsData, tierData, cfg, eff, strs] = await Promise.all([
      client.settings.get(),
      client.compaction.getTierExplain(),
      client.compaction.getConfig('global', ''),
      client.compaction.getEffective({}),
      client.compaction.listCustomStrategies(),
    ]);
    settings.value = settingsData;
    compactionTier.value =
      (settingsData.compactionAggressiveness as CompactionAggressiveness) || 'balanced';
    tierExplainRows.value = tierData;
    globalConfig.value = cfg.sites ? { sites: { ...cfg.sites } } : { sites: {} };
    effectiveConfig.value = eff;
    customStrategies.value = strs;
  } catch (err) {
    error.value = err instanceof Error ? err.message : String(err);
  } finally {
    loading.value = false;
  }
}

watch([overrideScope, overrideScopeId], () => {
  if (overrideScopeId.value.trim()) {
    void loadOverrideConfig();
  }
});

onMounted(() => {
  void refresh();
});

defineExpose({ refresh, saveGlobalConfig });
</script>

<template>
  <div class="px-6 py-4 max-w-4xl space-y-8" data-testid="compaction-strategy-panel">
    <!-- Global error banner -->
    <div
      v-if="error"
      class="rounded-md border border-signal-danger bg-surface-1 px-3 py-2 font-ui text-[12px] text-signal-danger"
      role="alert"
      data-testid="csp-error"
    >
      {{ error }}
    </div>

    <!-- 1. Aggressiveness tier ─────────────────────────────────────────── -->
    <section data-testid="csp-tier-section">
      <h2 class="font-ui text-[11px] uppercase tracking-[0.18em] text-ink-subtle">
        Aggressiveness tier
      </h2>
      <p class="mt-1 font-ui text-[11px] text-ink-muted">
        Controls when compaction fires and how much history is summarised.
        This tier is shared with the General settings panel — changes here
        are reflected there immediately.
      </p>

      <div
        class="mt-3 inline-flex rounded-sm border border-border"
        role="radiogroup"
        aria-label="Compaction aggressiveness"
      >
        <button
          v-for="t in COMPACTION_TIERS"
          :key="t"
          type="button"
          role="radio"
          :aria-checked="compactionTier === t"
          class="px-3 py-1.5 font-ui text-[12px] border-r border-border last:border-r-0 transition-colors capitalize"
          :class="compactionTier === t
            ? 'bg-surface-3 text-ink'
            : 'bg-surface-1 text-ink-muted hover:text-ink'"
          :data-testid="`csp-tier-${t}`"
          @click="setCompactionTier(t)"
        >
          {{ t }}
        </button>
      </div>

      <button
        type="button"
        class="mt-2 font-ui text-[11px] text-accent hover:underline"
        data-testid="csp-explain-toggle"
        :aria-expanded="explainOpen"
        @click="explainOpen = !explainOpen"
      >
        {{ explainOpen ? 'Hide' : 'What does this mean?' }}
      </button>

      <div
        v-if="explainOpen"
        class="mt-2 rounded-sm border border-border bg-surface-1 p-3 font-ui text-[12px]"
        data-testid="csp-explain-disclosure"
      >
        <div v-if="selectedTierExplain" class="space-y-1">
          <div class="font-medium text-ink" data-testid="csp-explain-label">
            {{ selectedTierExplain.label }}
          </div>
          <p class="text-ink-muted" data-testid="csp-explain-description">
            {{ selectedTierExplain.description }}
          </p>
          <dl
            class="mt-2 grid gap-1 font-mono text-[11px] text-ink-muted"
            style="grid-template-columns: 14ch 1fr"
            data-testid="csp-explain-numerics"
          >
            <dt>Trigger %</dt>
            <dd>{{ selectedTierExplain.triggerPct > 0
              ? `${Math.round(selectedTierExplain.triggerPct * 100)}% of cap`
              : '—' }}</dd>
            <dt>Summarize %</dt>
            <dd>{{ selectedTierExplain.summarizePct > 0
              ? `${Math.round(selectedTierExplain.summarizePct * 100)}% of oldest tokens`
              : '—' }}</dd>
            <dt>Mode</dt>
            <dd>{{ selectedTierExplain.mode }}</dd>
          </dl>
        </div>
        <div v-else class="text-ink-muted">
          No explain row available — using locked defaults.
        </div>
      </div>
    </section>

    <!-- 2. Per-site strategy picker (global layer) ─────────────────────── -->
    <section data-testid="csp-global-strategies-section">
      <h2 class="font-ui text-[11px] uppercase tracking-[0.18em] text-ink-subtle">
        Strategy per trigger site
        <span class="ml-1 text-ink-dim font-normal normal-case tracking-normal">(global layer)</span>
      </h2>
      <p class="mt-1 font-ui text-[11px] text-ink-muted">
        Choose a compaction algorithm for each trigger site. "— inherit —" falls
        through to the engine's built-in default. Project and session overrides can
        be set in the Scope override section below.
      </p>

      <div
        v-if="loading"
        class="mt-3 font-ui text-[12px] text-ink-muted"
        data-testid="csp-loading"
      >
        Loading…
      </div>

      <div v-else class="mt-3 space-y-4">
        <div
          v-for="site in sites"
          :key="site"
          class="rounded-md border border-border-muted bg-surface-1 p-4"
          :data-testid="`csp-site-${site}`"
        >
          <header class="mb-3 flex items-center justify-between">
            <h3 class="font-ui text-[13px] uppercase tracking-[0.18em] text-ink-dim">
              {{ siteLabel(site) }}
            </h3>
            <span
              v-if="effectiveSiteConfig(site).strategy"
              class="rounded-sm border border-border-muted px-2 py-0 font-ui text-[10px] uppercase tracking-[0.18em] text-ink-dim"
              :data-testid="`csp-effective-${site}`"
            >
              Effective: {{ strategyLabel(effectiveSiteConfig(site).strategy) }}
              <span
                v-if="attributionFor(site, 'strategy')"
                class="ml-1 text-accent"
                :data-testid="`csp-attribution-${site}`"
              >
                [{{ attributionFor(site, 'strategy') }}]
              </span>
            </span>
          </header>

          <div class="grid grid-cols-2 gap-3">
            <!-- Enabled toggle -->
            <label class="flex items-center gap-2 font-ui text-[12px] text-ink-muted col-span-2">
              <input
                type="checkbox"
                :checked="siteConfig(site).enabled"
                class="h-4 w-4 accent-accent"
                :data-testid="`csp-${site}-enabled`"
                @change="updateSiteConfig(site, { enabled: ($event.target as HTMLInputElement).checked })"
              />
              Enabled
            </label>

            <!-- Strategy picker -->
            <label class="flex flex-col font-ui text-[12px] text-ink-muted">
              <span>Strategy</span>
              <select
                :value="siteConfig(site).strategy ?? ''"
                class="mt-1 rounded-sm border border-border-muted bg-surface-2 px-2 py-1 text-[12px] text-ink"
                :data-testid="`csp-${site}-strategy`"
                @change="updateSiteConfig(site, {
                  strategy: (($event.target as HTMLSelectElement).value as CompactionStrategy) || undefined,
                })"
              >
                <option value="">— inherit —</option>
                <option v-for="s in strategies" :key="s" :value="s">
                  {{ strategyLabel(s) }}
                </option>
              </select>
            </label>

            <!-- Pre-call threshold (only for pre_call site) -->
            <label
              v-if="site === 'pre_call'"
              class="flex flex-col font-ui text-[12px] text-ink-muted"
            >
              <span>Threshold (0.0–1.0)</span>
              <input
                type="number"
                step="0.05"
                min="0"
                max="1"
                :value="siteConfig(site).preCallThreshold ?? ''"
                class="mt-1 rounded-sm border border-border-muted bg-surface-2 px-2 py-1 text-[12px] text-ink w-24"
                :data-testid="`csp-${site}-threshold`"
                @input="updateSiteConfig(site, {
                  preCallThreshold: Number.parseFloat(($event.target as HTMLInputElement).value) || undefined,
                })"
              />
            </label>

            <!-- Tool result max bytes (only for post_tool site) -->
            <label
              v-if="site === 'post_tool'"
              class="flex flex-col font-ui text-[12px] text-ink-muted"
            >
              <span>Tool result max bytes</span>
              <input
                type="number"
                min="0"
                :value="siteConfig(site).toolResultMaxBytes ?? ''"
                class="mt-1 rounded-sm border border-border-muted bg-surface-2 px-2 py-1 text-[12px] text-ink w-28"
                :data-testid="`csp-${site}-max-bytes`"
                @input="updateSiteConfig(site, {
                  toolResultMaxBytes: Number.parseInt(($event.target as HTMLInputElement).value, 10) || undefined,
                })"
              />
            </label>

            <!-- Drop-oldest keep-recent-N (for drop_oldest strategy) -->
            <label
              v-if="siteConfig(site).strategy === 'drop_oldest'"
              class="flex flex-col font-ui text-[12px] text-ink-muted"
            >
              <span>{{ summaryLengthLabel('drop_oldest') }}</span>
              <input
                type="number"
                min="1"
                :value="siteConfig(site).dropOldestKeepRecentN ?? ''"
                class="mt-1 rounded-sm border border-border-muted bg-surface-2 px-2 py-1 text-[12px] text-ink w-24"
                :data-testid="`csp-${site}-keep-recent-n`"
                @input="updateSiteConfig(site, {
                  dropOldestKeepRecentN: Number.parseInt(($event.target as HTMLInputElement).value, 10) || undefined,
                })"
              />
            </label>

            <!-- Semantic cluster count (for semantic_cluster strategy) -->
            <label
              v-if="siteConfig(site).strategy === 'semantic_cluster'"
              class="flex flex-col font-ui text-[12px] text-ink-muted"
            >
              <span>{{ summaryLengthLabel('semantic_cluster') }}</span>
              <input
                type="number"
                min="1"
                :value="siteConfig(site).semanticClusterCount ?? ''"
                class="mt-1 rounded-sm border border-border-muted bg-surface-2 px-2 py-1 text-[12px] text-ink w-24"
                :data-testid="`csp-${site}-cluster-count`"
                @input="updateSiteConfig(site, {
                  semanticClusterCount: Number.parseInt(($event.target as HTMLInputElement).value, 10) || undefined,
                })"
              />
            </label>

            <!-- Summary model (for summary strategy) -->
            <label
              v-if="siteConfig(site).strategy === 'summary'"
              class="flex flex-col font-ui text-[12px] text-ink-muted"
            >
              <span>Summary model (optional)</span>
              <input
                type="text"
                placeholder="Use session's active model"
                :value="siteConfig(site).summaryModel ?? ''"
                class="mt-1 rounded-sm border border-border-muted bg-surface-2 px-2 py-1 text-[12px] text-ink"
                :data-testid="`csp-${site}-summary-model`"
                @input="updateSiteConfig(site, {
                  summaryModel: ($event.target as HTMLInputElement).value || undefined,
                })"
              />
            </label>

            <!-- Custom subgraph picker (for custom_subgraph strategy) -->
            <label
              v-if="siteConfig(site).strategy === 'custom_subgraph'"
              class="col-span-2 flex flex-col font-ui text-[12px] text-ink-muted"
            >
              <span>Custom strategy graph</span>
              <select
                :value="siteConfig(site).customGraphId ?? ''"
                class="mt-1 rounded-sm border border-border-muted bg-surface-2 px-2 py-1 text-[12px] text-ink"
                :data-testid="`csp-${site}-custom-graph`"
                @change="updateSiteConfig(site, {
                  customGraphId: ($event.target as HTMLSelectElement).value || undefined,
                })"
              >
                <option value="">— pick a graph —</option>
                <option v-for="cs in customStrategies" :key="cs.graphId" :value="cs.graphId">
                  {{ cs.name }}
                </option>
              </select>
              <p
                v-if="customStrategies.length === 0"
                class="mt-1 font-ui text-[11px] text-ink-dim"
              >
                No custom strategies registered. They appear here once the
                agent-graph library ships custom_subgraph nodes.
              </p>
            </label>
          </div>
        </div>

        <div class="flex items-center gap-3">
          <button
            type="button"
            class="rounded-sm border border-accent bg-surface-2 px-4 py-1.5 font-ui text-[12px] uppercase tracking-[0.18em] text-accent hover:bg-accent-glow disabled:opacity-50"
            data-testid="csp-save-global"
            :disabled="saving"
            @click="saveGlobalConfig"
          >
            {{ saving ? 'Saving…' : 'Save global config' }}
          </button>
          <span
            v-if="saveSuccess"
            class="font-ui text-[11px] text-signal-success"
            data-testid="csp-save-success"
          >
            Saved.
          </span>
        </div>
      </div>
    </section>

    <!-- 3. Live preview ────────────────────────────────────────────────── -->
    <section data-testid="csp-preview-section">
      <h2 class="font-ui text-[11px] uppercase tracking-[0.18em] text-ink-subtle">
        Live preview
      </h2>
      <p class="mt-1 font-ui text-[11px] text-ink-muted">
        What the effective cascade would compact in a sample {{ SAMPLE_TURNS.length }}-turn session
        ({{ SAMPLE_TOTAL_TOKENS.toLocaleString() }} tokens / {{ MOCK_CONTEXT_CAP.toLocaleString() }} cap).
      </p>

      <div
        class="mt-3 rounded-md border border-border-muted bg-surface-1 p-4"
        data-testid="csp-preview-card"
      >
        <p
          class="mb-3 font-ui text-[11px] text-ink-muted"
          data-testid="csp-preview-note"
        >
          {{ previewResult.note }}
        </p>
        <ul class="space-y-1">
          <li
            v-for="turn in SAMPLE_TURNS"
            :key="turn.id"
            class="flex items-center gap-2 font-ui text-[12px]"
            :data-testid="`csp-preview-turn-${turn.id}`"
          >
            <span
              class="inline-block w-5 h-5 flex-shrink-0 rounded-sm text-center text-[10px] leading-5"
              :class="previewResult.compacted.includes(turn.id)
                ? 'bg-signal-warning/20 text-signal-warning'
                : 'bg-surface-2 text-ink-muted'"
            >
              {{ previewResult.compacted.includes(turn.id) ? '↓' : '·' }}
            </span>
            <span
              :class="previewResult.compacted.includes(turn.id)
                ? 'text-ink-dim line-through'
                : 'text-ink'"
            >
              {{ turn.label }}
            </span>
            <span class="ml-auto font-mono text-[10px] text-ink-dim">
              {{ turn.tokens.toLocaleString() }}t
            </span>
          </li>
        </ul>
        <p
          v-if="previewResult.compacted.length > 0"
          class="mt-3 font-ui text-[11px] text-signal-warning"
          data-testid="csp-preview-compacted-count"
        >
          {{ previewResult.compacted.length }} turn{{ previewResult.compacted.length !== 1 ? 's' : '' }}
          would be compacted (strikethrough). {{ previewResult.kept.length }} kept.
        </p>
      </div>
    </section>

    <!-- 4. Per-scope override ──────────────────────────────────────────── -->
    <section data-testid="csp-scope-override-section">
      <h2 class="font-ui text-[11px] uppercase tracking-[0.18em] text-ink-subtle">
        Scope override
      </h2>
      <p class="mt-1 font-ui text-[11px] text-ink-muted">
        A project or session can opt into a different strategy than the global
        default. Enter its ID to load, edit, and save a per-scope override.
        The cascade priority is global → project → session → run → node.
      </p>

      <div class="mt-3 flex flex-wrap items-end gap-3">
        <label class="flex flex-col font-ui text-[12px] text-ink-muted">
          Scope
          <div
            class="mt-1 inline-flex rounded-sm border border-border-muted"
            role="radiogroup"
            aria-label="Override scope"
          >
            <button
              v-for="s in (['project', 'session'] as const)"
              :key="s"
              type="button"
              role="radio"
              :aria-checked="overrideScope === s"
              class="px-3 py-1.5 font-ui text-[12px] border-r border-border-muted last:border-r-0 transition-colors capitalize"
              :class="overrideScope === s
                ? 'bg-surface-3 text-ink'
                : 'bg-surface-1 text-ink-muted hover:text-ink'"
              :data-testid="`csp-override-scope-${s}`"
              @click="overrideScope = s; overrideScopeId = ''; overrideConfig = { sites: {} }"
            >
              {{ s }}
            </button>
          </div>
        </label>

        <label class="flex flex-col font-ui text-[12px] text-ink-muted">
          {{ overrideScopeIdLabel }}
          <input
            v-model="overrideScopeId"
            type="text"
            :placeholder="`Enter ${overrideScopeIdLabel.toLowerCase()}…`"
            class="mt-1 rounded-sm border border-border-muted bg-surface-2 px-2 py-1 text-[12px] text-ink w-64"
            data-testid="csp-override-scope-id"
          />
        </label>

        <button
          type="button"
          class="rounded-sm border border-border-muted bg-surface-2 px-3 py-1 font-ui text-[12px] uppercase tracking-[0.18em] text-ink-muted hover:bg-surface-3 disabled:opacity-50"
          data-testid="csp-override-load"
          :disabled="!overrideScopeId.trim() || overrideLoading"
          @click="loadOverrideConfig"
        >
          {{ overrideLoading ? 'Loading…' : 'Load' }}
        </button>
      </div>

      <div
        v-if="overrideError"
        class="mt-2 rounded-md border border-signal-danger bg-surface-1 px-3 py-2 font-ui text-[12px] text-signal-danger"
        role="alert"
        data-testid="csp-override-error"
      >
        {{ overrideError }}
      </div>

      <div
        v-if="overrideScopeId.trim() && !overrideLoading"
        class="mt-4 space-y-4"
        data-testid="csp-override-config"
      >
        <div
          v-for="site in sites"
          :key="site"
          class="rounded-md border border-border-muted bg-surface-1 p-4"
          :data-testid="`csp-override-site-${site}`"
        >
          <header class="mb-3 flex items-center justify-between">
            <h3 class="font-ui text-[13px] uppercase tracking-[0.18em] text-ink-dim">
              {{ siteLabel(site) }}
            </h3>
            <span
              v-if="overrideEffectiveSiteConfig(site).strategy"
              class="rounded-sm border border-border-muted px-2 py-0 font-ui text-[10px] uppercase tracking-[0.18em] text-ink-dim"
              :data-testid="`csp-override-effective-${site}`"
            >
              Effective: {{ strategyLabel(overrideEffectiveSiteConfig(site).strategy) }}
              <span
                v-if="overrideAttributionFor(site, 'strategy')"
                class="ml-1 text-accent"
                :data-testid="`csp-override-attribution-${site}`"
              >
                [{{ overrideAttributionFor(site, 'strategy') }}]
              </span>
            </span>
          </header>

          <div class="grid grid-cols-2 gap-3">
            <label class="flex items-center gap-2 font-ui text-[12px] text-ink-muted col-span-2">
              <input
                type="checkbox"
                :checked="overrideSiteConfig(site).enabled"
                class="h-4 w-4 accent-accent"
                :data-testid="`csp-override-${site}-enabled`"
                @change="updateOverrideSiteConfig(site, { enabled: ($event.target as HTMLInputElement).checked })"
              />
              Enabled
            </label>

            <label class="flex flex-col font-ui text-[12px] text-ink-muted">
              <span>Strategy</span>
              <select
                :value="overrideSiteConfig(site).strategy ?? ''"
                class="mt-1 rounded-sm border border-border-muted bg-surface-2 px-2 py-1 text-[12px] text-ink"
                :data-testid="`csp-override-${site}-strategy`"
                @change="updateOverrideSiteConfig(site, {
                  strategy: (($event.target as HTMLSelectElement).value as CompactionStrategy) || undefined,
                })"
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
              <span>Threshold (0.0–1.0)</span>
              <input
                type="number"
                step="0.05"
                min="0"
                max="1"
                :value="overrideSiteConfig(site).preCallThreshold ?? ''"
                class="mt-1 rounded-sm border border-border-muted bg-surface-2 px-2 py-1 text-[12px] text-ink w-24"
                :data-testid="`csp-override-${site}-threshold`"
                @input="updateOverrideSiteConfig(site, {
                  preCallThreshold: Number.parseFloat(($event.target as HTMLInputElement).value) || undefined,
                })"
              />
            </label>
          </div>
        </div>

        <div class="flex items-center gap-3">
          <button
            type="button"
            class="rounded-sm border border-accent bg-surface-2 px-4 py-1.5 font-ui text-[12px] uppercase tracking-[0.18em] text-accent hover:bg-accent-glow disabled:opacity-50"
            data-testid="csp-override-save"
            :disabled="overrideSaving || !overrideScopeId.trim()"
            @click="saveOverrideConfig"
          >
            {{ overrideSaving ? 'Saving…' : `Save ${overrideScope} override` }}
          </button>
          <span
            v-if="overrideSaveSuccess"
            class="font-ui text-[11px] text-signal-success"
            data-testid="csp-override-save-success"
          >
            Saved.
          </span>
        </div>
      </div>
    </section>
  </div>
</template>
