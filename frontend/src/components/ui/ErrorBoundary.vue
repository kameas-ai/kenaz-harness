<script setup lang="ts">
import { onErrorCaptured, ref } from 'vue';
import { reportError } from '@/lib/eventLog';

/**
 * ErrorBoundary — Vue's errorCaptured hook captures crashes and reports
 * through eventLog.ts (FR-018). Renders a quiet recovery affordance
 * that resets the boundary state.
 */
const captured = ref<Error | null>(null);

onErrorCaptured((err, _instance, info) => {
  captured.value = err instanceof Error ? err : new Error(String(err));
  reportError(err, info);
  return false; // stop propagation
});

function recover() {
  captured.value = null;
}
</script>

<template>
  <div v-if="captured" class="px-6 py-6">
    <div
      class="rounded-md border border-signal-warn bg-surface-1 px-4 py-3 max-w-prose"
      role="alert"
    >
      <div class="font-ui text-[11px] uppercase tracking-[0.18em] text-signal-warn">
        Surface error
      </div>
      <p class="mt-1 font-ui text-sm text-ink">
        Something failed while rendering this surface. The error has been logged.
      </p>
      <button
        type="button"
        class="mt-2 font-ui text-xs text-accent hover:text-ink"
        @click="recover"
      >
        Retry
      </button>
    </div>
  </div>
  <slot v-else />
</template>
