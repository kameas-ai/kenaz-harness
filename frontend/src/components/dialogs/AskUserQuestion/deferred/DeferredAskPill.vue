<script setup lang="ts">
/**
 * DeferredAskPill — chat-header pill for pending deferred asks (WP06).
 *
 * Shown in the chat header when there are in-flight deferred asks for the
 * current session. Clicking the pill expands DeferredAskPanel.vue where
 * the user can answer.
 *
 * Wire flow:
 *   1. Backend emits "elicit:deferred" when the model registers a deferred ask.
 *   2. This component queues the request.
 *   3. User clicks pill → DeferredAskPanel renders the question.
 *   4. User answers → client.elicit.answerDeferred → "elicit:deferred:answered".
 *   5. Backend injects system_reminder on the next LLM turn.
 */

import { computed, ref } from 'vue';
import { useEventStream } from '@/lib/useEventStream';
import { useHarnessClient } from '@/lib/harnessClientContext';
import type { ElicitRequest } from '@/lib/types';
import DeferredAskPanel from './DeferredAskPanel.vue';

const client = useHarnessClient();

// ── deferred ask queue ─────────────────────────────────────────────────────

const pending = ref<ElicitRequest[]>([]);
const panelOpen = ref(false);

useEventStream<ElicitRequest>('elicit:deferred', (payload) => {
  if (!payload?.request_id) return;
  if (pending.value.some((p) => p.request_id === payload.request_id)) return;
  pending.value = [...pending.value, payload];
});

useEventStream<{ ask_id: string }>('elicit:deferred:answered', (payload) => {
  if (!payload?.ask_id) return;
  pending.value = pending.value.filter((p) => p.request_id !== payload.ask_id);
  if (pending.value.length === 0) panelOpen.value = false;
});

const pendingCount = computed(() => pending.value.length);

function togglePanel() {
  panelOpen.value = !panelOpen.value;
}

async function onAnswered(askID: string, answer: unknown) {
  await client.elicit.answerDeferred(askID, answer);
}

defineExpose({ pending, pendingCount, panelOpen });
</script>

<template>
  <div v-if="pendingCount > 0" class="relative" data-testid="deferred-ask-pill-container">
    <!-- Pill button -->
    <button
      type="button"
      class="flex items-center gap-1 rounded-full border border-accent bg-accent/10 px-3 py-1 font-ui text-[11px] text-accent hover:bg-accent/20"
      :aria-label="`${pendingCount} pending question${pendingCount > 1 ? 's' : ''} from model`"
      data-testid="deferred-ask-pill"
      @click="togglePanel"
    >
      <span class="h-2 w-2 animate-pulse rounded-full bg-accent" />
      {{ pendingCount }}
    </button>

    <!-- Expanded panel -->
    <DeferredAskPanel
      v-if="panelOpen"
      :asks="pending"
      @answered="onAnswered"
      @close="panelOpen = false"
    />
  </div>
</template>
