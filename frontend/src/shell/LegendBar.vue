<script setup lang="ts">
import LiveRateIndicator from '@/components/ui/LiveRateIndicator.vue';
import StatusPill from './StatusPill.vue';
import { useShellStatus } from '@/lib/useHarnessAPI';

/**
 * LegendBar — bottom rail. The Kenaz-inherited category dots
 * (FILESYSTEM / PROCESS / CLIPBOARD / NETWORK / KEYSTROKE) are
 * hidden by default in the harness because nothing emits per-
 * category telemetry yet. The `legend` slot is preserved so a
 * future surface can inject its own legend without re-adding the
 * dots here.
 */
const status = useShellStatus();
</script>

<template>
  <div class="flex items-center justify-between px-3 py-1.5 text-[11px]">
    <div class="flex items-center gap-3" aria-label="Status legend">
      <slot name="legend" />
      <slot name="rate">
        <LiveRateIndicator
          v-if="status.eventRate > 0"
          :rate="status.eventRate"
          unit="e/s"
        />
      </slot>
    </div>
    <StatusPill />
  </div>
</template>
