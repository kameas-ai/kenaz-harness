<script setup lang="ts">
/**
 * SettingsView — General app preferences.
 *
 * Grouped sections (top → bottom):
 *   - Appearance & startup — theme, route restoration
 *   - Memory — compaction tier + model + archive/recent windows
 *   - About — collapsible disclosure with build / commit / platform
 *
 * Permission mode + Cache dangerous ops moved to /permissions
 * (the Permissions sub-tab), Global context moved to /contexts.
 *
 * Persistence routes through SettingsStore (Settings_Get + Settings_Set);
 * writes are debounced 250ms via lib/settings.ts.
 */
import { computed, onMounted, ref } from 'vue';
import CanvasHead from '@/shell/CanvasHead.vue';
import SettingsTabs from '@/views/settings/SettingsTabs.vue';
import RadioStrip from '@/components/settings/RadioStrip.vue';
import { useHarnessClient } from '@/lib/useHarnessAPI';
import { debouncedSave } from '@/lib/settings';
import type {
  AppInfo,
  CompactionAggressiveness,
  CompactionTierExplain,
  Provider,
  Settings,
  Theme,
} from '@/lib/types';

const client = useHarnessClient();

const settings = ref<Settings>({
  schemaVersion: 1,
  lastRoute: '/sessions',
  theme: 'system',
  accent: 'default',
  windowSize: { width: 1280, height: 800 },
  memoryEnabled: false,
  confirmEachDisabled: false,
});
const appInfo = ref<AppInfo | null>(null);
const restoreOnLaunch = ref(true);

/* ── Compaction ────────────────────────────────────────────────── */

const COMPACTION_TIERS: ReadonlyArray<CompactionAggressiveness> = [
  'off',
  'conservative',
  'balanced',
  'aggressive',
  'maximal',
];

const compactionTier = ref<CompactionAggressiveness>('balanced');
const compactionProviderId = ref('');
const compactionModelId = ref('');
const compactionArchiveDays = ref<number>(90);
const compactionRecentWindow = ref<number>(4);
// Default-open the first time a user lands on Settings so the tier
// names ("balanced" / "aggressive" / etc.) aren't opaque. The
// LocalStorage flag flips after the user explicitly closes it once.
const COMPACTION_EXPLAIN_DISMISSED_KEY = 'kaneaz.settings.compactionExplainDismissed';
const compactionExplainOpen = ref<boolean>(
  typeof window !== 'undefined'
    ? window.localStorage.getItem(COMPACTION_EXPLAIN_DISMISSED_KEY) !== '1'
    : false,
);
function toggleCompactionExplain() {
  compactionExplainOpen.value = !compactionExplainOpen.value;
  if (typeof window !== 'undefined' && !compactionExplainOpen.value) {
    window.localStorage.setItem(COMPACTION_EXPLAIN_DISMISSED_KEY, '1');
  }
}
const compactionTiers = ref<CompactionTierExplain[]>([]);
const compactionProviders = ref<Provider[]>([]);
const compactionArchiveDaysError = ref<string | null>(null);
const compactionRecentWindowError = ref<string | null>(null);

const selectedTierExplain = computed<CompactionTierExplain | null>(() => {
  return (
    compactionTiers.value.find(
      (row) => row.aggressiveness === compactionTier.value,
    ) ?? null
  );
});

const compactionModelOptions = computed<
  { value: string; label: string; providerId: string; modelId: string }[]
>(() => {
  const out: {
    value: string;
    label: string;
    providerId: string;
    modelId: string;
  }[] = [];
  for (const p of compactionProviders.value) {
    const models = p.models && p.models.length > 0 ? p.models : [p.model];
    for (const m of models) {
      out.push({
        value: `${p.id}::${m}`,
        label: `${p.name} • ${m}`,
        providerId: p.id,
        modelId: m,
      });
    }
  }
  return out;
});

const compactionModelValue = computed<string>(() => {
  if (!compactionProviderId.value || !compactionModelId.value) return '';
  return `${compactionProviderId.value}::${compactionModelId.value}`;
});

const themes: ReadonlyArray<{ value: Theme; label: string; note?: string }> = [
  { value: 'system', label: 'System' },
  { value: 'dark', label: 'Dark', note: 'v1 default' },
  { value: 'light', label: 'Light', note: 'v1.x' },
];

const compactionTierOptions = computed(() =>
  COMPACTION_TIERS.map((t) => ({ value: t, label: t })),
);

/* ── About disclosure ────────────────────────────────────────── */

const aboutOpen = ref(false);

async function refresh() {
  try {
    settings.value = await client.settings.get();
  } catch {
    // Keep defaults on error.
  }
  try {
    appInfo.value = await client.appInfo();
  } catch {
    appInfo.value = null;
  }
  compactionTier.value =
    (settings.value.compactionAggressiveness as CompactionAggressiveness) ||
    'balanced';
  compactionProviderId.value = settings.value.compactionModel?.providerId ?? '';
  compactionModelId.value = settings.value.compactionModel?.modelId ?? '';
  compactionArchiveDays.value = settings.value.compactionArchiveDays || 90;
  compactionRecentWindow.value = settings.value.compactionRecentWindow || 4;
  compactionArchiveDaysError.value = null;
  compactionRecentWindowError.value = null;
  try {
    compactionTiers.value = await client.compaction.getTierExplain();
  } catch {
    compactionTiers.value = [];
  }
  try {
    compactionProviders.value = await client.llm.listProviders();
  } catch {
    compactionProviders.value = [];
  }
}

function setCompactionTier(t: CompactionAggressiveness) {
  compactionTier.value = t;
  persistCompactionFields();
}

function onCompactionModelChange(evt: Event) {
  const v = (evt.target as HTMLSelectElement).value;
  if (!v) {
    compactionProviderId.value = '';
    compactionModelId.value = '';
  } else {
    const opt = compactionModelOptions.value.find((o) => o.value === v);
    if (opt) {
      compactionProviderId.value = opt.providerId;
      compactionModelId.value = opt.modelId;
    }
  }
  persistCompactionFields();
}

function onCompactionArchiveDaysInput(evt: Event) {
  const raw = (evt.target as HTMLInputElement).value;
  const n = Number.parseInt(raw, 10);
  if (Number.isNaN(n) || n < 7 || n > 365) {
    compactionArchiveDaysError.value =
      'Archive days must be between 7 and 365.';
    return;
  }
  compactionArchiveDaysError.value = null;
  compactionArchiveDays.value = n;
  persistCompactionFields();
}

function onCompactionRecentWindowInput(evt: Event) {
  const raw = (evt.target as HTMLInputElement).value;
  const n = Number.parseInt(raw, 10);
  if (Number.isNaN(n) || n < 1) {
    compactionRecentWindowError.value =
      'Recent window must be at least 1.';
    return;
  }
  compactionRecentWindowError.value = null;
  compactionRecentWindow.value = n;
  persistCompactionFields();
}

function persistCompactionFields() {
  if (
    compactionArchiveDaysError.value !== null ||
    compactionRecentWindowError.value !== null
  ) {
    return;
  }
  debouncedSave(client, {
    ...settings.value,
    compactionAggressiveness: compactionTier.value,
    compactionModel:
      compactionProviderId.value && compactionModelId.value
        ? {
            providerId: compactionProviderId.value,
            modelId: compactionModelId.value,
          }
        : undefined,
    compactionArchiveDays: compactionArchiveDays.value,
    compactionRecentWindow: compactionRecentWindow.value,
  });
}

function setTheme(t: Theme) {
  settings.value = { ...settings.value, theme: t };
  void client.settings.saveTheme(t).catch(() => {});
}

function toggleRestore() {
  restoreOnLaunch.value = !restoreOnLaunch.value;
  debouncedSave(client, {
    ...settings.value,
    lastRoute: restoreOnLaunch.value ? settings.value.lastRoute : '/sessions',
  });
}

onMounted(() => {
  void refresh();
});
</script>

<template>
  <div>
    <CanvasHead
      number="06"
      section="SETTINGS"
      title="App preferences"
      subtitle="Theme, startup behaviour, and conversation memory. Permissions live on the Permissions tab; global context on the Contexts page."
    />
    <SettingsTabs />

    <div class="px-6 py-4 grid gap-8 max-w-3xl" data-testid="settings-form">
      <!-- ── Group: Appearance & startup ─────────────────────────── -->
      <div class="grid gap-6">
        <h3 class="font-ui text-[10px] uppercase tracking-[0.22em] text-ink-dim border-b border-border-muted pb-1">
          Appearance &amp; startup
        </h3>

        <section>
          <h2 class="font-ui text-[11px] uppercase tracking-[0.18em] text-ink-subtle">
            Theme
          </h2>
          <div class="mt-2">
            <RadioStrip
              :model-value="settings.theme"
              :options="themes"
              aria-label="Theme"
              testid-prefix="theme"
              @update:model-value="setTheme"
            />
          </div>
        </section>

        <section>
          <h2 class="font-ui text-[11px] uppercase tracking-[0.18em] text-ink-subtle">
            Route restoration
          </h2>
          <label class="mt-2 flex items-center gap-3 font-ui text-[12px] text-ink">
            <input
              type="checkbox"
              class="accent-accent"
              :checked="restoreOnLaunch"
              data-testid="restore-toggle"
              @change="toggleRestore"
            />
            Restore the last visited route on launch
          </label>
          <p class="mt-1 text-[11px] text-ink-muted">
            Last route: <span class="font-mono">{{ settings.lastRoute }}</span>
          </p>
        </section>
      </div>

      <!-- ── Group: Memory ───────────────────────────────────────── -->
      <div class="grid gap-6">
        <h3 class="font-ui text-[10px] uppercase tracking-[0.22em] text-ink-dim border-b border-border-muted pb-1">
          Memory
        </h3>

        <section data-testid="compaction-section">
          <h2 class="font-ui text-[11px] uppercase tracking-[0.18em] text-ink-subtle">
            Compaction
          </h2>
          <p class="mt-1 font-ui text-[11px] text-ink-muted">
            When a session approaches the model's context cap, the harness
            summarises older turns into a single message so the conversation
            can keep going.
          </p>

          <div class="mt-3">
            <RadioStrip
              :model-value="compactionTier"
              :options="compactionTierOptions"
              aria-label="Compaction aggressiveness"
              testid-prefix="compaction-tier"
              @update:model-value="setCompactionTier"
            />
          </div>

          <button
            type="button"
            class="mt-3 font-ui text-[11px] text-accent hover:underline"
            data-testid="compaction-explain-toggle"
            :aria-expanded="compactionExplainOpen"
            @click="toggleCompactionExplain"
          >
            {{ compactionExplainOpen ? 'Hide' : 'What does this mean?' }}
          </button>

          <div
            v-if="compactionExplainOpen"
            class="mt-2 rounded-sm border border-border bg-surface-1 p-3 font-ui text-[12px]"
            data-testid="compaction-explain-disclosure"
          >
            <div v-if="selectedTierExplain" class="space-y-1">
              <div class="font-medium text-ink" data-testid="compaction-explain-label">
                {{ selectedTierExplain.label }}
              </div>
              <p class="text-ink-muted" data-testid="compaction-explain-description">
                {{ selectedTierExplain.description }}
              </p>
              <dl
                class="mt-2 grid gap-1 font-mono text-[11px] text-ink-muted"
                style="grid-template-columns: 14ch 1fr"
                data-testid="compaction-explain-numerics"
              >
                <dt>Trigger %</dt>
                <dd>
                  {{
                    selectedTierExplain.triggerPct > 0
                      ? `${Math.round(selectedTierExplain.triggerPct * 100)}% of cap`
                      : '—'
                  }}
                </dd>
                <dt>Summarize %</dt>
                <dd>
                  {{
                    selectedTierExplain.summarizePct > 0
                      ? `${Math.round(selectedTierExplain.summarizePct * 100)}% of oldest tokens`
                      : '—'
                  }}
                </dd>
                <dt>Mode</dt>
                <dd>{{ selectedTierExplain.mode }}</dd>
              </dl>
            </div>
            <div v-else class="text-ink-muted">
              No explain row available — using locked defaults.
            </div>
          </div>

          <div class="mt-4 grid gap-2" style="grid-template-columns: 14ch 1fr">
            <label
              for="compaction-model"
              class="self-center font-ui text-[12px] text-ink-muted"
            >
              Compaction model
            </label>
            <select
              id="compaction-model"
              class="rounded-sm border border-border bg-surface-1 px-2 py-1 font-ui text-[12px] text-ink"
              data-testid="compaction-model-select"
              :value="compactionModelValue"
              @change="onCompactionModelChange"
            >
              <option value="">Use session's active model (recommended)</option>
              <option
                v-for="opt in compactionModelOptions"
                :key="opt.value"
                :value="opt.value"
              >
                {{ opt.label }}
              </option>
            </select>
          </div>

          <div class="mt-2 grid gap-2" style="grid-template-columns: 14ch 1fr">
            <label
              for="compaction-archive-days"
              class="self-center font-ui text-[12px] text-ink-muted"
            >
              Archive days
            </label>
            <div>
              <input
                id="compaction-archive-days"
                type="number"
                min="7"
                max="365"
                :value="compactionArchiveDays"
                class="w-24 rounded-sm border border-border bg-surface-1 px-2 py-1 font-ui text-[12px] text-ink"
                data-testid="compaction-archive-days-input"
                @input="onCompactionArchiveDaysInput"
              />
              <p
                v-if="compactionArchiveDaysError"
                class="mt-1 font-ui text-[11px] text-signal-danger"
                role="alert"
                data-testid="compaction-archive-days-error"
              >
                {{ compactionArchiveDaysError }}
              </p>
              <p v-else class="mt-1 font-ui text-[11px] text-ink-muted">
                Soft-archived originals are deleted after this many days. Default 90.
              </p>
            </div>
          </div>

          <div class="mt-2 grid gap-2" style="grid-template-columns: 14ch 1fr">
            <label
              for="compaction-recent-window"
              class="self-center font-ui text-[12px] text-ink-muted"
            >
              Recent window
            </label>
            <div>
              <input
                id="compaction-recent-window"
                type="number"
                min="1"
                :value="compactionRecentWindow"
                class="w-24 rounded-sm border border-border bg-surface-1 px-2 py-1 font-ui text-[12px] text-ink"
                data-testid="compaction-recent-window-input"
                @input="onCompactionRecentWindowInput"
              />
              <p
                v-if="compactionRecentWindowError"
                class="mt-1 font-ui text-[11px] text-signal-danger"
                role="alert"
                data-testid="compaction-recent-window-error"
              >
                {{ compactionRecentWindowError }}
              </p>
              <p v-else class="mt-1 font-ui text-[11px] text-ink-muted">
                Most-recent user-assistant pairs that compaction never touches. Default 4.
              </p>
            </div>
          </div>
        </section>
      </div>

      <!-- ── About (collapsed disclosure) ────────────────────────── -->
      <section data-testid="about-section">
        <button
          type="button"
          class="font-ui text-[11px] text-ink-muted hover:text-ink uppercase tracking-[0.18em]"
          :aria-expanded="aboutOpen"
          data-testid="about-toggle"
          @click="aboutOpen = !aboutOpen"
        >
          {{ aboutOpen ? '▼' : '▶' }} About this build
        </button>
        <div
          v-if="aboutOpen && appInfo"
          class="mt-2 rounded-sm border border-border bg-surface-1 p-3"
          data-testid="about-disclosure"
        >
          <dl class="grid gap-2 font-ui text-[12px]" style="grid-template-columns: 12ch 1fr">
            <dt class="text-ink-muted">Build</dt>
            <dd class="font-mono text-ink">{{ appInfo.build }}</dd>
            <dt class="text-ink-muted">Commit</dt>
            <dd class="font-mono text-ink">{{ appInfo.commit }}</dd>
            <dt class="text-ink-muted">Built</dt>
            <dd class="font-mono text-ink">{{ appInfo.buildTime || '—' }}</dd>
            <dt class="text-ink-muted">Go</dt>
            <dd class="font-mono text-ink">{{ appInfo.goVersion || '—' }}</dd>
            <dt class="text-ink-muted">Platform</dt>
            <dd class="font-mono text-ink">{{ appInfo.platform || '—' }}</dd>
          </dl>
        </div>
      </section>
    </div>
  </div>
</template>
