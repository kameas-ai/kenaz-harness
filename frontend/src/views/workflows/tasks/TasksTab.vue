<script setup lang="ts">
/**
 * TasksTab — Workflows › Tasks. nav-ia-sweep-01DOGF0F WP04 (FR-3).
 *
 * Background tasks (bash commands run with run_in_background) and their live
 * output, moved here from Settings › Runtime › Tasks. Toggles between the
 * task list and one task's output within the tab (view-task / back) rather
 * than a separate route — the same behaviour the Settings pane had.
 */
import { ref } from 'vue';
import TasksPanel from './TasksPanel.vue';
import TaskOutputViewer from './TaskOutputViewer.vue';

const viewingTaskId = ref<string | null>(null);
</script>

<template>
  <div class="flex flex-col h-full" data-testid="workflows-tasks-tab">
    <p class="font-ui text-xs text-ink-muted mb-2">
      Background tasks — bash commands run with run_in_background, and their
      live output.
    </p>
    <template v-if="viewingTaskId">
      <button
        type="button"
        class="self-start mb-2 text-[11px] text-ink-muted hover:text-ink transition-colors underline"
        data-testid="tasks-viewer-back-btn"
        @click="viewingTaskId = null"
      >
        ← Back to tasks
      </button>
      <TaskOutputViewer :task-id="viewingTaskId" class="flex-1 min-h-0" />
    </template>
    <TasksPanel v-else @view-task="viewingTaskId = $event" />
  </div>
</template>
