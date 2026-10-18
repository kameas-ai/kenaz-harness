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

import { computed, ref, watch } from 'vue';
import { useEventStream } from '@/lib/useEventStream';
import { useHarnessClient } from '@/lib/harnessClientContext';
import type { ElicitRequest } from '@/lib/types';
import DeferredAskPanel from './DeferredAskPanel.vue';

const props = defineProps<{
  /**
   * Scopes the pill to one session's deferred asks (automation-
   * actually-runs-01PMZ404 UNIT-15). "elicit:deferred" is a process-
   * wide broker topic — every session's deferred asks fire on it — so
   * without this a chat header mounted for session A would show
   * session B's pending questions too. Optional and unfiltered when
   * omitted, matching this component's pre-UNIT-15 behavior (its
   * existing spec mounts it standalone with no session context).
   */
  sessionId?: string;
}>();

const client = useHarnessClient();

// ── deferred ask queue ─────────────────────────────────────────────────────

const allPending = ref<ElicitRequest[]>([]);

useEventStream<ElicitRequest>('elicit:deferred', (payload) => {
  if (!payload?.request_id) return;
  if (allPending.value.some((p) => p.request_id === payload.request_id)) return;
  allPending.value = [...allPending.value, payload];
});

useEventStream<{ ask_id: string }>('elicit:deferred:answered', (payload) => {
  if (!payload?.ask_id) return;
  allPending.value = allPending.value.filter((p) => p.request_id !== payload.ask_id);
});

// Session-scoped view. Falls back to the full queue when no sessionId
// was supplied (see the prop doc above).
const pending = computed<ElicitRequest[]>(() =>
  props.sessionId === undefined
    ? allPending.value
    : allPending.value.filter((p) => p.session_id === props.sessionId),
);

const panelOpen = ref(false);

const pendingCount = computed(() => pending.value.length);

watch(pendingCount, (count) => {
  if (count === 0) panelOpen.value = false;
});

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
