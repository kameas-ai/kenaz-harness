<script setup lang="ts">
/**
 * SessionCloseDialog — confirms session close when background tasks are running.
 *
 * Shows three actions:
 *   - "Kill tasks" — SIGTERM all running tasks then proceed with delete.
 *   - "Leave running" — detach tasks from the session (they keep running).
 *   - "Cancel" — abort the close.
 *
 * The "Convert to scheduled" path is a stub that emits the event for the
 * scheduled-chat-runs integration to implement.
 *
 * (background-task-monitor-01KZNP3C WP07)
 */

import { computed } from 'vue';
import type { TaskRow } from '@/lib/types';

const props = defineProps<{
  /** Whether the dialog is visible. */
  open: boolean;
  /** The running tasks that will be affected. */
  tasks: TaskRow[];
  /** The session being closed. */
  sessionId: string;
}>();

const emit = defineEmits<{
  /** User chose to kill all running tasks and close the session. */
  (e: 'kill'): void;
  /** User chose to leave tasks running (orphaned) and close the session. */
  (e: 'leave-running'): void;
  /** User cancelled — session should not be deleted. */
  (e: 'cancel'): void;
}>();

const taskCount = computed(() => props.tasks.length);
const taskNoun = computed(() => (taskCount.value === 1 ? 'task' : 'tasks'));

function onKill() { emit('kill'); }
function onLeave() { emit('leave-running'); }
function onCancel() { emit('cancel'); }
</script>

<template>
  <Teleport to="body">
    <div
      v-if="open"
      class="fixed inset-0 z-50 flex items-center justify-center bg-black/40"
      role="dialog"
      aria-modal="true"
      aria-labelledby="session-close-dialog-title"
      data-testid="session-close-dialog"
      @click.self="onCancel"
    >
      <div class="bg-surface border border-border rounded-xl shadow-xl w-full max-w-sm p-6 flex flex-col gap-4">
        <!-- Title -->
        <h2
          id="session-close-dialog-title"
          class="font-ui font-semibold text-[14px] text-ink"
        >
          Close session?
        </h2>

        <!-- Body -->
        <p class="text-[13px] text-ink-muted leading-relaxed">
          This session has
          <strong class="text-ink">{{ taskCount }} running background {{ taskNoun }}</strong>.
          What should happen to {{ taskCount === 1 ? 'it' : 'them' }} when you close?
        </p>

        <!-- Task list preview -->
        <ul class="flex flex-col gap-1 max-h-32 overflow-y-auto">
          <li
            v-for="task in tasks"
            :key="task.id"
            class="text-[12px] font-mono text-ink-muted truncate"
          >
            ⚙ {{ task.description || task.cmd || task.id }}
          </li>
        </ul>

        <!-- Actions -->
        <div class="flex flex-col gap-2">
          <button
            type="button"
            class="w-full px-4 py-2 text-[13px] font-ui bg-signal-danger text-white rounded-lg hover:brightness-90 transition-colors"
            data-testid="session-close-kill-btn"
            @click="onKill"
          >
            Kill {{ taskNoun }} and close
          </button>

          <button
            type="button"
            class="w-full px-4 py-2 text-[13px] font-ui bg-surface-raised border border-border text-ink rounded-lg hover:bg-surface transition-colors"
            data-testid="session-close-leave-btn"
            @click="onLeave"
          >
            Leave running, close session
          </button>

          <button
            type="button"
            class="w-full px-4 py-2 text-[13px] font-ui text-ink-muted hover:text-ink transition-colors"
            data-testid="session-close-cancel-btn"
            @click="onCancel"
          >
            Cancel
          </button>
        </div>
      </div>
    </div>
  </Teleport>
</template>
