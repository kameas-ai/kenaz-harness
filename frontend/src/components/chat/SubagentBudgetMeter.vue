<script setup lang="ts">
/**
 * SubagentBudgetMeter — compact token + time usage meters shown in the
 * SubagentTab header bar.
 *
 * Renders two progress segments: tokens (used / budget) and time
 * (elapsed / budget). When budget is 0 or undefined, the bar shows an
 * indeterminate spinner and the label shows the raw usage count.
 *
 * branch-subagent-interactive-01KZNP3B WP08.
 */
import { computed } from 'vue';

const props = defineProps<{
  /** Tokens consumed so far. */
  tokensUsed?: number;
  /** Token budget from the profile (0 = unlimited). */
  budgetTokens?: number;
  /** Wall-clock seconds elapsed since spawn. */
  elapsedS?: number;
  /** Time budget from the profile in seconds (0 = unlimited). */
  budgetTimeS?: number;
}>();

const tokenPct = computed(() => {
  if (!props.budgetTokens || !props.tokensUsed) return null;
  return Math.min(100, Math.round((props.tokensUsed / props.budgetTokens) * 100));
});

const timePct = computed(() => {
  if (!props.budgetTimeS || !props.elapsedS) return null;
  return Math.min(100, Math.round((props.elapsedS / props.budgetTimeS) * 100));
});

function formatK(n: number | undefined): string {
  if (n === undefined || n === 0) return '—';
  return n >= 1000 ? `${(n / 1000).toFixed(1)}k` : String(n);
}

function formatTime(s: number | undefined): string {
  if (s === undefined || s === 0) return '—';
  if (s < 60) return `${s}s`;
  const m = Math.floor(s / 60);
  const rem = s % 60;
  return rem ? `${m}m${rem}s` : `${m}m`;
}
</script>

<template>
  <div class="flex items-center gap-3 font-ui text-[11px] text-ink-subtle" data-testid="subagent-budget-meter">
    <!-- Token meter -->
    <div class="flex items-center gap-1">
      <span>Tokens:</span>
      <span class="text-ink">{{ formatK(tokensUsed) }}</span>
      <template v-if="budgetTokens">
        <span>/</span>
        <span>{{ formatK(budgetTokens) }}</span>
        <div class="h-1.5 w-20 overflow-hidden rounded-full bg-surface-2">
          <div
            class="h-full rounded-full transition-all"
            :class="(tokenPct ?? 0) >= 90 ? 'bg-status-error' : 'bg-accent'"
            :style="{ width: `${tokenPct ?? 0}%` }"
            :data-testid="`token-bar-${tokenPct ?? 0}`"
          />
        </div>
      </template>
    </div>

    <!-- Time meter -->
    <div class="flex items-center gap-1">
      <span>Time:</span>
      <span class="text-ink">{{ formatTime(elapsedS) }}</span>
      <template v-if="budgetTimeS">
        <span>/</span>
        <span>{{ formatTime(budgetTimeS) }}</span>
        <div class="h-1.5 w-16 overflow-hidden rounded-full bg-surface-2">
          <div
            class="h-full rounded-full transition-all"
            :class="(timePct ?? 0) >= 90 ? 'bg-status-error' : 'bg-accent'"
            :style="{ width: `${timePct ?? 0}%` }"
          />
        </div>
      </template>
    </div>
  </div>
</template>
