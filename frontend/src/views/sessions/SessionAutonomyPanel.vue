<script setup lang="ts">
/**
 * SessionAutonomyPanel — per-session autonomy override panel
 * (autonomy-dial-01KR3M2A WP07).
 *
 * Same shape as the project panel with an extra "Reset session to
 * project default" button and a 4-column source-trace annotation per
 * knob (Global / Project / Session / Effective). Calls
 * Sessions_ResolveAutonomy on mount + after each save so the
 * Effective column always shows the live-resolved value.
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
  type ResolvedAutonomy,
} from '@/lib/types';

const props = defineProps<{
  sessionId: string;
  /** Skip on-mount fetch (vitest). */
  skipFetch?: boolean;
  /** Inject pre-resolved data for tests. */
  resolvedOverride?: ResolvedAutonomy | null;
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

const sessionLayer = ref<AutonomyLayer>(emptyAutonomyLayer());
const resolved = ref<ResolvedAutonomy | null>(null);
const advancedOpen = ref(false);
const saveError = ref<string | null>(null);

const selectedTier = computed<AutonomyTier | null>(() => sessionLayer.value.level);
const overrideKeys = computed<AutonomyKnob[]>(
  () =>
    AUTONOMY_KNOB_ORDER.filter(
      (k) => sessionLayer.value.overrides[k] !== undefined,
    ) as AutonomyKnob[],
);
const isCustom = computed(() => overrideKeys.value.length > 0);

async function refresh() {
  if (props.skipFetch) {
    if (props.resolvedOverride) {
      resolved.value = props.resolvedOverride;
      sessionLayer.value = normalise(props.resolvedOverride.session);
    }
    return;
  }
  try {
    const got = await client.sessions.resolveAutonomy(props.sessionId);
    resolved.value = got;
    sessionLayer.value = normalise(got.session);
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
    await client.sessions.setAutonomy(props.sessionId, sessionLayer.value);
    emit('change', sessionLayer.value);
    // Refresh the resolved trace so Effective column updates.
    if (!props.skipFetch) {
      const got = await client.sessions.resolveAutonomy(props.sessionId);
      resolved.value = got;
    }
  } catch (err) {
    saveError.value = err instanceof Error ? err.message : String(err);
  }
}

function setTier(t: AutonomyTier | null) {
  sessionLayer.value = { ...sessionLayer.value, level: t };
  void persist();
}

function resetSession() {
  sessionLayer.value = emptyAutonomyLayer();
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
  sessionLayer.value = {
    ...sessionLayer.value,
    overrides: { ...sessionLayer.value.overrides, [k]: parsed },
  };
  void persist();
}

function resetOverride(k: AutonomyKnob) {
  if (sessionLayer.value.overrides[k] === undefined) return;
  const next = { ...sessionLayer.value.overrides };
  delete next[k];
  sessionLayer.value = { ...sessionLayer.value, overrides: next };
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
  const v = sessionLayer.value.overrides[k];
  if (v === undefined) return '';
  if (Array.isArray(v)) return v.join(', ');
  return String(v);
}

function layerKnob(k: AutonomyKnob, layer: AutonomyLayer | undefined): string {
  if (!layer) return '—';
  const v = layer.overrides[k];
  if (v !== undefined) {
    if (Array.isArray(v)) return v.join(', ');
    return String(v);
  }
  if (layer.level) return `(tier: ${layer.level})`;
  return '—';
}

function effectiveKnob(k: AutonomyKnob): string {
  const r = resolved.value?.resolved;
  if (!r) return '';
  switch (k) {
    case 'maxIterations':
      return String(r.maxIterations);
    case 'askOnAmbiguity':
      return r.askOnAmbiguity;
    case 'autoApproveFamilies':
      return (r.autoApproveFamilies ?? []).join(', ') || '(none)';
    case 'tokenCeilingPerTurn':
      return String(r.tokenCeilingPerTurn);
    case 'recapStyle':
      return r.recapStyle;
    case 'continueOnError':
      return r.continueOnError;
    case 'destructiveActionPosture':
      return r.destructiveActionPosture;
  }
}

function sourceFor(k: AutonomyKnob): string {
  return resolved.value?.resolved.sourceTrace[k] ?? '';
}

watch(
  () => props.resolvedOverride,
  (v) => {
    if (v) {
      resolved.value = v;
      sessionLayer.value = normalise(v.session);
    }
  },
  { immediate: true },
);
watch(() => props.sessionId, () => void refresh());

onMounted(refresh);

defineExpose({ refresh });
</script>

<template>
  <section data-testid="session-autonomy-panel">
    <div class="flex items-center justify-between">
      <h2 class="font-ui text-[11px] uppercase tracking-[0.18em] text-ink-subtle">
        Session autonomy
      </h2>
      <span
        v-if="isCustom"
        class="rounded-sm border border-border-muted bg-surface-2 px-2 py-0.5 font-ui text-[10px] uppercase tracking-[0.14em] text-ink-muted"
        data-testid="session-autonomy-custom"
      >
        Custom
      </span>
    </div>
    <p class="mt-1 font-ui text-[11px] text-ink-muted">
      Per-session override. Folds atop project + global. Use Inherit
      to fall through. Reset clears every session-level override.
    </p>

    <div
      class="mt-3 inline-flex flex-wrap rounded-sm border border-border"
      role="radiogroup"
      aria-label="Session autonomy tier"
      data-testid="session-autonomy-tier-selector"
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
        data-testid="session-autonomy-tier-inherit"
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
        :data-testid="`session-autonomy-tier-${t}`"
        @click="setTier(t)"
      >
        {{ AUTONOMY_TIER_LABELS[t] }}
      </button>
    </div>

    <div class="mt-2 flex items-center gap-3">
      <button
        v-if="!sessionLayer.level && overrideKeys.length === 0 ? false : true"
        type="button"
        class="font-ui text-[11px] text-ink-subtle hover:text-ink underline-offset-2 hover:underline"
        data-testid="session-autonomy-reset-all"
        @click="resetSession"
      >
        Reset session to project default
      </button>
    </div>

    <p
      v-if="selectedTier !== null"
      class="mt-2 font-ui text-[12px] text-ink"
      data-testid="session-autonomy-tier-description"
    >
      {{ AUTONOMY_TIER_DESCRIPTIONS[selectedTier] }}
    </p>

    <div class="mt-4">
      <button
        type="button"
        class="font-ui text-[11px] uppercase tracking-[0.16em] text-ink-muted hover:text-ink"
        data-testid="session-autonomy-advanced-toggle"
        :aria-expanded="advancedOpen"
        @click="advancedOpen = !advancedOpen"
      >
        {{ advancedOpen ? '▾' : '▸' }} Advanced — per-knob overrides + source trace
      </button>
    </div>

    <div
      v-if="advancedOpen"
      class="mt-3 overflow-x-auto"
      data-testid="session-autonomy-advanced-grid"
    >
      <table class="w-full font-ui text-[11px]">
        <thead>
          <tr class="text-left text-ink-subtle">
            <th class="pb-1 pr-3">Knob</th>
            <th class="pb-1 pr-3">Global</th>
            <th class="pb-1 pr-3">Project</th>
            <th class="pb-1 pr-3">Session override</th>
            <th class="pb-1 pr-3">Effective</th>
            <th class="pb-1">Source</th>
          </tr>
        </thead>
        <tbody>
          <tr
            v-for="k in AUTONOMY_KNOB_ORDER"
            :key="k"
            class="border-t border-border-muted align-top"
          >
            <td class="py-1 pr-3 text-ink">{{ AUTONOMY_KNOB_LABELS[k] }}</td>
            <td class="py-1 pr-3 font-mono text-ink-muted">
              {{ layerKnob(k, resolved?.global) }}
            </td>
            <td class="py-1 pr-3 font-mono text-ink-muted">
              {{ layerKnob(k, resolved?.project) }}
            </td>
            <td class="py-1 pr-3">
              <div class="flex items-center gap-2">
                <input
                  type="text"
                  class="w-32 rounded-sm border border-border bg-surface-2 px-1.5 py-0.5 font-mono text-[11px] text-ink"
                  :value="knobDisplay(k)"
                  :aria-label="`${k} autonomy setting`"
                  :placeholder="
                    sessionLayer.overrides[k] === undefined ? '(inherit)' : ''
                  "
                  :data-testid="`session-autonomy-knob-${k}`"
                  @change="
                    (e) => setOverride(k, (e.target as HTMLInputElement).value)
                  "
                />
                <button
                  v-if="sessionLayer.overrides[k] !== undefined"
                  type="button"
                  class="text-[10px] uppercase tracking-[0.14em] text-ink-subtle hover:text-ink"
                  :data-testid="`session-autonomy-knob-reset-${k}`"
                  @click="resetOverride(k)"
                >
                  Reset
                </button>
              </div>
            </td>
            <td
              class="py-1 pr-3 font-mono text-ink"
              :data-testid="`session-autonomy-effective-${k}`"
            >
              {{ effectiveKnob(k) }}
            </td>
            <td
              class="py-1 text-ink-subtle"
              :data-testid="`session-autonomy-source-${k}`"
            >
              {{ sourceFor(k) }}
            </td>
          </tr>
        </tbody>
      </table>
    </div>

    <div
      v-if="saveError"
      class="mt-2 rounded-sm border border-signal-danger bg-surface-1 px-3 py-2 font-ui text-[12px] text-signal-danger"
      role="alert"
      data-testid="session-autonomy-error"
    >
      {{ saveError }}
    </div>
  </section>
</template>
