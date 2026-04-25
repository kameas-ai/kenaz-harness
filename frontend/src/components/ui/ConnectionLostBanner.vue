<script setup lang="ts">
import { ref } from 'vue';
import { setConnectionState } from '@/lib/useConnectionState';

/**
 * ConnectionLostBanner — single dismissable, non-toasting banner (FR-013).
 * Shown only when useConnectionState() === 'lost'. Retry pokes the
 * bridge by transitioning state back to 'connecting'.
 */
const dismissed = ref(false);

function retry() {
  setConnectionState('connecting');
  dismissed.value = false;
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
