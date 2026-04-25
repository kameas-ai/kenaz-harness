<script setup lang="ts">
import { onErrorCaptured, ref, watch } from 'vue';
import { useRoute } from 'vue-router';
import { reportError } from '@/lib/eventLog';

/**
 * ErrorBoundary — Vue's errorCaptured hook captures crashes and reports
 * through eventLog.ts (FR-018). Renders a quiet recovery affordance:
 *
 *   - "Dismiss" clears the captured error and re-renders the slot.
 *   - The boundary auto-recovers when the route changes, so the user
 *     can always navigate away from a broken surface.
 *   - The error message + stack are shown inline so a developer or
 *     support engineer can read the cause without opening DevTools.
 */
const captured = ref<Error | null>(null);
const route = useRoute();

onErrorCaptured((err, _instance, info) => {
  const e = err instanceof Error ? err : new Error(String(err));
  captured.value = e;
  reportError(err, info);
  return false; // stop propagation
});

function dismiss() {
  captured.value = null;
}

// Recover automatically when the user navigates: if a surface is
// broken at /sessions, navigating to /providers should return them
// to a working app rather than a sticky error pane.
watch(
  () => route.fullPath,
  () => {
    captured.value = null;
  },
);
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
      <pre
        class="mt-2 max-h-40 overflow-auto rounded-sm bg-surface-2 px-2 py-1.5 font-mono text-[11px] text-ink-muted whitespace-pre-wrap"
      >{{ captured.message }}</pre>
      <div class="mt-2 flex items-center gap-2">
        <button
          type="button"
          class="font-ui text-xs text-accent hover:text-ink"
          @click="dismiss"
        >
          Dismiss
        </button>
        <span class="font-ui text-[11px] text-ink-dim">
          or pick another surface from the left rail.
        </span>
      </div>
    </div>
  </div>
  <slot v-else />
</template>
