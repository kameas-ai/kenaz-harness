<script setup lang="ts">
/**
 * TodoChip — compact pill that appears in a chat message bubble when
 * the model invoked kenaz__todo_write.
 *
 * Shows a summary line ("N tasks · X done") and emits `open` when
 * clicked so the parent (MessageBubble) can mount the TodoSidePanel.
 *
 * The chip is intentionally presentational: it receives the TodoItem
 * list as a prop and does not call any RPC itself. The parent owns
 * the TodoSidePanel lifecycle.
 *
 * Privacy CI invariant #4: no raw colour literals — all colours
 * resolve through design tokens.
 * (builtin-tools-search-and-elicitation-01KZNP3D WP06)
 */

import { computed } from 'vue';
import { CheckSquare } from '@/shell/icons';

export interface TodoItem {
  id: string;
  content: string;
  status: 'pending' | 'in_progress' | 'done' | 'cancelled';
  priority?: 'high' | 'medium' | 'low';
}

const props = defineProps<{
  /** The full todo list from the tool call result. */
  todos: TodoItem[];
}>();

const emit = defineEmits<{
  (e: 'open', todos: TodoItem[]): void;
}>();

const total = computed(() => props.todos.length);
const done = computed(
  () => props.todos.filter((t) => t.status === 'done').length,
);
const pending = computed(
  () =>
    props.todos.filter(
      (t) => t.status === 'pending' || t.status === 'in_progress',
    ).length,
);

const summaryLabel = computed(() => {
  if (total.value === 0) return 'todo list cleared';
  const parts: string[] = [];
  parts.push(`${total.value} task${total.value !== 1 ? 's' : ''}`);
  if (done.value > 0) parts.push(`${done.value} done`);
  if (pending.value > 0) parts.push(`${pending.value} open`);
  return parts.join(' · ');
});

function onClick() {
  emit('open', props.todos);
}
</script>

<template>
  <button
    type="button"
    class="inline-flex items-center gap-1.5 rounded-sm border border-border-muted bg-surface-1 px-2 py-1 font-ui text-[11px] text-ink-muted hover:bg-surface-2 hover:text-ink transition-fast"
    data-testid="todo-chip"
    :aria-label="`Open task list — ${summaryLabel}`"
    @click="onClick"
  >
    <CheckSquare :size="12" class="text-accent" aria-hidden="true" />
    <span class="font-mono">{{ summaryLabel }}</span>
  </button>
</template>
