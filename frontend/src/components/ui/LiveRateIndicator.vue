<script setup lang="ts">
import { computed } from 'vue';

/**
 * LiveRateIndicator — small inline `0.4 e/s`-style rate label (FR-001g).
 * rAF throttling is the producer's responsibility; this component just
 * formats the numeric rate.
 */
const props = withDefaults(
  defineProps<{
    rate: number;
    unit: string;
    precision?: number;
  }>(),
  { precision: 1 },
);

const formatted = computed(() => props.rate.toFixed(props.precision));
</script>

<template>
  <span
    class="inline-flex items-center gap-1 font-mono text-[11px] text-ink-subtle"
    role="status"
    :aria-label="`Rate ${formatted} ${unit}`"
  >
    <span>{{ formatted }}</span>
    <span>{{ unit }}</span>
  </span>
</template>
