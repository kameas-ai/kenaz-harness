<script setup lang="ts">
/**
 * TaskOutputViewer — streams live output for a background task.
 *
 * On mount it fetches all lines from offset 0, then polls every 500 ms
 * for new lines while the task is running. Stops polling once the task
 * reaches a terminal state.
 *
 * (background-task-monitor-01KZNP3C WP05)
 */

import { ref, computed, onMounted, onUnmounted, nextTick, watch } from 'vue';
import type { TaskRow, LineRow } from '../../lib/types';
import { tasksTail, tasksGet } from '../../lib/tasks';

const props = defineProps<{
  taskId: string;
}>();

// ── state ─────────────────────────────────────────────────────────────────

const task = ref<TaskRow | null>(null);
const lines = ref<LineRow[]>([]);
const error = ref<string | null>(null);
const loading = ref(true);
let nextOffset = 0;
let pollTimer: ReturnType<typeof setTimeout> | null = null;
const outputEl = ref<HTMLElement | null>(null);

// ── computed ──────────────────────────────────────────────────────────────

const isTerminal = computed(() => {
  const s = task.value?.status;
  return s === 'completed' || s === 'failed' || s === 'cancelled' || s === 'crashed';
});

// ── lifecycle ─────────────────────────────────────────────────────────────

async function fetchTask() {
  try {
    task.value = await tasksGet(props.taskId);
  } catch (_e) {
    // Tolerate — task may not exist yet.
  }
}

async function poll() {
  try {
    const newLines = await tasksTail(props.taskId, nextOffset);
    if (newLines.length > 0) {
      lines.value.push(...newLines);
      nextOffset = newLines[newLines.length - 1].offset + 1;
      await nextTick();
      scrollToBottom();
    }
    await fetchTask();
  } catch (err) {
    error.value = String(err);
  }
}

function schedulePoll() {
  if (pollTimer !== null) return;
  pollTimer = setTimeout(async () => {
    pollTimer = null;
    await poll();
    if (!isTerminal.value) {
      schedulePoll();
    }
  }, 500);
}

onMounted(async () => {
  await fetchTask();
  try {
    const initial = await tasksTail(props.taskId, 0);
    lines.value = initial;
    if (initial.length > 0) {
      nextOffset = initial[initial.length - 1].offset + 1;
    }
  } catch (err) {
    error.value = String(err);
  } finally {
    loading.value = false;
  }
  if (!isTerminal.value) schedulePoll();
  await nextTick();
  scrollToBottom();
});

onUnmounted(() => {
  if (pollTimer !== null) {
    clearTimeout(pollTimer);
    pollTimer = null;
  }
});

watch(() => props.taskId, async () => {
  lines.value = [];
  nextOffset = 0;
  task.value = null;
  loading.value = true;
  if (pollTimer !== null) {
    clearTimeout(pollTimer);
    pollTimer = null;
  }
  await fetchTask();
  try {
    const initial = await tasksTail(props.taskId, 0);
    lines.value = initial;
    if (initial.length > 0) {
      nextOffset = initial[initial.length - 1].offset + 1;
    }
  } finally {
    loading.value = false;
  }
  if (!isTerminal.value) schedulePoll();
});

// ── helpers ───────────────────────────────────────────────────────────────

function scrollToBottom() {
  if (outputEl.value) {
    outputEl.value.scrollTop = outputEl.value.scrollHeight;
  }
}

function lineClass(stream: string): string {
  return stream === 'stderr' ? 'text-signal-danger' : 'text-ink';
}

function statusLabel(): string {
  if (!task.value) return '';
  const s = task.value.status;
  if (s === 'running') return '● running';
  if (s === 'completed') return '✓ completed';
  if (s === 'failed') return `✗ failed (${task.value.exitCode})`;
  return s;
}
</script>

<template>
  <div
    class="flex flex-col h-full"
    data-testid="task-output-viewer"
    :data-task-id="taskId"
  >
    <!-- Header -->
    <div
      class="flex items-center justify-between px-4 py-2 border-b border-border-muted bg-surface-raised shrink-0"
    >
      <span class="font-mono text-[12px] text-ink truncate" :title="taskId">
        {{ task?.description || task?.cmd || taskId }}
      </span>
      <span
        class="text-[11px] font-mono ml-4 shrink-0"
        :class="isTerminal ? 'text-ink-muted' : 'text-accent'"
        data-testid="viewer-status"
      >
        {{ statusLabel() }}
      </span>
    </div>

    <!-- Loading -->
    <p v-if="loading" class="p-4 text-ink-muted text-[13px]">Loading…</p>

    <!-- Error -->
    <p v-if="error" class="p-4 text-signal-danger text-[13px]" data-testid="viewer-error">
      {{ error }}
    </p>

    <!-- Output area -->
    <div
      ref="outputEl"
      class="flex-1 overflow-y-auto p-4 font-mono text-[12px] bg-surface"
      data-testid="viewer-output"
    >
      <div v-if="lines.length === 0 && !loading" class="text-ink-muted">
        (no output yet)
      </div>
      <div
        v-for="line in lines"
        :key="line.offset"
        :class="lineClass(line.stream)"
        class="whitespace-pre-wrap break-all leading-relaxed"
      >{{ line.text }}</div>
    </div>
  </div>
</template>
