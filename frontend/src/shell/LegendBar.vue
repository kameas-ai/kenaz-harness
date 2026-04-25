<script setup lang="ts">
import { CATEGORY_REGISTRY, type Category } from '@/lib/categories';
import LiveRateIndicator from '@/components/ui/LiveRateIndicator.vue';
import StatusPill from './StatusPill.vue';
import { useShellStatus } from '@/lib/useHarnessAPI';

const status = useShellStatus();

// Keep legend slim — show the 5 Kenaz categories by default; surfaces
// can extend through a slot.
const defaultLegend: Category[] = [
  'FILESYSTEM',
  'PROCESS',
  'CLIPBOARD',
  'NETWORK',
  'KEYSTROKE',
];
</script>

<template>
  <div class="flex items-center justify-between px-3 py-1.5 text-[11px]">
    <div class="flex items-center gap-3" aria-label="Category legend">
      <slot name="legend">
        <span
          v-for="cat in defaultLegend"
          :key="cat"
          class="flex items-center gap-1.5 font-ui text-ink-muted"
        >
          <span
            class="inline-block w-1.5 h-1.5 rounded-full"
            :style="{ background: `var(${CATEGORY_REGISTRY[cat].token})` }"
            :aria-hidden="true"
          ></span>
          <span class="uppercase tracking-[0.12em]">{{ cat }}</span>
        </span>
      </slot>
      <slot name="rate">
        <LiveRateIndicator
          v-if="status.value.eventRate > 0"
          :rate="status.value.eventRate"
          unit="e/s"
        />
      </slot>
    </div>
    <StatusPill />
  </div>
</template>
