<script setup lang="ts">
/**
 * BackgroundTaskChip — chat-header chip showing the count of running
 * background tasks owned by the current session.
 *
 * Clicking the chip opens the Settings → Tasks panel filtered to this
 * session. The chip hides itself when there are no running tasks.
 *
 * (background-task-monitor-01KZNP3C WP06)
 */

import { ref, computed, onMounted, onUnmounted } from 'vue';
import { tasksList } from '@/lib/tasks';
import type { TaskRow } from '@/lib/types';

const props = defineProps<{
  /** Session ID used to filter the task list. */
  sessionId: string;
}>();

const emit = defineEmits<{
  /** Emitted when the chip is clicked. Parent opens the Tasks panel. */
  (e: 'open-tasks'): void;
}>();

// ── state ─────────────────────────────────────────────────────────────────

const tasks = ref<TaskRow[]>([]);
let pollTimer: ReturnType<typeof setTimeout> | null = null;

// ── computed ──────────────────────────────────────────────────────────────

const runningTasks = computed(() =>
  tasks.value.filter(
    t => t.ownerSessionId === props.sessionId && t.status === 'running',
  ),
);

const runningCount = computed(() => runningTasks.value.length);

const visible = computed(() => runningCount.value > 0);

// ── lifecycle ─────────────────────────────────────────────────────────────

async function load() {
  try {
    tasks.value = await tasksList();
  } catch {
    // Silently fail — chip just hides.
  }
}

function schedulePoll() {
  if (pollTimer !== null) return;
  pollTimer = setTimeout(async () => {
    pollTimer = null;
    await load();
    // Keep polling as long as there are running tasks.
    schedulePoll();
  }, 2000);
}

onMounted(async () => {
  await load();
  schedulePoll();
});

onUnmounted(() => {
  if (pollTimer !== null) {
    clearTimeout(pollTimer);
    pollTimer = null;
  }
});
</script>

<template>
  <button
    v-if="visible"
    type="button"
    class="flex items-center gap-1 px-2 py-0.5 rounded text-[11px] font-mono bg-surface-raised border border-border-muted text-accent hover:text-ink transition-colors"
    title="Background tasks running — click to view"
    data-testid="background-task-chip"
    @click="emit('open-tasks')"
  >
    <span class="animate-pulse" aria-hidden="true">⚙</span>
    <span>{{ runningCount }} running</span>
  </button>
</template>
