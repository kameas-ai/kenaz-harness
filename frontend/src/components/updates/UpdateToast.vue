<script setup lang="ts">
/**
 * UpdateToast — the *one* notification we ever raise for an update.
 *
 * Fires the first time a given availableVersion is observed in this browser
 * session, then stays silent until the user restarts the harness AND a newer
 * version appears. Auto-dismisses after 5s. Lower-right corner so it doesn't
 * stomp the chat surface or the upper-right indicator.
 *
 * If the user has already skipped the version, no toast — that's the point of
 * Skip. The store handles that filtering via `shouldToast`.
 */

import { ref, watch, onBeforeUnmount } from 'vue';
import { useUpdateStore } from './useUpdateStore';

const store = useUpdateStore();
const visible = ref(false);
const versionLabel = ref('');
let dismissTimer: ReturnType<typeof setTimeout> | null = null;

function clearTimer() {
  if (dismissTimer) {
    clearTimeout(dismissTimer);
    dismissTimer = null;
  }
}

function dismiss() {
  visible.value = false;
  clearTimer();
}

watch(
  () => store.shouldToast.value,
  (should) => {
    if (!should) return;
    const ver = store.status.value?.availableVersion;
    if (!ver) return;
    versionLabel.value = ver;
    visible.value = true;
    store.markToasted(ver);
    clearTimer();
    dismissTimer = setTimeout(() => {
      visible.value = false;
      dismissTimer = null;
    }, 5000);
  },
  { immediate: true },
);

onBeforeUnmount(() => {
  clearTimer();
});
</script>

<template>
  <div
    v-if="visible"
    class="pointer-events-none fixed bottom-4 right-4 z-40 flex justify-end"
    role="status"
    aria-live="polite"
    data-testid="update-toast"
  >
    <div
      class="pointer-events-auto flex max-w-sm items-center gap-3 rounded-md border border-accent-hairline bg-surface-1 px-3 py-2 shadow-lg"
    >
      <span
        class="block h-2 w-2 flex-shrink-0 rounded-full bg-accent"
        aria-hidden="true"
      />
      <div class="font-ui text-xs text-ink">
        Kenaz {{ versionLabel }} is available — click
        <span class="font-mono text-[11px] text-accent">⬆</span> to install.
      </div>
      <button
        type="button"
        class="font-ui text-[11px] text-ink-subtle hover:text-ink"
        aria-label="Dismiss notification"
        data-testid="update-toast-dismiss"
        @click="dismiss"
      >
        ×
      </button>
    </div>
  </div>
</template>
