<script setup lang="ts">
/**
 * AutonomyChip — chat-header chip showing the effective autonomy tier
 * for the current session (autonomy-dial-01KR3M2A WP07).
 *
 * Click opens the SessionAutonomyPanel in a popover. Tooltip shows the
 * source-trace per knob (which layer contributed each value).
 *
 * Beta scope: drag-to-bump from plan §WP07 is deferred — interactive
 * panel is the primary affordance for v0.3.0. The chip is read-only at
 * the click level; full editing happens in the panel below.
 */

import { computed, onMounted, ref, watch } from 'vue';

import SessionAutonomyPanel from '@/views/sessions/SessionAutonomyPanel.vue';
import { useHarnessClient } from '@/lib/harnessClientContext';
import {
  AUTONOMY_KNOB_LABELS,
  AUTONOMY_KNOB_ORDER,
  AUTONOMY_TIER_LABELS,
  type AutonomyTier,
  type ResolvedAutonomy,
} from '@/lib/types';

const props = defineProps<{
  sessionId: string;
  /** Inject pre-resolved data for tests. */
  resolvedOverride?: ResolvedAutonomy | null;
  /** Skip the on-mount fetch. */
  skipFetch?: boolean;
}>();

const client = useHarnessClient();

const resolved = ref<ResolvedAutonomy | null>(null);
const open = ref(false);
const fetchError = ref<string | null>(null);

const tier = computed<string>(() => resolved.value?.resolved.tier ?? 'default');
const tierLabel = computed<string>(() => {
  const t = tier.value as AutonomyTier;
  return AUTONOMY_TIER_LABELS[t] ?? tier.value;
});

const isCustom = computed<boolean>(() => {
  const r = resolved.value;
  if (!r) return false;
  // Custom when ANY layer has overrides set on knobs.
  const anyOverride = (l: { overrides?: Record<string, unknown> }) =>
    l.overrides && Object.keys(l.overrides).length > 0;
  return anyOverride(r.session) || anyOverride(r.project) || anyOverride(r.global);
});

const tooltipText = computed<string>(() => {
  const r = resolved.value;
  if (!r) return 'Loading autonomy…';
  const lines = AUTONOMY_KNOB_ORDER.map((k) => {
    const src = r.resolved.sourceTrace[k] ?? 'tier-default';
    return `${AUTONOMY_KNOB_LABELS[k]}: ${src}`;
  });
  return ['Source trace:', ...lines].join('\n');
});

async function refresh() {
  if (props.skipFetch) {
    if (props.resolvedOverride) resolved.value = props.resolvedOverride;
    return;
  }
  if (!props.sessionId) return;
  try {
    const got = await client.sessions.resolveAutonomy(props.sessionId);
    resolved.value = got;
  } catch (err) {
    fetchError.value = err instanceof Error ? err.message : String(err);
  }
}

watch(
  () => props.resolvedOverride,
  (v) => {
    if (v) resolved.value = v;
  },
  { immediate: true },
);
watch(() => props.sessionId, () => void refresh());

onMounted(refresh);

function togglePopover() {
  open.value = !open.value;
}

function onPanelChange() {
  void refresh();
}
</script>

<template>
  <div class="relative inline-flex" data-testid="autonomy-chip">
    <button
      type="button"
      class="inline-flex items-center gap-1.5 rounded-full border px-2 py-0.5 font-ui text-[11px] uppercase tracking-[0.12em] transition-colors"
      :class="
        isCustom
          ? 'border-accent text-accent bg-accent/10 hover:bg-accent/15'
          : 'border-border-muted text-ink-muted bg-surface-1 hover:text-ink'
      "
      :title="tooltipText"
      :aria-expanded="open"
      data-testid="autonomy-chip-button"
      @click="togglePopover"
    >
      <span aria-hidden="true">●</span>
      {{ tierLabel }}
      <span
        v-if="isCustom"
        class="ml-0.5 text-[9px] font-normal not-italic text-ink-subtle"
        data-testid="autonomy-chip-custom-badge"
      >
        custom
      </span>
    </button>

    <div
      v-if="open"
      class="absolute right-0 top-full z-40 mt-2 w-[28rem] max-w-[90vw] rounded-md border border-border bg-surface-1 p-4 shadow-lg"
      role="dialog"
      data-testid="autonomy-chip-popover"
    >
      <div class="mb-2 flex items-center justify-between">
        <h3 class="font-ui text-[11px] uppercase tracking-[0.18em] text-ink-subtle">
          Session autonomy
        </h3>
        <button
          type="button"
          class="font-ui text-[11px] text-ink-muted hover:text-ink"
          data-testid="autonomy-chip-close"
          @click="open = false"
        >
          Close
        </button>
      </div>
      <SessionAutonomyPanel
        :session-id="sessionId"
        :resolved-override="resolved"
        @change="onPanelChange"
      />
    </div>

    <div
      v-if="fetchError"
      class="absolute right-0 top-full z-30 mt-1 rounded-sm border border-signal-danger bg-surface-1 px-2 py-1 font-ui text-[10px] text-signal-danger"
      role="alert"
    >
      {{ fetchError }}
    </div>
  </div>
</template>
