<script setup lang="ts">
/**
 * DeferredAskPanel — expanded floating panel for answering deferred asks (WP06).
 *
 * Renders when the user clicks the DeferredAskPill. Shows the head of the
 * deferred ask queue as a non-modal panel (does not block the rest of the UI).
 * On answer the parent calls client.elicit.answerDeferred; the backend queues
 * a system_reminder for the next LLM turn.
 */

import { computed, ref } from 'vue';
import type { ElicitRequest } from '@/lib/types';

const props = defineProps<{
  asks: ElicitRequest[];
}>();

const emit = defineEmits<{
  (e: 'answered', askID: string, answer: unknown): void;
  (e: 'close'): void;
}>();

const head = computed<ElicitRequest | null>(() => props.asks[0] ?? null);
const answer = ref<unknown>(null);
const submitting = ref(false);

async function submit() {
  if (!head.value || submitting.value) return;
  submitting.value = true;
  try {
    emit('answered', head.value.request_id, answer.value);
    answer.value = null;
  } finally {
    submitting.value = false;
  }
}

function dismiss() {
  emit('close');
}
</script>

<template>
  <div
    v-if="head"
    class="absolute right-0 top-10 z-50 w-80 rounded-md border border-accent-hairline bg-surface-1 p-4 shadow-xl"
    role="dialog"
    aria-label="Pending question from model"
    data-testid="deferred-ask-panel"
  >
    <!-- Header -->
    <div class="flex items-center justify-between">
      <span class="font-ui text-[11px] uppercase tracking-[0.18em] text-ink-subtle">
        Pending question
      </span>
      <button
        type="button"
        class="font-ui text-[11px] text-ink-muted hover:text-ink"
        aria-label="Close pending question panel"
        data-testid="deferred-panel-close"
        @click="dismiss"
      >
        ✕
      </button>
    </div>

    <!-- Question -->
    <p class="mt-2 font-ui text-sm text-ink" data-testid="deferred-panel-question">
      {{ head.question }}
    </p>

    <!-- Simple text answer for deferred (full kind support via AskUserQuestionDialog) -->
    <textarea
      v-model="answer as string"
      class="mt-3 w-full rounded-sm border border-border-muted bg-surface-2 p-2 font-ui text-sm text-ink focus:outline-none focus:ring-1 focus:ring-accent"
      rows="3"
      placeholder="Your answer…"
      data-testid="deferred-panel-answer"
    />

    <!-- Actions -->
    <div class="mt-3 flex gap-2">
      <button
        type="button"
        class="rounded-md border border-accent bg-accent/10 px-3 py-1.5 font-ui text-[11px] text-accent hover:bg-accent/20 disabled:opacity-50"
        :disabled="submitting"
        data-testid="deferred-panel-submit"
        @click="submit"
      >
        Answer
      </button>
      <button
        type="button"
        class="rounded-md border border-border-muted px-3 py-1.5 font-ui text-[11px] text-ink-muted hover:bg-surface-2"
        data-testid="deferred-panel-skip"
        @click="dismiss"
      >
        Later
      </button>
    </div>

    <!-- Queue overflow badge -->
    <div
      v-if="asks.length > 1"
      class="mt-2 font-ui text-[10px] text-ink-subtle"
      data-testid="deferred-panel-queue"
    >
      +{{ asks.length - 1 }} more
    </div>
  </div>
</template>
