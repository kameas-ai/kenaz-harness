<script setup lang="ts">
/**
 * MessageList — scrollable list of MessageBubbles.
 *
 * Behaviour (mirrors Kenaz visual register + WP12 useStream conventions):
 *   - Auto-scrolls to the bottom on new message *unless* the user has
 *     scrolled up — in which case we surface a subtle "new" pill.
 *   - Tracks the last scrollTop so a user scroll-up sticks; we only
 *     resume auto-stick when the user returns within ~32px of the bottom.
 *   - The "virtualization" here is keyed re-rendering only: for v1 chat
 *     surfaces the message count stays well under the threshold where
 *     window-virtualization pays off. The real virtualizer lands when
 *     long-context replays (>1k messages) become a thing.
 */

import { computed, nextTick, onMounted, ref, watch } from 'vue';
import MessageBubble from './MessageBubble.vue';
import type { Message } from '@/lib/types';

const props = defineProps<{
  messages: ReadonlyArray<Message>;
  /**
   * Optional in-flight assistant message. Rendered after `messages` with
   * `streaming` flag forced on. The chat surface owns the buffer so this
   * component stays presentational.
   */
  streamingMessage?: Message | null;
  /**
   * True while a stream is open but no chunks have arrived yet — render
   * a small "thinking…" indicator so the user sees the system is alive.
   */
  waiting?: boolean;
  /**
   * Inline error to render at the bottom of the conversation. Useful
   * when send/startStream fails — the error must surface in the chat
   * itself, not just the page subtitle.
   */
  errorMessage?: string | null;
}>();

const scrollEl = ref<HTMLElement | null>(null);
const stickToBottom = ref(true);
const newPillVisible = ref(false);

const allMessages = computed<ReadonlyArray<Message>>(() => {
  if (props.streamingMessage) {
    return [...props.messages, props.streamingMessage];
  }
  return props.messages;
});

function isNearBottom(el: HTMLElement, threshold = 32): boolean {
  return el.scrollHeight - el.scrollTop - el.clientHeight <= threshold;
}

function scrollToBottom() {
  const el = scrollEl.value;
  if (!el) return;
  el.scrollTop = el.scrollHeight;
  newPillVisible.value = false;
  stickToBottom.value = true;
}

function onScroll() {
  const el = scrollEl.value;
  if (!el) return;
  if (isNearBottom(el)) {
    stickToBottom.value = true;
    newPillVisible.value = false;
  } else {
    stickToBottom.value = false;
  }
}

onMounted(() => {
  void nextTick(scrollToBottom);
});

watch(
  () => allMessages.value.length,
  async () => {
    await nextTick();
    if (stickToBottom.value) {
      scrollToBottom();
    } else if (props.streamingMessage) {
      newPillVisible.value = true;
    }
  },
);

watch(
  () => props.streamingMessage?.content,
  async () => {
    await nextTick();
    if (stickToBottom.value) scrollToBottom();
    else if (props.streamingMessage) newPillVisible.value = true;
  },
);

defineExpose({ scrollToBottom });
</script>

<template>
  <div class="relative flex flex-col h-full">
    <div
      ref="scrollEl"
      class="flex-1 overflow-y-auto px-4 py-3 space-y-3"
      role="log"
      aria-live="polite"
      aria-relevant="additions"
      aria-label="Conversation messages"
      @scroll="onScroll"
    >
      <div
        v-if="allMessages.length === 0"
        class="grid place-items-center h-full font-ui text-sm text-ink-subtle"
      >
        No messages yet — start the conversation below.
      </div>
      <MessageBubble
        v-for="m in allMessages"
        :key="m.id"
        :role="m.role"
        :content="m.content"
        :streaming="m.streaming === true"
        :tool-calls="m.toolCalls"
      />

      <!-- Thinking indicator: visible from the moment send() opens a
           stream until the first chunk arrives. -->
      <div
        v-if="waiting"
        class="flex items-center gap-2 font-ui text-[12px] text-ink-muted"
        role="status"
        aria-live="polite"
      >
        <span class="thinking-dots" aria-hidden="true">
          <span class="dot"></span>
          <span class="dot"></span>
          <span class="dot"></span>
        </span>
        <span>Thinking…</span>
      </div>

      <!-- Inline error from useSession.error.value when send fails. -->
      <div
        v-if="errorMessage"
        class="rounded-md border border-signal-danger bg-surface-1 px-3 py-2 font-ui text-[12px] text-signal-danger"
        role="alert"
      >
        <div class="text-[10px] uppercase tracking-[0.18em] text-signal-danger">
          Send failed
        </div>
        <p class="mt-1 break-words text-ink">{{ errorMessage }}</p>
      </div>
    </div>

    <button
      v-if="newPillVisible"
      type="button"
      class="absolute bottom-3 left-1/2 -translate-x-1/2 px-3 py-1 rounded-md border border-accent bg-surface-2 text-accent font-ui text-[11px] uppercase tracking-[0.18em] hover:bg-surface-3"
      aria-label="Scroll to newest message"
      @click="scrollToBottom"
    >
      new ↓
    </button>
  </div>
</template>

<style scoped>
.thinking-dots {
  display: inline-flex;
  gap: 3px;
}
.thinking-dots .dot {
  width: 5px;
  height: 5px;
  border-radius: 50%;
  background: currentColor;
  opacity: 0.4;
  animation: thinking-bounce 1.2s infinite ease-in-out;
}
.thinking-dots .dot:nth-child(2) { animation-delay: 0.15s; }
.thinking-dots .dot:nth-child(3) { animation-delay: 0.3s; }

@keyframes thinking-bounce {
  0%, 60%, 100% { transform: translateY(0); opacity: 0.4; }
  30% { transform: translateY(-3px); opacity: 1; }
}
</style>
