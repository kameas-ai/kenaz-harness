<script setup lang="ts">
import { useShellStatus } from '@/lib/useHarnessAPI';

/**
 * StatusPill — minimal build-version footer. The original FR-001f
 * triple (provider · trust · build) is reduced to just the build
 * label until the upstream surfaces emit real values for the other
 * two fields. Slot stays in LegendBar for surface-injected pills.
 */
const status = useShellStatus();
</script>

<template>
  <div
    class="flex items-center gap-2 px-3 py-1 text-[11px] font-ui text-ink-muted"
    role="status"
    aria-label="Harness build"
  >
    <span class="text-ink-dim">v</span>
    <span class="font-mono text-ink">{{ status.harnessBuild }}</span>
  </div>
</template>
