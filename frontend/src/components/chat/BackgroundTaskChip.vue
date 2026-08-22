<script setup lang="ts">
/**
 * BackgroundTaskChip — chat-header chip showing the count of running
 * background tasks owned by the current session.
 *
 * Clicking the chip opens the Settings → Tasks panel filtered to this
 * session. The chip hides itself when there are no running tasks.
 *
 * (background-task-monitor-01KZNP3C WP06; mounted by
 * subagent-control-and-background-tasks-01PMZB11 UNIT-11 — this
 * component had never been mounted anywhere since it was created in
 * v0.11.0. SessionHeader.vue is the natural host: it already renders
 * per-session chips (AutonomyChip, PlanModeBadge) and has `session.id`
 * on hand.)
 *
 * Routes through the typed harnessClient's Tasks_ListBySession, which
 * scopes the query server-side instead of fetching every task and
 * filtering client-side — this binding existed in harnessClient.ts with
 * zero non-test callers before this component was mounted.
 */

import { ref, computed, onMounted, onUnmounted } from 'vue';
import { useHarnessClient } from '@/lib/useHarnessAPI';
import type { TaskRow } from '@/lib/types';

const client = useHarnessClient();

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

// Tasks_ListBySession already scopes to props.sessionId server-side;
// only the running-status filter is left to do client-side.
const runningTasks = computed(() =>
  tasks.value.filter(t => t.status === 'running'),
);

const runningCount = computed(() => runningTasks.value.length);

const visible = computed(() => runningCount.value > 0);

// ── lifecycle ─────────────────────────────────────────────────────────────

async function load() {
  try {
    tasks.value = await client.Tasks_ListBySession(props.sessionId);
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
