<script setup lang="ts">
/**
 * ToastRoot — fixed-position toast stack mounted near the App root.
 *
 * Sources:
 *   1. Direct push via `useToastQueue().push(...)` (in-tree callers).
 *   2. `md-toast` CustomEvent dispatched on `document` (CodeBlock and
 *      other components mounted via `createApp(...).mount(host)` —
 *      they are outside the Vue tree and can't import the composable
 *      reactive state, so they fire an event instead).
 *
 * Visuals stay deliberately neutral: a small, low-contrast pill with
 * the toast message and an optional Undo link. Auto-dismiss after 5s.
 */
import { onBeforeUnmount, onMounted } from 'vue';
import { push, dismiss, invokeUndo, useToastQueue } from '@/composables/useToastQueue';

const { toasts } = useToastQueue();

interface MdToastDetail {
  message: string;
  undoFn?: () => void | Promise<void>;
}

function onMdToast(evt: Event) {
  const detail = (evt as CustomEvent<MdToastDetail>).detail;
  if (!detail || typeof detail.message !== 'string') return;
  push(detail.message, { undoFn: detail.undoFn });
}

onMounted(() => {
  document.addEventListener('md-toast', onMdToast as EventListener);
});

onBeforeUnmount(() => {
  document.removeEventListener('md-toast', onMdToast as EventListener);
});
</script>

<template>
  <div class="toast-root" aria-live="polite" aria-atomic="false">
    <div
      v-for="t in toasts"
      :key="t.id"
      class="toast"
      :data-testid="`toast-${t.id}`"
      role="status"
    >
      <span class="toast-msg">{{ t.message }}</span>
      <button
        v-if="t.undoFn"
        type="button"
        class="toast-undo"
        :data-testid="`toast-undo-${t.id}`"
        @click="invokeUndo(t.id)"
      >
        Undo
      </button>
      <button
        type="button"
        class="toast-close"
        aria-label="Dismiss"
        :data-testid="`toast-close-${t.id}`"
        @click="dismiss(t.id)"
      >
        ×
      </button>
    </div>
  </div>
</template>

<style scoped>
.toast-root {
  position: fixed;
  bottom: 1rem;
  right: 1rem;
  display: flex;
  flex-direction: column;
  gap: 0.5rem;
  z-index: 9999;
  pointer-events: none;
}

.toast {
  pointer-events: auto;
  display: inline-flex;
  align-items: center;
  gap: 0.6rem;
  padding: 0.45rem 0.7rem;
  background: var(--surface-3);
  border: 1px solid var(--border-muted);
  border-radius: var(--radius-sm);
  font-family: var(--font-ui);
  font-size: 12px;
  color: var(--ink);
  box-shadow: var(--shadow-elevated, 0 2px 8px var(--shadow-color, transparent));
  max-width: 28rem;
}

.toast-msg {
  flex: 1 1 auto;
  overflow: hidden;
  text-overflow: ellipsis;
}

.toast-undo {
  background: transparent;
  border: 0;
  color: var(--accent);
  font-family: inherit;
  font-size: inherit;
  cursor: pointer;
  padding: 0 0.25rem;
}

.toast-undo:hover {
  text-decoration: underline;
}

.toast-close {
  background: transparent;
  border: 0;
  color: var(--ink-muted);
  font-family: inherit;
  font-size: 14px;
  line-height: 1;
  cursor: pointer;
  padding: 0 0.25rem;
}

.toast-close:hover {
  color: var(--ink);
}
</style>
