<script setup lang="ts">
/**
 * FallbackActivePill — transient inline indicator shown when the fallback
 * runner redirects a turn to a different provider.
 *
 * The backend emits 'llm:fallback-attempted' on the stream broker whenever
 * the runner hops to a fallback entry. This component listens for the event
 * and shows a dismissible pill. It auto-hides after 8 seconds.
 *
 * Mounted once in SessionsView (near the cost/token meter chips) so a single
 * instance handles every fallback event across the chat surface.
 *
 * model-fallback-routing-01NDFSEX04 WP05.
 */
import { ref } from 'vue';
import { useEventStream } from '@/lib/useEventStream';
import type { FallbackAttemptedPayload } from '@/lib/types';

const props = defineProps<{
  /** Optional manual seed for tests — pre-populate without a backend event. */
  initial?: FallbackAttemptedPayload | null;
}>();

const emit = defineEmits<{
  dismissed: [];
}>();

const payload = ref<FallbackAttemptedPayload | null>(props.initial ?? null);
let autoDismissTimer: ReturnType<typeof setTimeout> | null = null;

function show(p: FallbackAttemptedPayload) {
  payload.value = p;
  if (autoDismissTimer !== null) clearTimeout(autoDismissTimer);
  autoDismissTimer = setTimeout(dismiss, 8_000);
}

function dismiss() {
  payload.value = null;
  if (autoDismissTimer !== null) {
    clearTimeout(autoDismissTimer);
    autoDismissTimer = null;
  }
  emit('dismissed');
}

useEventStream<FallbackAttemptedPayload>('llm:fallback-attempted', (p) => {
  if (!p?.to_profile) return;
  show(p);
});

defineExpose({ payload, dismiss });
</script>

<template>
  <Transition
    enter-active-class="transition-opacity duration-200"
    enter-from-class="opacity-0"
    leave-active-class="transition-opacity duration-150"
    leave-to-class="opacity-0"
  >
    <div
      v-if="payload"
      class="inline-flex items-center gap-1.5 rounded-full border border-signal-warning bg-surface-1 px-2.5 py-1 font-ui text-[11px] text-ink shadow-sm"
      role="status"
      aria-live="polite"
      data-testid="fallback-active-pill"
    >
      <span class="h-1.5 w-1.5 rounded-full bg-signal-warning flex-shrink-0" />
      <span>Fallback: {{ payload.to_profile }}<template v-if="payload.to_model"> / {{ payload.to_model }}</template></span>
      <button
        type="button"
        class="ml-1 text-ink-muted hover:text-ink"
        aria-label="Dismiss fallback indicator"
        data-testid="fallback-active-pill-dismiss"
        @click="dismiss"
      >
        &times;
      </button>
    </div>
  </Transition>
</template>
