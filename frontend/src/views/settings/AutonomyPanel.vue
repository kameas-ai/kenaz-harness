<script setup lang="ts">
/**
 * AutonomyPanel — global autonomy dial (autonomy-dial-01KR3M2A WP06).
 *
 * Three-tier preset selector (Strict / Default / Autonomous) plus a
 * "more presets" expansion (Cautious / Bold) and an Advanced section
 * that lists the 7 underlying knobs with override + reset affordances.
 *
 * Beta scope per mission brief: "exists, may not be perfect" — we
 * prioritise persistence + round-trip fidelity over a polished knob
 * editor. Knob overrides accept JSON-coercible scalars; the Reset
 * link removes the override and the resolver folds back to the tier
 * preset.
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
  /** Override the data fetcher for tests. */
  layerOverride?: AutonomyLayer | null;
  /** Skip the on-mount fetch (useful in vitest contexts). */
  skipFetch?: boolean;
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

// PRIMARY_TIERS surfaces the three-stop selector the design asked for;
// the disclosure expands to all five.
const PRIMARY_TIERS: readonly AutonomyTier[] = ['strict', 'default', 'autonomous'];

const layer = ref<AutonomyLayer>(emptyAutonomyLayer());
const showAllTiers = ref(false);
const advancedOpen = ref(false);
const loading = ref(false);
const saveError = ref<string | null>(null);

// ── Legacy fallback + permission-posture dials ────────────────────────
//
// These two are NOT AutonomyLayer overrides — they persist through the
// plain Settings store (Settings_{Get,Set}MaxAgentTurns /
// Settings_{Get,Set}FSRequestAccessEnabled), not getAutonomy/setAutonomy.
// Surfaced here per owner ruling (trust-surfaces-that-fire-01PMZ202
// WP25 C2V-04): MaxAgentTurns is the legacy numeric fallback the chat
// graph's LoopNode iteration cap falls back to only when the "Max
// iterations" knob above has no explicit override
// (core/rpc/api.go resolveAutonomyKnobsWithSettingsFallback); the more
// specific "Max iterations" override above always wins when set.
// FSRequestAccessEnabled gates whether the model's tool catalog
// includes kenaz__request_filesystem_access at all
// (core/rpc/builtins_wiring.go builtinEnabledPredicate).
const maxAgentTurns = ref(0);
const maxAgentTurnsBusy = ref(false);
const maxAgentTurnsError = ref<string | null>(null);

const fsRequestAccessEnabled = ref(true);
const fsRequestAccessBusy = ref(false);
const fsRequestAccessError = ref<string | null>(null);

async function refreshLegacyControls() {
  if (props.skipFetch) return;
  try {
    maxAgentTurns.value = await client.settings.getMaxAgentTurns();
  } catch (err) {
    maxAgentTurnsError.value = err instanceof Error ? err.message : String(err);
  }
  try {
    fsRequestAccessEnabled.value = await client.settings.getFSRequestAccessEnabled();
  } catch (err) {
    fsRequestAccessError.value = err instanceof Error ? err.message : String(err);
  }
}

async function persistMaxAgentTurns(raw: string) {
  const n = Number(raw);
  const next = Number.isFinite(n) ? Math.max(0, Math.trunc(n)) : 0;
  if (maxAgentTurnsBusy.value) return;
  maxAgentTurnsBusy.value = true;
  maxAgentTurnsError.value = null;
  const previous = maxAgentTurns.value;
  maxAgentTurns.value = next;
  try {
    await client.settings.setMaxAgentTurns(next);
  } catch (err) {
    maxAgentTurns.value = previous;
    maxAgentTurnsError.value = err instanceof Error ? err.message : String(err);
  } finally {
    maxAgentTurnsBusy.value = false;
  }
}

async function toggleFSRequestAccess(event: Event) {
  if (fsRequestAccessBusy.value) return;
  const next = (event.target as HTMLInputElement).checked;
  fsRequestAccessBusy.value = true;
  fsRequestAccessError.value = null;
  const previous = fsRequestAccessEnabled.value;
  fsRequestAccessEnabled.value = next;
  try {
    await client.settings.setFSRequestAccessEnabled(next);
  } catch (err) {
    fsRequestAccessEnabled.value = previous;
    fsRequestAccessError.value = err instanceof Error ? err.message : String(err);
  } finally {
    fsRequestAccessBusy.value = false;
  }
}

const selectedTier = computed<AutonomyTier | null>(() => layer.value.level);
const overrideKeys = computed<AutonomyKnob[]>(
  () =>
    AUTONOMY_KNOB_ORDER.filter(
      (k) => layer.value.overrides[k] !== undefined,
    ) as AutonomyKnob[],
);
const isCustom = computed(() => overrideKeys.value.length > 0);

async function refresh() {
  if (props.skipFetch) return;
  loading.value = true;
  try {
    const got = await client.settings.getAutonomy();
    layer.value = normaliseLayer(got);
  } catch (err) {
    saveError.value = err instanceof Error ? err.message : String(err);
  } finally {
    loading.value = false;
  }
}

function normaliseLayer(l: AutonomyLayer | null | undefined): AutonomyLayer {
  if (!l) return emptyAutonomyLayer();
  return {
    level: l.level ?? null,
    overrides: { ...(l.overrides ?? {}) },
  };
}

async function persist() {
  try {
    saveError.value = null;
    await client.settings.setAutonomy(layer.value);
    emit('change', layer.value);
  } catch (err) {
    saveError.value = err instanceof Error ? err.message : String(err);
  }
}

function setTier(t: AutonomyTier) {
  if (isCustom.value && layer.value.level !== t) {
    const keep = window.confirm(
      'Switching tiers — keep your knob overrides? Click OK to keep, Cancel to reset overrides to the new tier presets.',
    );
    if (!keep) {
      layer.value = { level: t, overrides: {} };
      void persist();
      return;
    }
  }
  layer.value = { ...layer.value, level: t };
  void persist();
}

function clearTier() {
  layer.value = { ...layer.value, level: null };
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
  layer.value = {
    ...layer.value,
    overrides: { ...layer.value.overrides, [k]: parsed },
  };
  void persist();
}

function resetOverride(k: AutonomyKnob) {
  if (layer.value.overrides[k] === undefined) return;
  const next = { ...layer.value.overrides };
  delete next[k];
  layer.value = { ...layer.value, overrides: next };
  void persist();
}

/**
 * parseKnob coerces a user-typed override into the wire-correct shape.
 * Returns undefined when the value can't be coerced (the input keeps
 * its previous value silently — beta scope, no field-level validation).
 */
function parseKnob(k: AutonomyKnob, raw: string): unknown | undefined {
  switch (k) {
    case 'maxIterations':
    case 'tokenCeilingPerTurn': {
      const n = Number(raw);
      if (!Number.isFinite(n)) return undefined;
      return Math.max(0, Math.trunc(n));
    }
    case 'autoApproveFamilies': {
      // Accept a comma-separated list.
      const families = raw
        .split(',')
        .map((s) => s.trim())
        .filter(Boolean);
      return families;
    }
    default:
      return raw;
  }
}

function knobDisplay(k: AutonomyKnob): string {
  const v = layer.value.overrides[k];
  if (v === undefined) return '';
  if (Array.isArray(v)) return v.join(', ');
  return String(v);
}

watch(
  () => props.layerOverride,
  (v) => {
    if (v !== undefined && v !== null) layer.value = normaliseLayer(v);
  },
  { immediate: true },
);

onMounted(refresh);
onMounted(refreshLegacyControls);

defineExpose({ refresh, refreshLegacyControls });
</script>

<template>
  <section data-testid="autonomy-panel">
    <div class="flex items-center justify-between">
      <h2 class="font-ui text-[11px] uppercase tracking-[0.18em] text-ink-subtle">
        Autonomy
      </h2>
      <span
        v-if="isCustom"
        class="rounded-sm border border-border-muted bg-surface-2 px-2 py-0.5 font-ui text-[10px] uppercase tracking-[0.14em] text-ink-muted"
        data-testid="autonomy-custom-label"
      >
        Custom
      </span>
    </div>
    <p class="mt-1 font-ui text-[11px] text-ink-muted">
      Controls how aggressively the model proceeds without confirmation.
      Cedar deny remains the floor regardless of tier.
    </p>

    <div
      class="mt-3 inline-flex flex-wrap rounded-sm border border-border"
      role="radiogroup"
      aria-label="Autonomy tier"
      data-testid="autonomy-tier-selector"
    >
      <button
        v-for="t in showAllTiers ? TIERS : PRIMARY_TIERS"
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
        :data-testid="`autonomy-tier-${t}`"
        @click="setTier(t)"
      >
        {{ AUTONOMY_TIER_LABELS[t] }}
      </button>
    </div>

    <div class="mt-2 flex items-center gap-3">
      <button
        v-if="!showAllTiers"
        type="button"
        class="font-ui text-[11px] text-ink-subtle hover:text-ink underline-offset-2 hover:underline"
        data-testid="autonomy-show-all-tiers"
        @click="showAllTiers = true"
      >
        Show all 5 tiers
      </button>
      <button
        v-if="selectedTier !== null"
        type="button"
        class="font-ui text-[11px] text-ink-subtle hover:text-ink underline-offset-2 hover:underline"
        data-testid="autonomy-clear-tier"
        @click="clearTier"
      >
        Clear tier
      </button>
    </div>

    <p
      v-if="selectedTier !== null"
      class="mt-2 font-ui text-[12px] text-ink"
      data-testid="autonomy-tier-description"
    >
      {{ AUTONOMY_TIER_DESCRIPTIONS[selectedTier] }}
    </p>

    <div class="mt-4">
      <button
        type="button"
        class="font-ui text-[11px] uppercase tracking-[0.16em] text-ink-muted hover:text-ink"
        data-testid="autonomy-advanced-toggle"
        :aria-expanded="advancedOpen"
        @click="advancedOpen = !advancedOpen"
      >
        {{ advancedOpen ? '▾' : '▸' }} Advanced — per-knob overrides
      </button>
    </div>

    <div
      v-if="advancedOpen"
      class="mt-3 grid gap-3 sm:grid-cols-2"
      data-testid="autonomy-advanced-grid"
    >
      <div
        v-for="k in AUTONOMY_KNOB_ORDER"
        :key="k"
        class="rounded-sm border border-border-muted bg-surface-1 px-3 py-2"
      >
        <label
          class="block font-ui text-[10px] uppercase tracking-[0.16em] text-ink-subtle"
        >
          {{ AUTONOMY_KNOB_LABELS[k] }}
        </label>
        <div class="mt-1 flex items-center gap-2">
          <input
            type="text"
            class="flex-1 rounded-sm border border-border bg-surface-2 px-2 py-1 font-mono text-[12px] text-ink"
            :value="knobDisplay(k)"
            :placeholder="layer.overrides[k] === undefined ? '(use tier preset)' : ''"
            :data-testid="`autonomy-knob-${k}`"
            @change="(e) => setOverride(k, (e.target as HTMLInputElement).value)"
          />
          <button
            v-if="layer.overrides[k] !== undefined"
            type="button"
            class="font-ui text-[10px] uppercase tracking-[0.14em] text-ink-subtle hover:text-ink"
            :data-testid="`autonomy-knob-reset-${k}`"
            @click="resetOverride(k)"
          >
            Reset
          </button>
        </div>
      </div>
    </div>

    <div
      v-if="saveError"
      class="mt-2 rounded-sm border border-signal-danger bg-surface-1 px-3 py-2 font-ui text-[12px] text-signal-danger"
      role="alert"
      data-testid="autonomy-error"
    >
      {{ saveError }}
    </div>

    <div class="mt-5 border-t border-border-muted pt-4">
      <h3 class="font-ui text-[11px] uppercase tracking-[0.18em] text-ink-subtle">
        Fallback &amp; permission dials
      </h3>

      <div class="mt-3 rounded-sm border border-border-muted bg-surface-1 px-3 py-2">
        <label class="block font-ui text-[12px] text-ink" for="autonomy-max-agent-turns">
          Default iteration cap
        </label>
        <p class="mt-1 font-ui text-[11px] text-ink-muted max-w-prose">
          Legacy fallback for the chat graph's iteration cap. Only takes
          effect when the "Max iterations" override above is unset — a
          value there always wins. 0 means "use the built-in default".
        </p>
        <input
          id="autonomy-max-agent-turns"
          type="number"
          min="0"
          class="mt-2 w-32 rounded-sm border border-border bg-surface-2 px-2 py-1 font-mono text-[12px] text-ink"
          :value="maxAgentTurns"
          :disabled="maxAgentTurnsBusy"
          data-testid="autonomy-max-agent-turns"
          @change="(e) => persistMaxAgentTurns((e.target as HTMLInputElement).value)"
        />
        <div
          v-if="maxAgentTurnsError"
          class="mt-2 font-ui text-[11px] text-signal-danger"
          role="alert"
        >
          {{ maxAgentTurnsError }}
        </div>
      </div>

      <label
        class="mt-3 flex items-start justify-between gap-3 rounded-sm border border-border-muted bg-surface-1 px-3 py-2 cursor-pointer"
      >
        <span>
          <span class="block font-ui text-[12px] text-ink">
            Allow requesting filesystem access
          </span>
          <span class="mt-1 block font-ui text-[11px] text-ink-muted max-w-prose">
            When off, the model's tool catalog omits
            kenaz__request_filesystem_access entirely — it cannot ask to
            expand its filesystem allowlist during a run.
          </span>
        </span>
        <input
          type="checkbox"
          class="accent-accent mt-0.5 h-4 w-4 shrink-0"
          :checked="fsRequestAccessEnabled"
          :disabled="fsRequestAccessBusy"
          aria-label="Allow requesting filesystem access"
          data-testid="autonomy-fs-request-access-toggle"
          @change="toggleFSRequestAccess"
        />
      </label>
      <div
        v-if="fsRequestAccessError"
        class="mt-2 font-ui text-[11px] text-signal-danger"
        role="alert"
      >
        {{ fsRequestAccessError }}
      </div>
    </div>
  </section>
</template>
