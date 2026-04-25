<script setup lang="ts">
/**
 * ChatInput — multiline composition surface.
 *
 *   - Enter sends, Shift+Enter inserts a newline.
 *   - Disabled while a stream is in-flight (`streaming` prop).
 *   - Cancel button replaces Send while streaming; clicking emits
 *     `cancel` so the parent can call `client.llm.stopStream(subId)`.
 *   - Token-count + cost estimate are placeholder reads of `estimate`;
 *     the providers mission wires real-time accounting later.
 *
 * Accessibility: textarea owns `aria-label`; the live region announces
 * the streaming state without being chatty.
 */

import { computed, ref, watch } from 'vue';
import type { CostEstimate } from '@/lib/types';

const props = defineProps<{
  modelValue?: string;
  streaming?: boolean;
  estimate?: CostEstimate | null;
  placeholder?: string;
  /** Optional disabled override (e.g. no-provider state). */
  disabled?: boolean;
}>();

const emit = defineEmits<{
  (e: 'update:modelValue', value: string): void;
  (e: 'send', value: string): void;
  (e: 'cancel'): void;
}>();

const internal = ref(props.modelValue ?? '');
watch(
  () => props.modelValue,
  (v) => {
    if (typeof v === 'string' && v !== internal.value) internal.value = v;
  },
);

const isDisabled = computed(() => props.disabled === true || props.streaming === true);
const trimmed = computed(() => internal.value.trim());
const canSend = computed(() => trimmed.value.length > 0 && !isDisabled.value);

const tokenLabel = computed(() => {
  const n = props.estimate?.tokens ?? 0;
  return `${n.toLocaleString()} tok`;
});
const costLabel = computed(() => {
  const usd = props.estimate?.usd ?? 0;
  return `$${usd.toFixed(4)}`;
});

function onInput(ev: Event) {
  const t = ev.target as HTMLTextAreaElement;
  internal.value = t.value;
  emit('update:modelValue', t.value);
}

function send() {
  if (!canSend.value) return;
  const value = trimmed.value;
  internal.value = '';
  emit('update:modelValue', '');
  emit('send', value);
}

function onKeydown(ev: KeyboardEvent) {
  if (ev.key === 'Enter' && !ev.shiftKey) {
    ev.preventDefault();
    send();
  }
}

function cancel() {
  emit('cancel');
}
</script>

<template>
  <form
    class="border-t border-border-muted bg-surface-1 px-4 py-3"
    role="group"
    aria-label="Compose message"
    @submit.prevent="send"
  >
    <label class="sr-only" for="chat-input">Message</label>
    <textarea
      id="chat-input"
      :value="internal"
      :disabled="isDisabled"
      :placeholder="placeholder ?? 'Send a message — Enter to send, Shift+Enter for newline'"
      class="block w-full resize-none bg-surface-2 text-ink font-ui text-sm leading-relaxed rounded-md border border-border-muted px-3 py-2 outline-none focus:border-accent disabled:opacity-60 disabled:cursor-not-allowed"
      rows="3"
      aria-label="Message"
      aria-multiline="true"
      @input="onInput"
      @keydown="onKeydown"
    ></textarea>
    <div class="mt-2 flex items-center justify-between font-mono text-[11px] text-ink-subtle">
      <div aria-live="polite">
        <span>{{ tokenLabel }}</span>
        <span aria-hidden="true" class="px-2 text-ink-dim">·</span>
        <span>{{ costLabel }}</span>
        <span
          v-if="streaming"
          class="ml-3 text-accent uppercase tracking-[0.18em]"
          aria-live="polite"
        >
          streaming…
        </span>
      </div>
      <div class="flex items-center gap-2">
        <button
          v-if="streaming"
          type="button"
          class="px-3 py-1 rounded-md border border-signal-danger text-signal-danger font-ui text-[11px] uppercase tracking-[0.18em] hover:bg-surface-2"
          aria-label="Cancel stream"
          @click="cancel"
        >
          cancel
        </button>
        <button
          v-else
          type="submit"
          class="px-3 py-1 rounded-md border border-accent text-accent font-ui text-[11px] uppercase tracking-[0.18em] hover:bg-surface-2 disabled:opacity-50 disabled:cursor-not-allowed"
          :disabled="!canSend"
          aria-label="Send message"
        >
          send
        </button>
      </div>
    </div>
  </form>
</template>
