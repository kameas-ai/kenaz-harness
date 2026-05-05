<script setup lang="ts">
/**
 * TokenMeterChip — per-message token-cost chip for assistant bubbles.
 * (per-message-token-meter-01KR3PQR)
 *
 * Renders a small muted tag next to the role label:
 *   "1.2k → 240 = 1.4k tok · $0.012"
 *                 (or just the token count when cost is unknown)
 *
 * Click opens an inline popover with the full breakdown:
 *   - Prompt tokens
 *   - Completion tokens
 *   - Total tokens
 *   - Cost in USD (with source annotation: exact / estimated / unknown)
 *
 * Visibility is controlled by the parent — the chip renders only when
 * the `settings.showPerMessageTokenMeter` toggle is true AND the message
 * carries usage data (promptTokens / completionTokens must be defined).
 */

import { computed, ref } from 'vue';

const props = defineProps<{
  promptTokens?: number;
  completionTokens?: number;
  costUsd?: number;
  /**
   * "provider" | "derived" | "mixed" | "unknown".
   * Drives the cost prefix (~) and the popover source label.
   */
  costSource?: string;
}>();

const open = ref(false);

/** True when the chip has enough data to render. */
const hasData = computed(() =>
  props.promptTokens !== undefined && props.completionTokens !== undefined,
);

function formatTokens(n: number): string {
  if (n >= 1_000_000) return (n / 1_000_000).toFixed(1) + 'M';
  if (n >= 1_000) return (n / 1_000).toFixed(1) + 'k';
  return String(n);
}

function formatCost(usd: number): string {
  if (usd < 0.0001) return '<$0.01';
  if (usd < 0.01) return '$' + usd.toFixed(4);
  if (usd < 1) return '$' + usd.toFixed(3);
  if (usd < 10) return '$' + usd.toFixed(2);
  return '$' + usd.toFixed(1);
}

/** One-line chip label: "1.2k → 240 = 1.4k tok · $0.012" */
const chipLabel = computed<string>(() => {
  if (!hasData.value) return '';
  const prompt = props.promptTokens!;
  const completion = props.completionTokens!;
  const total = prompt + completion;
  const tokPart = `${formatTokens(prompt)} → ${formatTokens(completion)} = ${formatTokens(total)} tok`;
  const costUsd = props.costUsd;
  if (!costUsd || costUsd <= 0 || props.costSource === 'unknown') {
    return tokPart;
  }
  const prefix = props.costSource === 'provider' ? '' : '~';
  return `${tokPart} · ${prefix}${formatCost(costUsd)}`;
});

const costSourceLabel = computed<string>(() => {
  switch (props.costSource) {
    case 'provider':
      return 'Provider-reported (exact)';
    case 'derived':
      return 'Estimated from pricing table';
    case 'mixed':
      return 'Mix of provider-reported and estimated';
    default:
      return 'Unknown cost source';
  }
});

function togglePopover() {
  open.value = !open.value;
}

function closePopover() {
  open.value = false;
}
</script>

<template>
  <span
    v-if="hasData"
    class="relative inline-flex"
    data-testid="token-meter-chip"
  >
    <button
      type="button"
      class="inline-flex items-center font-mono text-[10px] text-ink-subtle tabular-nums rounded-sm border border-transparent px-1 py-0 hover:border-border-muted hover:text-ink-muted transition-colors"
      :aria-label="`Token usage: ${chipLabel}`"
      :aria-expanded="open"
      data-testid="token-meter-chip-button"
      @click.stop="togglePopover"
    >
      {{ chipLabel }}
    </button>

    <!-- Breakdown popover -->
    <div
      v-if="open"
      class="absolute left-0 top-full z-30 mt-1 min-w-[18rem] rounded-md border border-border-muted bg-surface-1 px-3 py-2.5 shadow-lg"
      role="dialog"
      aria-label="Token usage breakdown"
      data-testid="token-meter-popover"
    >
      <div class="mb-2 flex items-center justify-between">
        <span class="font-ui text-[10px] uppercase tracking-[0.18em] text-ink-subtle">
          Token usage
        </span>
        <button
          type="button"
          class="font-ui text-[11px] text-ink-muted hover:text-ink"
          aria-label="Close token usage popover"
          data-testid="token-meter-popover-close"
          @click.stop="closePopover"
        >
          ✕
        </button>
      </div>
      <dl class="space-y-1 font-mono text-[11px]">
        <div class="flex justify-between gap-4">
          <dt class="text-ink-muted">Prompt tokens</dt>
          <dd class="tabular-nums text-ink" data-testid="token-meter-prompt">
            {{ promptTokens?.toLocaleString() ?? '—' }}
          </dd>
        </div>
        <div class="flex justify-between gap-4">
          <dt class="text-ink-muted">Completion tokens</dt>
          <dd class="tabular-nums text-ink" data-testid="token-meter-completion">
            {{ completionTokens?.toLocaleString() ?? '—' }}
          </dd>
        </div>
        <div class="flex justify-between gap-4 border-t border-border-muted pt-1">
          <dt class="text-ink-muted">Total</dt>
          <dd class="tabular-nums text-ink" data-testid="token-meter-total">
            {{ ((promptTokens ?? 0) + (completionTokens ?? 0)).toLocaleString() }}
          </dd>
        </div>
        <div
          v-if="costUsd && costUsd > 0 && costSource !== 'unknown'"
          class="flex justify-between gap-4"
        >
          <dt class="text-ink-muted">Cost USD</dt>
          <dd class="tabular-nums text-ink" data-testid="token-meter-cost">
            {{ costSource === 'provider' ? '' : '~' }}{{ formatCost(costUsd) }}
          </dd>
        </div>
      </dl>
      <p
        v-if="costSource"
        class="mt-2 font-ui text-[10px] text-ink-subtle"
        data-testid="token-meter-cost-source"
      >
        {{ costSourceLabel }}
      </p>
    </div>
  </span>
</template>
