<script setup lang="ts">
/**
 * RetryAfterRotateToast — surfaces a post-rotation turn failure.
 * (provider-keychain-rotation-01KQ8TD9 WP05)
 *
 * Emitted when a resumed (post-rotation) turn errors for non-auth reasons
 * (`provider:retry-after-rotation-failed` broker topic). Copy differs from
 * AuthFailureToast: the key is confirmed-good; this is a regular request
 * failure after a validated key. Actions: "View error / Retry / Dismiss".
 *
 * Dedup: one entry per profileID at a time.
 * Mounted once at the SessionsView root.
 * role="alertdialog" for keyboard accessibility (WP05 a11y criterion).
 */

import { computed, ref } from 'vue';
import { useEventStream } from '@/lib/useEventStream';
import type { RetryAfterRotationFailedPayload } from '@/lib/types';

const props = defineProps<{
  /** Optional manual seed for tests: pre-populate without a backend event. */
  initial?: RetryAfterRotationFailedPayload | null;
}>();

const emit = defineEmits<{
  dismissed: [profileID: string];
}>();

const queue = ref<RetryAfterRotationFailedPayload[]>(
  props.initial ? [props.initial] : [],
);

useEventStream<RetryAfterRotationFailedPayload>(
  'provider:retry-after-rotation-failed',
  (payload) => {
    if (!payload?.profile_id) return;
    // Dedup: only one entry per profileID.
    if (queue.value.some((q) => q.profile_id === payload.profile_id)) return;
    queue.value = [...queue.value, payload];
  },
);

const head = computed<RetryAfterRotationFailedPayload | null>(
  () => queue.value[0] ?? null,
);

const showDetail = ref(false);

function popHead() {
  queue.value = queue.value.slice(1);
  showDetail.value = false;
}

function dismiss() {
  const cur = head.value;
  if (!cur) return;
  emit('dismissed', cur.profile_id);
  popHead();
}

function toggleDetail() {
  showDetail.value = !showDetail.value;
}

defineExpose({ queue });
</script>

<template>
  <div
    v-if="head"
    class="pointer-events-none fixed inset-x-0 bottom-4 z-50 flex justify-center"
    role="alertdialog"
    aria-label="Request failed after key rotation"
    aria-modal="false"
    data-testid="retry-after-rotate-toast"
  >
    <div
      class="pointer-events-auto flex max-w-md flex-col gap-2 rounded-md border border-signal-warning bg-surface-1 p-3 shadow-lg"
    >
      <div class="font-ui text-[11px] uppercase tracking-[0.18em] text-signal-warning">
        Request Failed
      </div>
      <div class="font-ui text-sm text-ink" data-testid="retry-after-rotate-body">
        Your key works, but the request still failed. Resend your message to
        try again.
      </div>

      <div v-if="showDetail && head.error_message" class="font-ui text-[11px] text-ink-muted">
        {{ head.error_message }}
      </div>

      <div class="mt-1 flex justify-end gap-2">
        <button
          v-if="head.error_message"
          type="button"
          class="font-ui text-xs text-ink-link hover:underline"
          data-testid="retry-after-rotate-detail"
          @click="toggleDetail"
        >
          {{ showDetail ? 'Hide details' : 'View details' }}
        </button>
        <button
          type="button"
          class="font-ui text-xs text-ink-link hover:underline"
          data-testid="retry-after-rotate-dismiss"
          @click="dismiss"
        >
          Dismiss
        </button>
      </div>
    </div>
  </div>
</template>
