<script setup lang="ts">
/**
 * TasksPanel — Settings → Tasks
 *
 * Lists all background tasks (running and completed) with kind, owner
 * session, status, age, and action buttons (View output, Abort).
 *
 * Refreshes every 2 s when there are running tasks; stops polling when all
 * tasks are terminal.
 *
 * (background-task-monitor-01KZNP3C WP05)
 */

import { ref, computed, onMounted, onUnmounted } from 'vue';
import type { TaskRow } from '../../lib/types';
import { tasksList, tasksAbort } from '../../lib/tasks';

const props = withDefaults(defineProps<{
  /** When set, only tasks for this session are shown. */
  filterSessionId?: string;
}>(), {
  filterSessionId: undefined,
});

const emit = defineEmits<{
  /** Emitted when the user clicks "View output" for a task. */
  (e: 'view-task', taskId: string): void;
}>();

// ── state ─────────────────────────────────────────────────────────────────

const tasks = ref<TaskRow[]>([]);
const loading = ref(true);
const error = ref<string | null>(null);
const abortingId = ref<string | null>(null);

// ── computed ──────────────────────────────────────────────────────────────

const filtered = computed(() => {
  if (!props.filterSessionId) return tasks.value;
  return tasks.value.filter(t => t.ownerSessionId === props.filterSessionId);
});

const runningCount = computed(() =>
  tasks.value.filter(t => t.status === 'running').length,
);

// ── lifecycle ─────────────────────────────────────────────────────────────

let pollTimer: ReturnType<typeof setTimeout> | null = null;

async function load() {
  try {
    tasks.value = await tasksList();
    error.value = null;
  } catch (err) {
    error.value = String(err);
  } finally {
    loading.value = false;
  }
}

function schedulePoll() {
  if (pollTimer !== null) return;
  // Poll every 2 s while there are running tasks.
  pollTimer = setTimeout(async () => {
    pollTimer = null;
    await load();
    if (runningCount.value > 0) {
      schedulePoll();
    }
  }, 2000);
}

onMounted(async () => {
  await load();
  if (runningCount.value > 0) schedulePoll();
});

onUnmounted(() => {
  if (pollTimer !== null) {
    clearTimeout(pollTimer);
    pollTimer = null;
  }
});

// ── actions ───────────────────────────────────────────────────────────────

async function abort(id: string) {
  abortingId.value = id;
  try {
    await tasksAbort(id);
    await load();
  } catch (err) {
    error.value = `Abort failed: ${err}`;
  } finally {
    abortingId.value = null;
  }
}

function viewOutput(id: string) {
  emit('view-task', id);
}

// ── helpers ───────────────────────────────────────────────────────────────

function statusClass(status: string): string {
  switch (status) {
    case 'running':  return 'text-accent';
    case 'completed': return 'text-success';
    case 'failed':   return 'text-error';
    case 'cancelled':
    case 'crashed':  return 'text-ink-muted';
    default:         return 'text-ink-muted';
  }
}

function ageLabel(ageMs: number): string {
  if (ageMs < 1000) return `${ageMs}ms`;
  const s = Math.floor(ageMs / 1000);
  if (s < 60) return `${s}s`;
  const m = Math.floor(s / 60);
  if (m < 60) return `${m}m ${s % 60}s`;
  return `${Math.floor(m / 60)}h ${m % 60}m`;
}

function kindIcon(kind: string): string {
  switch (kind) {
    case 'bash':     return '⚙';
    case 'subagent': return '🤖';
    case 'wails':    return '🖥';
    default:         return '?';
  }
}
</script>

<template>
  <section
    class="flex flex-col gap-4 p-6"
    aria-label="Background tasks"
    data-testid="tasks-panel"
  >
    <!-- Header -->
    <div class="flex items-center justify-between">
      <h2 class="font-ui text-[13px] uppercase tracking-[0.14em] text-ink-muted">
        Background Tasks
      </h2>
      <span
        v-if="runningCount > 0"
        class="text-[11px] text-accent font-mono"
        data-testid="running-count"
      >
        {{ runningCount }} running
      </span>
    </div>

    <!-- Loading -->
    <p v-if="loading" class="text-ink-muted text-[13px]">Loading…</p>

    <!-- Error -->
    <p
      v-if="error"
      class="text-error text-[13px]"
      data-testid="tasks-error"
    >
      {{ error }}
    </p>

    <!-- Empty -->
    <p
      v-if="!loading && filtered.length === 0"
      class="text-ink-muted text-[13px]"
      data-testid="tasks-empty"
    >
      No background tasks.
    </p>

    <!-- Task list -->
    <ul
      v-if="filtered.length > 0"
      class="flex flex-col gap-2"
      data-testid="task-list"
    >
      <li
        v-for="task in filtered"
        :key="task.id"
        class="border border-border-muted rounded-lg p-3 flex flex-col gap-1 bg-surface-raised"
        :data-testid="`task-row-${task.id}`"
      >
        <!-- Top row: kind + description/cmd + status -->
        <div class="flex items-start justify-between gap-2">
          <div class="flex items-center gap-2 min-w-0">
            <span class="text-[12px]" :title="task.kind">{{ kindIcon(task.kind) }}</span>
            <span
              class="text-[13px] font-mono truncate"
              :title="task.cmd"
              data-testid="task-cmd"
            >
              {{ task.description || task.cmd || task.id }}
            </span>
          </div>
          <span
            class="text-[11px] font-mono shrink-0"
            :class="statusClass(task.status)"
            data-testid="task-status"
          >
            {{ task.status }}
            <template v-if="task.status !== 'running' && task.exitCode !== 0">
              ({{ task.exitCode }})
            </template>
          </span>
        </div>

        <!-- Bottom row: age + actions -->
        <div class="flex items-center justify-between gap-2">
          <span class="text-[11px] text-ink-muted font-mono" data-testid="task-age">
            {{ ageLabel(task.ageMs) }}
          </span>
          <div class="flex items-center gap-2">
            <button
              type="button"
              class="text-[11px] text-ink-muted hover:text-ink transition-colors underline"
              data-testid="task-view-btn"
              @click="viewOutput(task.id)"
            >
              View output
            </button>
            <button
              v-if="task.status === 'running'"
              type="button"
              class="text-[11px] text-error hover:text-error-dark transition-colors"
              :disabled="abortingId === task.id"
              data-testid="task-abort-btn"
              @click="abort(task.id)"
            >
              {{ abortingId === task.id ? 'Aborting…' : 'Abort' }}
            </button>
          </div>
        </div>
      </li>
    </ul>
  </section>
</template>
