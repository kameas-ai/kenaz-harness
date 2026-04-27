<script setup lang="ts">
/**
 * MergeSuggestionToast — surfaces the kernel's "merge?" suggestion
 * stream. The backend emits a `branches:merge-suggested` event with a
 * { branchId, reason, detail } payload when its heuristic fires
 * (terminal-state token, child run complete, idle timeout). The user
 * can accept (runs the merge), dismiss (acknowledges this fire), or
 * keep working (pauses suggestions for the branch).
 *
 * The component is mounted at the SessionsView root so a single
 * instance handles every suggestion across the chat surface.
 */

import { computed, ref } from 'vue';
import { useEventStream } from '@/lib/useEventStream';
import { useHarnessClient } from '@/lib/harnessClientContext';

interface MergeSuggestionPayload {
  branchId: string;
  reason: string;
  detail?: string;
}

const props = defineProps<{
  // Optional manual trigger for tests + integration scenarios that
  // pre-seed a suggestion without a backend event.
  initial?: MergeSuggestionPayload | null;
}>();

const emit = defineEmits<{
  merged: [branchId: string];
  dismissed: [branchId: string];
}>();

const client = useHarnessClient();

const queue = ref<MergeSuggestionPayload[]>(props.initial ? [props.initial] : []);
const submitting = ref(false);
const lastError = ref<string | null>(null);

useEventStream<MergeSuggestionPayload>('branches:merge-suggested', (payload) => {
  if (!payload || !payload.branchId) return;
  if (queue.value.some((q) => q.branchId === payload.branchId)) return;
  queue.value = [...queue.value, payload];
});

const head = computed<MergeSuggestionPayload | null>(() => queue.value[0] ?? null);

function popHead() {
  queue.value = queue.value.slice(1);
}

async function acceptMerge() {
  const current = head.value;
  if (!current || submitting.value) return;
  submitting.value = true;
  lastError.value = null;
  try {
    await client.branches.merge(current.branchId);
    emit('merged', current.branchId);
    popHead();
  } catch (err) {
    lastError.value = err instanceof Error ? err.message : String(err);
  } finally {
    submitting.value = false;
  }
}

function dismiss() {
  const current = head.value;
  if (!current) return;
  emit('dismissed', current.branchId);
  popHead();
}

function reasonLabel(reason: string) {
  switch (reason) {
    case 'terminal_state_token':
      return 'Branch reply looks terminal';
    case 'child_run_complete':
      return 'Branch run finished';
    case 'idle_timeout':
      return 'Branch has been idle';
    default:
      return reason;
  }
}

defineExpose({ queue });
</script>

<template>
  <div
    v-if="head"
    class="pointer-events-none fixed inset-x-0 bottom-4 z-50 flex justify-center"
    role="status"
    aria-live="polite"
    data-testid="merge-suggestion-toast"
  >
    <div
      class="pointer-events-auto flex max-w-md flex-col gap-2 rounded-md border border-accent-hairline bg-surface-1 p-3 shadow-lg"
    >
      <div class="font-ui text-[11px] uppercase tracking-[0.18em] text-ink-subtle">
        Merge suggested
      </div>
      <div class="font-ui text-sm text-ink">
        {{ reasonLabel(head.reason) }}
      </div>
      <div v-if="head.detail" class="font-ui text-xs text-ink-subtle">
        {{ head.detail }}
      </div>
      <div
        v-if="lastError"
        class="font-ui text-xs text-status-error"
        data-testid="merge-suggestion-error"
      >
        {{ lastError }}
      </div>
      <div class="mt-1 flex justify-end gap-2">
        <button
          type="button"
          class="font-ui text-xs text-ink-link hover:underline"
          data-testid="merge-suggestion-keep"
          @click="dismiss"
        >
          Keep working
        </button>
        <button
          type="button"
          class="font-ui text-xs text-ink-link hover:underline"
          :disabled="submitting"
          data-testid="merge-suggestion-merge"
          @click="acceptMerge"
        >
          Merge now
        </button>
      </div>
    </div>
  </div>
</template>
