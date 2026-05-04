<script setup lang="ts">
/**
 * HealthPill — status indicator dot for a single MCP recipe's health state.
 * Renders a filled dot (●) coloured by state + a short label.
 * (mcp-server-health-ui-01KQ8TD6 WP03)
 *
 * Props:
 *   state — one of: "running" | "starting" | "restarting" | "failed" | "stopped"
 *   label — optional override label; defaults to the state string
 *   compact — when true shows only the dot (no label), suitable for toolbar use
 */
import { computed } from 'vue';
import type { RecipeState } from '@/lib/types';

const props = withDefaults(
  defineProps<{
    state: RecipeState | string;
    label?: string;
    compact?: boolean;
  }>(),
  {
    label: undefined,
    compact: false,
  },
);

/** Tailwind colour class derived from state. Uses design tokens only. */
const dotClass = computed<string>(() => {
  switch (props.state) {
    case 'running':
      return 'text-signal-ok';
    case 'starting':
    case 'restarting':
      return 'text-signal-warn';
    case 'failed':
      return 'text-signal-danger';
    case 'stopped':
    default:
      return 'text-ink-dim';
  }
});

const displayLabel = computed<string>(() => props.label ?? props.state);

/** Accessible description of the health state for screen readers. */
const ariaLabel = computed<string>(() => `MCP server status: ${displayLabel.value}`);
</script>

<template>
  <span
    class="inline-flex items-center gap-1"
    :aria-label="ariaLabel"
    role="status"
    :data-testid="`health-pill-${state}`"
  >
    <!-- Dot -->
    <span
      class="text-[8px] leading-none select-none"
      :class="dotClass"
      aria-hidden="true"
    >●</span>
    <!-- Label (hidden in compact mode) -->
    <span
      v-if="!compact"
      class="font-ui text-[10px] uppercase tracking-[0.14em]"
      :class="dotClass"
    >{{ displayLabel }}</span>
  </span>
</template>
