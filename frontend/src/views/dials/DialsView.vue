<script setup lang="ts">
/**
 * DialsView — cascading-config knob editor (Bundle E WP17).
 *
 * Three columns:
 *   1. Scope tabs (global / project / session / graph / run).
 *   2. The selected layer's overrides — sliders + numeric inputs.
 *   3. Effective values + per-field "from" attribution chip so the
 *      user can see, at a glance, which layer contributed each
 *      effective value (NFR-014: cascading config never silently
 *      wins).
 *
 * The view is intentionally form-driven; persistence is whole-layer
 * Save (no per-field auto-commit) so a half-edited layer can't
 * silently impact a running graph.
 */
import { computed, onMounted, ref, watch } from 'vue';
import { useHarnessClient } from '@/lib/useHarnessAPI';
import type {
  DialConfig,
  DialEffectiveDials,
  DialScope,
  DialScopeKey,
} from '@/lib/types';

const client = useHarnessClient();

const scopes: readonly { id: DialScope; label: string; needsID: boolean }[] = [
  { id: 'global', label: 'Global', needsID: false },
  { id: 'project', label: 'Project', needsID: true },
  { id: 'session', label: 'Session', needsID: true },
  { id: 'graph', label: 'Graph', needsID: true },
  { id: 'run', label: 'Run', needsID: true },
];

const activeScope = ref<DialScope>('global');
const scopeID = ref<string>('');
const config = ref<DialConfig>({});
const effective = ref<DialEffectiveDials | null>(null);
const loading = ref(false);
const saving = ref(false);
const error = ref<string | null>(null);
const lastSavedAt = ref<string | null>(null);

const activeKey = computed<DialScopeKey>(() => ({
  scope: activeScope.value,
  id: scopeID.value,
}));

const needsID = computed(
  () => scopes.find((s) => s.id === activeScope.value)?.needsID ?? false,
);

async function load() {
  loading.value = true;
  error.value = null;
  try {
    const [cfg, eff] = await Promise.all([
      client.dials.get(activeKey.value),
      client.dials.getEffective(
        activeScope.value === 'project' ? scopeID.value : '',
        activeScope.value === 'session' ? scopeID.value : '',
        activeScope.value === 'graph' ? scopeID.value : '',
        activeScope.value === 'run' ? scopeID.value : '',
      ),
    ]);
    config.value = cfg ?? {};
    effective.value = eff;
  } catch (err) {
    error.value = err instanceof Error ? err.message : String(err);
  } finally {
    loading.value = false;
  }
}

async function save() {
  saving.value = true;
  error.value = null;
  try {
    await client.dials.set(activeKey.value, config.value);
    lastSavedAt.value = new Date().toLocaleTimeString();
    // Re-pull effective so attribution reflects the save.
    const eff = await client.dials.getEffective(
      activeScope.value === 'project' ? scopeID.value : '',
      activeScope.value === 'session' ? scopeID.value : '',
      activeScope.value === 'graph' ? scopeID.value : '',
      activeScope.value === 'run' ? scopeID.value : '',
    );
    effective.value = eff;
  } catch (err) {
    error.value = err instanceof Error ? err.message : String(err);
  } finally {
    saving.value = false;
  }
}

function setScope(s: DialScope) {
  if (activeScope.value === s) return;
  activeScope.value = s;
  scopeID.value = '';
  void load();
}

function toggleField(field: keyof DialConfig, on: boolean) {
  const setKey = `${field as string}Set` as keyof DialConfig;
  // @ts-expect-error sparse boolean siblings
  config.value[setKey] = on;
}

watch(scopeID, () => {
  if (!needsID.value || scopeID.value !== '') {
    void load();
  }
});

onMounted(() => {
  void load();
});

defineExpose({ load, save });
</script>

<template>
  <div data-testid="dials-view">
    <div class="px-6 py-4 max-w-6xl">
      <header class="mb-4">
        <h2 class="font-ui text-[16px] uppercase tracking-[0.18em] text-ink">
          Dials
        </h2>
        <p class="mt-1 font-ui text-sm text-ink-muted">
          Per-run, per-session, per-project, and global tuning knobs
          for the agent kernel. Each effective value shows the
          contributing layer so you always know why the kernel sees
          what it does.
        </p>
      </header>

      <div class="mb-4 flex flex-wrap gap-2" data-testid="dials-scope-tabs">
        <button
          v-for="s in scopes"
          :key="s.id"
          type="button"
          :data-testid="`dials-scope-${s.id}`"
          class="px-3 py-1 rounded-sm border text-[11px] uppercase tracking-[0.18em] font-ui"
          :class="
            activeScope === s.id
              ? 'border-accent text-accent bg-surface-2'
              : 'border-border-muted text-ink-dim hover:bg-surface-2'
          "
          @click="setScope(s.id)"
        >
          {{ s.label }}
        </button>
      </div>

      <div
        v-if="needsID"
        class="mb-3 flex items-center gap-2 font-ui text-[12px]"
      >
        <label for="dials-scope-id" class="text-ink-dim uppercase tracking-[0.18em]">
          {{ activeScope }} id
        </label>
        <input
          id="dials-scope-id"
          v-model="scopeID"
          type="text"
          class="px-2 py-1 rounded-sm border border-border-muted bg-surface-1 font-mono text-[12px] text-ink"
          placeholder="enter id…"
          data-testid="dials-scope-id"
        />
      </div>

      <div
        v-if="error"
        class="mb-3 rounded-md border border-signal-danger bg-surface-1 px-3 py-2 font-ui text-[12px] text-signal-danger"
        role="alert"
      >
        {{ error }}
      </div>

      <div v-if="loading" class="font-ui text-[12px] text-ink-muted">
        Loading…
      </div>

      <div
        v-else
        class="grid grid-cols-1 md:grid-cols-2 gap-4"
        data-testid="dials-grid"
      >
        <!-- Layer overrides -->
        <section class="rounded-md border border-border-muted bg-surface-1 p-4">
          <h3 class="font-ui text-[11px] uppercase tracking-[0.18em] text-ink-subtle">
            Layer overrides ({{ activeScope }})
          </h3>
          <p class="mt-1 font-ui text-[11px] text-ink-muted">
            Toggle a row to set an explicit override; untoggled rows
            fall back to the cascade.
          </p>

          <div class="mt-4 grid grid-cols-1 gap-3 font-ui text-[12px]">
            <div class="grid grid-cols-[auto_1fr_auto] items-center gap-2">
              <input
                id="d-tokens"
                type="checkbox"
                :checked="config.maxTokensPerRunSet"
                data-testid="dial-toggle-maxTokensPerRun"
                @change="toggleField('maxTokensPerRun', ($event.target as HTMLInputElement).checked)"
              />
              <label for="d-tokens" class="text-ink">Max tokens / run</label>
              <input
                v-model.number="config.maxTokensPerRun"
                type="number"
                min="0"
                step="1000"
                class="w-32 px-2 py-0.5 rounded-sm border border-border-muted bg-surface-1 font-mono text-[12px] text-ink"
                :disabled="!config.maxTokensPerRunSet"
                data-testid="dial-input-maxTokensPerRun"
              />
            </div>

            <div class="grid grid-cols-[auto_1fr_auto] items-center gap-2">
              <input
                id="d-cost"
                type="checkbox"
                :checked="config.maxCostUSDSet"
                data-testid="dial-toggle-maxCostUSD"
                @change="toggleField('maxCostUSD', ($event.target as HTMLInputElement).checked)"
              />
              <label for="d-cost" class="text-ink">Max cost / run (USD)</label>
              <input
                v-model.number="config.maxCostUSD"
                type="number"
                min="0"
                step="0.5"
                class="w-32 px-2 py-0.5 rounded-sm border border-border-muted bg-surface-1 font-mono text-[12px] text-ink"
                :disabled="!config.maxCostUSDSet"
                data-testid="dial-input-maxCostUSD"
              />
            </div>

            <div class="grid grid-cols-[auto_1fr_auto] items-center gap-2">
              <input
                id="d-llm"
                type="checkbox"
                :checked="config.maxLLMCallsSet"
                data-testid="dial-toggle-maxLLMCalls"
                @change="toggleField('maxLLMCalls', ($event.target as HTMLInputElement).checked)"
              />
              <label for="d-llm" class="text-ink">Max LLM calls / run</label>
              <input
                v-model.number="config.maxLLMCalls"
                type="number"
                min="0"
                step="1"
                class="w-32 px-2 py-0.5 rounded-sm border border-border-muted bg-surface-1 font-mono text-[12px] text-ink"
                :disabled="!config.maxLLMCallsSet"
                data-testid="dial-input-maxLLMCalls"
              />
            </div>

            <div class="grid grid-cols-[auto_1fr_auto] items-center gap-2">
              <input
                id="d-tools"
                type="checkbox"
                :checked="config.maxToolCallsSet"
                data-testid="dial-toggle-maxToolCalls"
                @change="toggleField('maxToolCalls', ($event.target as HTMLInputElement).checked)"
              />
              <label for="d-tools" class="text-ink">Max tool calls / run</label>
              <input
                v-model.number="config.maxToolCalls"
                type="number"
                min="0"
                step="1"
                class="w-32 px-2 py-0.5 rounded-sm border border-border-muted bg-surface-1 font-mono text-[12px] text-ink"
                :disabled="!config.maxToolCallsSet"
                data-testid="dial-input-maxToolCalls"
              />
            </div>

            <div class="grid grid-cols-[auto_1fr_auto] items-center gap-2">
              <input
                id="d-clock"
                type="checkbox"
                :checked="config.maxWallclockSet"
                data-testid="dial-toggle-maxWallclock"
                @change="toggleField('maxWallclockSeconds', ($event.target as HTMLInputElement).checked)"
              />
              <label for="d-clock" class="text-ink">Wallclock cap (seconds)</label>
              <input
                v-model.number="config.maxWallclockSeconds"
                type="number"
                min="0"
                step="30"
                class="w-32 px-2 py-0.5 rounded-sm border border-border-muted bg-surface-1 font-mono text-[12px] text-ink"
                :disabled="!config.maxWallclockSet"
                data-testid="dial-input-maxWallclockSeconds"
              />
            </div>
          </div>

          <div class="mt-4 flex items-center gap-2">
            <button
              type="button"
              class="px-3 py-1 rounded-sm border border-accent font-ui text-[11px] uppercase tracking-[0.18em] text-accent hover:bg-surface-2 disabled:opacity-50"
              :disabled="saving || (needsID && !scopeID)"
              data-testid="dials-save"
              @click="save"
            >
              {{ saving ? 'Saving…' : 'Save layer' }}
            </button>
            <span
              v-if="lastSavedAt"
              class="font-ui text-[11px] text-ink-subtle"
            >
              saved at {{ lastSavedAt }}
            </span>
          </div>
        </section>

        <!-- Effective values -->
        <section class="rounded-md border border-border-muted bg-surface-1 p-4">
          <h3 class="font-ui text-[11px] uppercase tracking-[0.18em] text-ink-subtle">
            Effective dials
          </h3>
          <p class="mt-1 font-ui text-[11px] text-ink-muted">
            Resolved cascade (weakest → strongest: global → project →
            session → graph → run). The chip shows the contributing
            layer.
          </p>
          <div
            v-if="effective"
            class="mt-4 grid grid-cols-1 gap-2 font-ui text-[12px]"
            data-testid="dials-effective-grid"
          >
            <div class="flex items-center justify-between gap-2">
              <span class="text-ink-dim">Max tokens / run</span>
              <span class="font-mono text-ink">{{ effective.maxTokensPerRun.value }}</span>
              <span
                class="px-1.5 py-0.5 rounded-sm border border-border-muted text-[10px] uppercase tracking-[0.18em] text-ink-dim"
                data-testid="dials-effective-maxTokensPerRun-from"
              >
                {{ effective.maxTokensPerRun.from }}
              </span>
            </div>
            <div class="flex items-center justify-between gap-2">
              <span class="text-ink-dim">Max cost / run (USD)</span>
              <span class="font-mono text-ink">{{ effective.maxCostUSD.value }}</span>
              <span
                class="px-1.5 py-0.5 rounded-sm border border-border-muted text-[10px] uppercase tracking-[0.18em] text-ink-dim"
                data-testid="dials-effective-maxCostUSD-from"
              >
                {{ effective.maxCostUSD.from }}
              </span>
            </div>
            <div class="flex items-center justify-between gap-2">
              <span class="text-ink-dim">Max LLM calls / run</span>
              <span class="font-mono text-ink">{{ effective.maxLLMCalls.value }}</span>
              <span class="px-1.5 py-0.5 rounded-sm border border-border-muted text-[10px] uppercase tracking-[0.18em] text-ink-dim">
                {{ effective.maxLLMCalls.from }}
              </span>
            </div>
            <div class="flex items-center justify-between gap-2">
              <span class="text-ink-dim">Max tool calls / run</span>
              <span class="font-mono text-ink">{{ effective.maxToolCalls.value }}</span>
              <span class="px-1.5 py-0.5 rounded-sm border border-border-muted text-[10px] uppercase tracking-[0.18em] text-ink-dim">
                {{ effective.maxToolCalls.from }}
              </span>
            </div>
            <div class="flex items-center justify-between gap-2">
              <span class="text-ink-dim">Wallclock cap (s)</span>
              <span class="font-mono text-ink">{{ effective.maxWallclockSeconds.value }}</span>
              <span class="px-1.5 py-0.5 rounded-sm border border-border-muted text-[10px] uppercase tracking-[0.18em] text-ink-dim">
                {{ effective.maxWallclockSeconds.from }}
              </span>
            </div>
            <div class="flex items-center justify-between gap-2">
              <span class="text-ink-dim">Plan verbosity</span>
              <span class="font-mono text-ink">{{ effective.planVerbosity.value }}</span>
              <span class="px-1.5 py-0.5 rounded-sm border border-border-muted text-[10px] uppercase tracking-[0.18em] text-ink-dim">
                {{ effective.planVerbosity.from }}
              </span>
            </div>
            <div class="flex items-center justify-between gap-2">
              <span class="text-ink-dim">Memory hooks enabled</span>
              <span class="font-mono text-ink">
                {{ effective.memoryHooksEnabled.value ? 'on' : 'off' }}
              </span>
              <span class="px-1.5 py-0.5 rounded-sm border border-border-muted text-[10px] uppercase tracking-[0.18em] text-ink-dim">
                {{ effective.memoryHooksEnabled.from }}
              </span>
            </div>
            <div class="flex items-center justify-between gap-2">
              <span class="text-ink-dim">Memory prune interval (s)</span>
              <span class="font-mono text-ink">{{ effective.memoryPruneIntervalSeconds.value }}</span>
              <span class="px-1.5 py-0.5 rounded-sm border border-border-muted text-[10px] uppercase tracking-[0.18em] text-ink-dim">
                {{ effective.memoryPruneIntervalSeconds.from }}
              </span>
            </div>
          </div>
        </section>
      </div>
    </div>
  </div>
</template>
