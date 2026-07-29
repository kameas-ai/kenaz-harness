<script setup lang="ts">
/**
 * ModelSizeBadge — renders a compact size + quant badge for a local model.
 *
 * States:
 *   - sizeBytes > 0 and quantLevel: "4.7 GB · Q4_K_M"
 *   - sizeBytes > 0, no quant: "4.7 GB"
 *   - sizeBytes 0: renders nothing (empty slot)
 *
 * When sizeBytes > effectiveRamBytes * 0.85, an amber "Needs more RAM"
 * pill is rendered alongside the size badge.
 *
 * (local-model-runtimes-01KQ8VMZ WP06)
 */

import { computed } from 'vue';

const props = defineProps<{
  sizeBytes?: number;
  quantLevel?: string;
  /** effectiveRamBytes — from EffectiveLocalRuntimeRAMBytes. 0 = unknown. */
  effectiveRamBytes?: number;
}>();

const sizeLabel = computed<string>(() => {
  const b = props.sizeBytes ?? 0;
  if (b === 0) return '';
  const gb = b / (1024 * 1024 * 1024);
  return `${gb.toFixed(1)} GB`;
});

const displayText = computed<string>(() => {
  const size = sizeLabel.value;
  if (!size) return '';
  const quant = props.quantLevel;
  return quant ? `${size} · ${quant}` : size;
});

/** True when sizeBytes exceeds 85% of effectiveRamBytes. */
const needsMoreRAM = computed<boolean>(() => {
  const size = props.sizeBytes ?? 0;
  const ram = props.effectiveRamBytes ?? 0;
  if (size === 0 || ram === 0) return false;
  return size > ram * 0.85;
});
</script>

<template>
  <span
    v-if="displayText"
    class="inline-flex items-center gap-1.5"
    data-testid="model-size-badge"
  >
    <span class="text-xs text-ink-muted font-mono">{{ displayText }}</span>
    <span
      v-if="needsMoreRAM"
      class="inline-flex items-center rounded px-1.5 py-0.5 text-[10px] font-ui font-medium bg-signal-warn-soft text-signal-warn border border-signal-warn"
      data-testid="needs-more-ram-pill"
    >
      Needs more RAM
    </span>
  </span>
</template>
