<script setup lang="ts">
/**
 * MessageBubble — single-message render primitive.
 *
 * Visual register (FR-001b, plan §5.2):
 *   - User bubble  : right-aligned, surface-2 background, sans body.
 *   - Assistant    : left-aligned, surface-1 background, sans body. While
 *                    streaming a brass hairline glows along the leading
 *                    edge — brass strictly reserved for live state per
 *                    FR-011 / privacy CI invariant #4.
 *   - System       : centred, italic, ink-muted.
 *   - Tool         : monospace EventStreamRow-style line — namespaced.
 *
 * Markdown rendering is the deliberately-narrow inline subset (no HTML,
 * no images): the textual content flows through a `whitespace-pre-wrap`
 * block via StreamingText. Future missions extend with a vetted markdown
 * pipeline once the policy mission ratifies a sanitiser.
 */

import { computed } from 'vue';
import StreamingText from './StreamingText.vue';
import type { MessageRole, ToolCall } from '@/lib/types';

const props = defineProps<{
  role: MessageRole;
  content: string;
  streaming?: boolean;
  toolCalls?: readonly ToolCall[];
}>();

const isUser = computed(() => props.role === 'user');
const isAssistant = computed(() => props.role === 'assistant');
const isSystem = computed(() => props.role === 'system');
const isTool = computed(() => props.role === 'tool');

const wrapperClass = computed(() => {
  if (isUser.value) return 'flex justify-end';
  if (isSystem.value) return 'flex justify-center';
  if (isTool.value) return 'flex justify-start';
  return 'flex justify-start';
});

const bubbleClass = computed(() => {
  const base = 'max-w-[78ch] px-4 py-3 rounded-md font-ui text-sm leading-relaxed';
  if (isUser.value) {
    return `${base} bg-surface-3 text-ink border border-border-muted`;
  }
  if (isAssistant.value) {
    const live = props.streaming
      ? 'border-accent shadow-[0_0_0_1px_var(--accent-glow)]'
      : 'border-border-muted';
    return `${base} bg-surface-1 text-ink border ${live}`;
  }
  if (isSystem.value) {
    return `${base} bg-transparent text-ink-muted italic`;
  }
  // tool — monospace EventStreamRow-style
  return 'font-mono text-[12px] text-ink-muted px-3 py-1';
});

const roleLabel = computed(() => props.role.toUpperCase());
</script>

<template>
  <article
    :class="wrapperClass"
    :role="isTool ? 'log' : 'article'"
    :aria-label="`${roleLabel} message`"
  >
    <div :class="bubbleClass">
      <header
        v-if="!isTool"
        class="font-ui text-[10px] uppercase tracking-[0.18em] mb-1"
        :class="isAssistant && streaming ? 'text-accent' : 'text-ink-subtle'"
      >
        {{ roleLabel }}
        <span v-if="isAssistant && streaming" class="ml-2">live</span>
      </header>

      <!-- tool-call rows: namespaced monospace line each -->
      <ul v-if="toolCalls && toolCalls.length > 0" class="space-y-1 mb-2">
        <li
          v-for="tc in toolCalls"
          :key="tc.id"
          class="font-mono text-[12px] grid items-baseline gap-3"
          style="grid-template-columns: 14ch 1fr auto"
        >
          <span class="uppercase tracking-[0.12em] text-[11px] text-accent">
            tool · {{ tc.name }}
          </span>
          <span class="text-ink truncate">{{ tc.argsSummary }}</span>
          <span v-if="tc.latency" class="text-ink-subtle text-right">
            {{ tc.latency }}
          </span>
        </li>
      </ul>

      <StreamingText
        v-if="!isTool"
        :text="content"
        :streaming="streaming === true"
      />
      <span v-else class="font-mono text-[12px] text-ink-muted">
        {{ content }}
      </span>
    </div>
  </article>
</template>
