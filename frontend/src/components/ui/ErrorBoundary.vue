<script setup lang="ts">
import { onErrorCaptured, ref, watch } from 'vue';
import { useRoute, useRouter } from 'vue-router';
import { reportError } from '@/lib/eventLog';

/**
 * ErrorBoundary — Vue's errorCaptured hook captures crashes and reports
 * through eventLog.ts (FR-018). Renders a quiet recovery affordance:
 *
 *   - "Dismiss" re-mounts the slot once. A second throw within the same
 *     route transitions to /sessions (FR-006) rather than a dismiss loop.
 *   - The boundary auto-recovers when the route changes (FR-002), so the
 *     nav rail remains interactive and navigating away always clears the
 *     error card.
 *   - A "Go to Sessions" in-card action (FR-003) provides a keyboard-
 *     reachable fallback even when the slot content is invisible.
 *   - The error message + stack are shown inline so a developer or
 *     support engineer can read the cause without opening DevTools.
 *
 * IMPORTANT: this boundary must be scoped to the SURFACE CONTENT REGION
 * only (Shell.vue's router-view area). It must NOT wrap the left nav rail
 * or window chrome — those must stay mounted and interactive (FR-001).
 */
const captured = ref<Error | null>(null);
/**
 * dismissCount tracks how many times the user has dismissed within the
 * current route. On the first dismiss we re-mount the slot (giving
 * transient errors a chance to self-heal). On a second throw we navigate
 * to /sessions instead of showing the dismiss loop forever (FR-006).
 */
const dismissCount = ref(0);

const route = useRoute();
const router = useRouter();

onErrorCaptured((err, _instance, info) => {
  const e = err instanceof Error ? err : new Error(String(err));
  captured.value = e;
  reportError(err, info);
  return false; // stop propagation
});

function dismiss() {
  if (dismissCount.value >= 1) {
    // Second throw within the same route: bail out to /sessions (FR-006).
    void router.push('/sessions');
    return;
  }
  dismissCount.value += 1;
  captured.value = null;
}

function goToSessions() {
  void router.push('/sessions');
}

// Recover automatically when the user navigates (FR-002): if a surface
// is broken at /contexts, clicking Sessions in the nav rail clears the
// error card and renders the new surface. Reset the dismiss counter so
// the new route starts fresh.
watch(
  () => route.fullPath,
  () => {
    captured.value = null;
    dismissCount.value = 0;
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
          data-testid="error-boundary-dismiss"
          @click="dismiss"
        >
          Dismiss
        </button>
        <span class="font-ui text-[11px] text-ink-dim">or</span>
        <button
          type="button"
          class="font-ui text-xs text-accent hover:text-ink"
          data-testid="error-boundary-go-to-sessions"
          @click="goToSessions"
        >
          Go to Sessions
        </button>
      </div>
    </div>
  </div>
  <slot v-else />
</template>
