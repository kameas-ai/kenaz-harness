<script setup lang="ts">
import { ref } from 'vue';
import { setConnectionState } from '@/lib/useConnectionState';
import { retryReconnectNow } from '@/lib/servedTransport';

/**
 * ConnectionLostBanner — single dismissable, non-toasting banner (FR-013).
 * Shown only when useConnectionState() === 'lost'. Retry transitions the
 * state to 'connecting' AND fires an immediate served-mode reconnect probe
 * (bypassing the backoff timer) so the user's click actually pokes the
 * backend instead of waiting for the next scheduled attempt. retryReconnectNow
 * is a no-op in native mode / when no reconnect target has been recorded.
 */
const dismissed = ref(false);

function retry() {
  setConnectionState('connecting');
  dismissed.value = false;
  // Trigger an immediate reconnect attempt rather than passively waiting for
  // the backoff poller's next tick.
  retryReconnectNow();
}

function dismiss() {
  dismissed.value = true;
}
</script>

<template>
  <div
    v-if="!dismissed"
    class="px-4 py-2 bg-surface-2 border-b border-signal-danger flex items-center justify-between gap-3"
    role="alert"
  >
    <span class="font-ui text-sm text-ink">
      Connection to the harness backend lost.
    </span>
    <div class="flex items-center gap-2">
      <button
        type="button"
        class="font-ui text-xs text-accent hover:text-ink px-2 py-1 rounded-sm"
        @click="retry"
      >
        Retry
      </button>
      <button
        type="button"
        class="font-ui text-xs text-ink-muted hover:text-ink px-2 py-1 rounded-sm"
        @click="dismiss"
      >
        Dismiss
      </button>
    </div>
  </div>
</template>
