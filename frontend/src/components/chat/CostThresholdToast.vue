<script setup lang="ts">
/**
 * CostThresholdToast — surfaces the WP06 monthly-spend notification
 * (token-cost-telemetry-01KQ8TD7).
 *
 * The backend emits a `cost.threshold.crossed` broker event whenever a
 * new tier (50 / 80 / 100 / 150 / 200 % of monthlyCostNotifyUsd)
 * crosses for the first time in a calendar month. The cost_threshold_fired
 * SQLite table dedupes per (year_month, pct) so the toast never
 * re-fires on app restart for the same crossing.
 *
 * Mounted once at the SessionsView root so a single instance handles
 * every threshold notification across the chat surface — matches the
 * MergeSuggestionToast pattern.
 */

import { computed, ref } from 'vue';
import { useEventStream } from '@/lib/useEventStream';
import type { CostThresholdCrossedPayload } from '@/lib/types';

const props = defineProps<{
  // Optional manual seed for tests: pre-populates the queue without a
  // backend event so the toast can be exercised in isolation.
  initial?: CostThresholdCrossedPayload | null;
}>();

const emit = defineEmits<{
  dismissed: [pct: number];
}>();

const queue = ref<CostThresholdCrossedPayload[]>(
  props.initial ? [props.initial] : [],
);

useEventStream<CostThresholdCrossedPayload>(
  'cost.threshold.crossed',
  (payload) => {
    if (!payload || typeof payload.pct !== 'number') return;
    // Dedupe by (yearMonth, pct) so a stray duplicate event from a
    // bridge-reconnect replay never double-toasts the user.
    if (
      queue.value.some(
        (q) => q.pct === payload.pct && q.yearMonth === payload.yearMonth,
      )
    ) {
      return;
    }
    queue.value = [...queue.value, payload];
  },
);

const head = computed<CostThresholdCrossedPayload | null>(
  () => queue.value[0] ?? null,
);

function popHead() {
  queue.value = queue.value.slice(1);
}

function dismiss() {
  const cur = head.value;
  if (!cur) return;
  emit('dismissed', cur.pct);
  popHead();
}

/** Format a USD amount for display: 2 decimals at >= $1, 3 decimals
 * at $0.01..$1, and the cent floor below that. Mirrors CostCell. */
function formatUsd(usd: number): string {
  if (usd < 0.01) return '<$0.01';
  if (usd < 1) return '$' + usd.toFixed(3);
  if (usd < 10) return '$' + usd.toFixed(2);
  return '$' + usd.toFixed(2);
}

/** Tier-aware headline copy. The 100% tier reads as "budget reached";
 * the 150 / 200 % tiers escalate to "over budget" so the user can't
 * miss a runaway spend. */
function headline(pct: number): string {
  if (pct >= 200) return `2× over your monthly budget`;
  if (pct >= 150) return `1.5× over your monthly budget`;
  if (pct >= 100) return `Monthly budget reached`;
  return `${pct}% of your monthly budget`;
}

defineExpose({ queue });
</script>

<template>
  <div
    v-if="head"
    class="pointer-events-none fixed inset-x-0 bottom-4 z-50 flex justify-center"
    role="status"
    aria-live="polite"
    data-testid="cost-threshold-toast"
  >
    <div
      class="pointer-events-auto flex max-w-md flex-col gap-2 rounded-md border bg-surface-1 p-3 shadow-lg"
      :class="
        head.pct >= 100 ? 'border-signal-warning' : 'border-accent-hairline'
      "
    >
      <div
        class="font-ui text-[11px] uppercase tracking-[0.18em]"
        :class="head.pct >= 100 ? 'text-signal-warning' : 'text-ink-subtle'"
      >
        Cost notification · {{ head.yearMonth }}
      </div>
      <div
        class="font-ui text-sm text-ink"
        data-testid="cost-threshold-headline"
      >
        {{ headline(head.pct) }}
      </div>
      <div class="font-ui text-xs text-ink-subtle" data-testid="cost-threshold-detail">
        Spent <span class="font-mono">{{ formatUsd(head.monthTotalUsd) }}</span>
        of <span class="font-mono">{{ formatUsd(head.thresholdUsd) }}</span>
        this month. Hard caps live in your provider dashboard.
      </div>
      <div class="mt-1 flex justify-end gap-2">
        <button
          type="button"
          class="font-ui text-xs text-ink-link hover:underline"
          data-testid="cost-threshold-dismiss"
          @click="dismiss"
        >
          Got it
        </button>
      </div>
    </div>
  </div>
</template>
