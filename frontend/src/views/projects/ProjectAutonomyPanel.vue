<script setup lang="ts">
/**
 * ProjectAutonomyPanel — per-project autonomy override panel
 * (autonomy-dial-01KR3M2A WP07).
 *
 * Mirrors the global AutonomyPanel but adds an "Inherit from global"
 * option for the tier selector and a side-by-side annotation showing
 * what the global layer currently sets so the user can see what their
 * override displaces.
 *
 * Beta scope: lean on the global panel's knob editor — same shape,
 * extended with an "inherit" indicator per row.
 */

import { computed, onMounted, ref, watch } from 'vue';

import { useHarnessClient } from '@/lib/harnessClientContext';
import {
  AUTONOMY_KNOB_LABELS,
  AUTONOMY_KNOB_ORDER,
  AUTONOMY_TIER_DESCRIPTIONS,
  AUTONOMY_TIER_LABELS,
  emptyAutonomyLayer,
  type AutonomyKnob,
  type AutonomyLayer,
  type AutonomyTier,
} from '@/lib/types';

const props = defineProps<{
  projectId: string;
  /** Skip on-mount fetch (vitest). */
  skipFetch?: boolean;
  /** Override layers for tests. */
  globalOverride?: AutonomyLayer | null;
  projectOverride?: AutonomyLayer | null;
}>();

const emit = defineEmits<{
  (e: 'change', layer: AutonomyLayer): void;
}>();

const client = useHarnessClient();

const TIERS: readonly AutonomyTier[] = [
  'strict',
  'cautious',
  'default',
  'bold',
  'autonomous',
];

const projectLayer = ref<AutonomyLayer>(emptyAutonomyLayer());
const globalLayer = ref<AutonomyLayer>(emptyAutonomyLayer());
const advancedOpen = ref(false);
const saveError = ref<string | null>(null);

const selectedTier = computed<AutonomyTier | null>(() => projectLayer.value.level);
const overrideKeys = computed<AutonomyKnob[]>(
  () =>
    AUTONOMY_KNOB_ORDER.filter(
      (k) => projectLayer.value.overrides[k] !== undefined,
    ) as AutonomyKnob[],
);
const isCustom = computed(() => overrideKeys.value.length > 0);

async function refresh() {
  if (props.skipFetch) return;
  try {
    const [proj, glob] = await Promise.all([
      client.projects.getAutonomy(props.projectId),
      client.settings.getAutonomy(),
    ]);
    projectLayer.value = normalise(proj);
    globalLayer.value = normalise(glob);
  } catch (err) {
    saveError.value = err instanceof Error ? err.message : String(err);
  }
}

function normalise(l: AutonomyLayer | null | undefined): AutonomyLayer {
  if (!l) return emptyAutonomyLayer();
  return { level: l.level ?? null, overrides: { ...(l.overrides ?? {}) } };
}

async function persist() {
  try {
    saveError.value = null;
    await client.projects.setAutonomy(props.projectId, projectLayer.value);
    emit('change', projectLayer.value);
  } catch (err) {
    saveError.value = err instanceof Error ? err.message : String(err);
  }
}

function setTier(t: AutonomyTier | null) {
  projectLayer.value = { ...projectLayer.value, level: t };
  void persist();
}

function setOverride(k: AutonomyKnob, raw: string) {
  const trimmed = raw.trim();
  if (trimmed === '') {
    resetOverride(k);
    return;
  }
  const parsed = parseKnob(k, trimmed);
  if (parsed === undefined) return;
  projectLayer.value = {
    ...projectLayer.value,
    overrides: { ...projectLayer.value.overrides, [k]: parsed },
  };
  void persist();
}

function resetOverride(k: AutonomyKnob) {
  if (projectLayer.value.overrides[k] === undefined) return;
  const next = { ...projectLayer.value.overrides };
  delete next[k];
  projectLayer.value = { ...projectLayer.value, overrides: next };
  void persist();
}

function parseKnob(k: AutonomyKnob, raw: string): unknown | undefined {
  switch (k) {
    case 'maxIterations':
    case 'tokenCeilingPerTurn': {
      const n = Number(raw);
      if (!Number.isFinite(n)) return undefined;
      return Math.max(0, Math.trunc(n));
    }
    case 'autoApproveFamilies': {
      return raw
        .split(',')
        .map((s) => s.trim())
        .filter(Boolean);
    }
    default:
      return raw;
  }
}

function knobDisplay(k: AutonomyKnob): string {
  const v = projectLayer.value.overrides[k];
  if (v === undefined) return '';
  if (Array.isArray(v)) return v.join(', ');
  return String(v);
}

function inheritedFromGlobal(k: AutonomyKnob): string {
  const v = globalLayer.value.overrides[k];
  if (v !== undefined) {
    if (Array.isArray(v)) return v.join(', ');
    return String(v);
  }
  if (globalLayer.value.level) {
    return `(tier preset: ${globalLayer.value.level})`;
  }
  return '(tier-default)';
}

watch(
  () => props.globalOverride,
  (v) => {
    if (v !== undefined && v !== null) globalLayer.value = normalise(v);
  },
  { immediate: true },
);
watch(
  () => props.projectOverride,
  (v) => {
    if (v !== undefined && v !== null) projectLayer.value = normalise(v);
  },
  { immediate: true },
);
watch(() => props.projectId, () => void refresh());

onMounted(refresh);

defineExpose({ refresh });
</script>

<template>
  <section data-testid="project-autonomy-panel">
    <div class="flex items-center justify-between">
      <h2 class="font-ui text-[11px] uppercase tracking-[0.18em] text-ink-subtle">
        Project autonomy
      </h2>
      <span
        v-if="isCustom"
        class="rounded-sm border border-border-muted bg-surface-2 px-2 py-0.5 font-ui text-[10px] uppercase tracking-[0.14em] text-ink-muted"
        data-testid="project-autonomy-custom"
      >
        Custom
      </span>
    </div>
    <p class="mt-1 font-ui text-[11px] text-ink-muted">
      Per-project tier override. Folds atop the global layer. Use
      "Inherit" to fall through to global. Sessions in this project may
      override further.
    </p>

    <div
      class="mt-3 inline-flex flex-wrap rounded-sm border border-border"
      role="radiogroup"
      aria-label="Project autonomy tier"
      data-testid="project-autonomy-tier-selector"
    >
      <button
        type="button"
        role="radio"
        :aria-checked="selectedTier === null"
        class="px-3 py-1.5 font-ui text-[12px] border-r border-border last:border-r-0 transition-colors"
        :class="
          selectedTier === null
            ? 'bg-surface-3 text-ink'
            : 'bg-surface-1 text-ink-muted hover:text-ink'
        "
        data-testid="project-autonomy-tier-inherit"
        @click="setTier(null)"
      >
        Inherit
      </button>
      <button
        v-for="t in TIERS"
        :key="t"
        type="button"
        role="radio"
        :aria-checked="selectedTier === t"
        class="px-3 py-1.5 font-ui text-[12px] border-r border-border last:border-r-0 transition-colors"
        :class="
          selectedTier === t
            ? 'bg-surface-3 text-ink'
            : 'bg-surface-1 text-ink-muted hover:text-ink'
        "
        :data-testid="`project-autonomy-tier-${t}`"
        @click="setTier(t)"
      >
        {{ AUTONOMY_TIER_LABELS[t] }}
      </button>
    </div>

    <p
      v-if="selectedTier !== null"
      class="mt-2 font-ui text-[12px] text-ink"
      data-testid="project-autonomy-tier-description"
    >
      {{ AUTONOMY_TIER_DESCRIPTIONS[selectedTier] }}
    </p>
    <p
      v-else
      class="mt-2 font-ui text-[12px] text-ink-muted"
      data-testid="project-autonomy-inherit-hint"
    >
      Inheriting from global —
      {{
        globalLayer.level
          ? `tier preset: ${AUTONOMY_TIER_LABELS[globalLayer.level]}`
          : 'tier-default'
      }}
    </p>

    <div class="mt-4">
      <button
        type="button"
        class="font-ui text-[11px] uppercase tracking-[0.16em] text-ink-muted hover:text-ink"
        data-testid="project-autonomy-advanced-toggle"
        :aria-expanded="advancedOpen"
        @click="advancedOpen = !advancedOpen"
      >
        {{ advancedOpen ? '▾' : '▸' }} Advanced — per-knob overrides
      </button>
    </div>

    <div
      v-if="advancedOpen"
      class="mt-3 grid gap-3 sm:grid-cols-2"
      data-testid="project-autonomy-advanced-grid"
    >
      <div
        v-for="k in AUTONOMY_KNOB_ORDER"
        :key="k"
        class="rounded-sm border border-border-muted bg-surface-1 px-3 py-2"
      >
        <label class="block font-ui text-[10px] uppercase tracking-[0.16em] text-ink-subtle">
          {{ AUTONOMY_KNOB_LABELS[k] }}
        </label>
        <div class="mt-1 flex items-center gap-2">
          <input
            type="text"
            class="flex-1 rounded-sm border border-border bg-surface-2 px-2 py-1 font-mono text-[12px] text-ink"
            :value="knobDisplay(k)"
            :placeholder="
              projectLayer.overrides[k] === undefined ? '(inherit)' : ''
            "
            :data-testid="`project-autonomy-knob-${k}`"
            @change="(e) => setOverride(k, (e.target as HTMLInputElement).value)"
          />
          <button
            v-if="projectLayer.overrides[k] !== undefined"
            type="button"
            class="font-ui text-[10px] uppercase tracking-[0.14em] text-ink-subtle hover:text-ink"
            :data-testid="`project-autonomy-knob-reset-${k}`"
            @click="resetOverride(k)"
          >
            Reset
          </button>
        </div>
        <p class="mt-1 font-ui text-[10px] text-ink-subtle">
          global: <span class="font-mono">{{ inheritedFromGlobal(k) }}</span>
        </p>
      </div>
    </div>

    <div
      v-if="saveError"
      class="mt-2 rounded-sm border border-signal-danger bg-surface-1 px-3 py-2 font-ui text-[12px] text-signal-danger"
      role="alert"
      data-testid="project-autonomy-error"
    >
      {{ saveError }}
    </div>
  </section>
</template>
