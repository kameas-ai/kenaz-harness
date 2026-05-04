<script setup lang="ts">
/**
 * SessionHeader — WP05 suggest-title affordance.
 *
 * Renders the session name and a "Suggest new title" button.
 * When the session already has a user-set title (name is non-empty and
 * autoTitled is falsy), clicking the button first shows a confirm modal
 * before calling Sessions_SuggestTitle. Auto-titled sessions get
 * re-titled immediately without a modal (the title was machine-chosen,
 * so there's nothing to protect).
 */
import { ref } from 'vue';
import { useHarnessClient } from '@/lib/useHarnessAPI';
import type { Session } from '@/lib/types';

const props = defineProps<{
  session: Session;
}>();

const emit = defineEmits<{
  'title-changed': [];
}>();

const client = useHarnessClient();

const suggesting = ref(false);
const confirmOpen = ref(false);
const lastError = ref<string | null>(null);

/** True when the session has a user-set title that needs confirmation before overwriting. */
function needsConfirm(): boolean {
  return Boolean(props.session.name) && !props.session.autoTitled;
}

async function onSuggestClick() {
  lastError.value = null;
  if (needsConfirm()) {
    confirmOpen.value = true;
    return;
  }
  await doSuggest();
}

async function doSuggest() {
  confirmOpen.value = false;
  if (suggesting.value) return;
  suggesting.value = true;
  try {
    await client.sessions.suggestTitle(props.session.id);
    emit('title-changed');
  } catch (err) {
    lastError.value = err instanceof Error ? err.message : String(err);
  } finally {
    suggesting.value = false;
  }
}

function cancelConfirm() {
  confirmOpen.value = false;
}
</script>

<template>
  <header
    data-testid="session-header"
    class="flex items-center gap-2 px-4 py-2 border-b border-hairline"
  >
    <span class="flex-1 truncate text-sm font-medium text-ink">
      {{ session.name || session.id }}
    </span>

    <button
      type="button"
      data-testid="suggest-title-btn"
      class="shrink-0 text-xs px-2 py-1 rounded text-ink-muted hover:text-ink hover:bg-surface-2 transition-colors"
      :disabled="suggesting"
      @click="onSuggestClick"
    >
      Suggest new title
    </button>

    <p
      v-if="lastError"
      class="w-full text-xs text-signal-danger mt-1"
    >
      {{ lastError }}
    </p>

    <!-- Confirm-overwrite modal — shown only when session has a user-set title -->
    <div
      v-if="confirmOpen"
      data-testid="suggest-title-confirm-modal"
      class="fixed inset-0 z-50 flex items-center justify-center bg-black/40"
      role="dialog"
      aria-modal="true"
    >
      <div class="bg-surface-1 rounded-lg shadow-xl p-6 max-w-sm w-full mx-4 flex flex-col gap-4">
        <p class="text-sm text-ink">
          This will replace the current title with an auto-generated one. Continue?
        </p>
        <div class="flex justify-end gap-2">
          <button
            type="button"
            data-testid="suggest-title-confirm-cancel"
            class="px-3 py-1.5 text-sm rounded text-ink-muted hover:bg-surface-2"
            @click="cancelConfirm"
          >
            Cancel
          </button>
          <button
            type="button"
            data-testid="suggest-title-confirm-ok"
            class="px-3 py-1.5 text-sm rounded bg-accent text-white hover:bg-accent/90"
            @click="doSuggest"
          >
            Replace title
          </button>
        </div>
      </div>
    </div>
  </header>
</template>
