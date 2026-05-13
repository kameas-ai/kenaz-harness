<script setup lang="ts">
/**
 * SubagentTab — chat surface specialized for live sub-agent sessions.
 *
 * Shows the worker's full transcript (via the standard MessageList),
 * budget meters (SubagentBudgetMeter), and control buttons
 * (Pause/Resume/Abort). The composer (ChatInput) is enabled while the
 * worker is running so the user can steer the worker; messages are
 * interleaved with the model-driven trace on the next worker turn.
 *
 * When the user types a message the parent session is notified so it
 * can auto-promote MergePolicy from "auto" to "confirm".
 *
 * branch-subagent-interactive-01KZNP3B WP08.
 */
import { ref, computed } from 'vue';
import SubagentBudgetMeter from '@/components/chat/SubagentBudgetMeter.vue';
import type { SubagentBranch, SubagentStatus } from '@/lib/types';

const props = defineProps<{
  /** The sub-agent branch descriptor. */
  branch: SubagentBranch;
  /** Child session id — used by the caller to load the transcript. */
  childSessionId: string;
}>();

const emit = defineEmits<{
  /** User wants to abort this sub-agent. */
  abort: [branchId: string];
  /** User sent a steering message. Payload is the message text. */
  steer: [branchId: string, message: string];
  /** User clicked Pause. */
  pause: [branchId: string];
  /** User clicked Resume. */
  resume: [branchId: string];
}>();

const steerInput = ref('');
const steerError = ref<string | null>(null);

const isRunning = computed(() =>
  (props.branch.subagentStatus as SubagentStatus) === 'running',
);
const isPaused = computed(() =>
  (props.branch.subagentStatus as SubagentStatus) === 'paused',
);
const isComplete = computed(() =>
  ['complete', 'error', 'aborted'].includes(props.branch.subagentStatus),
);

const statusLabel = computed(() => {
  switch (props.branch.subagentStatus as SubagentStatus) {
    case 'running':
      return 'Running';
    case 'awaiting-merge':
      return 'Awaiting merge';
    case 'paused':
      return 'Paused';
    case 'complete':
      return 'Complete';
    case 'error':
      return 'Error';
    case 'aborted':
      return 'Aborted';
    default:
      return props.branch.subagentStatus;
  }
});

const statusClass = computed(() => {
  switch (props.branch.subagentStatus as SubagentStatus) {
    case 'running':
      return 'text-accent';
    case 'error':
    case 'aborted':
      return 'text-status-error';
    case 'complete':
      return 'text-status-success';
    default:
      return 'text-ink-muted';
  }
});

function sendSteer() {
  const text = steerInput.value.trim();
  if (!text) return;
  steerInput.value = '';
  steerError.value = null;
  emit('steer', props.branch.id, text);
}

function confirmAbort() {
  if (!confirm('Abort this sub-agent? The parent will receive an abort result.')) return;
  emit('abort', props.branch.id);
}
</script>

<template>
  <div class="flex h-full flex-col" data-testid="subagent-tab">
    <!-- Sub-agent header bar -->
    <div
      class="flex items-center gap-3 border-b border-border-muted bg-surface-1 px-4 py-2"
      data-testid="subagent-header"
    >
      <!-- Worker glyph + profile -->
      <span class="text-sm" aria-hidden="true">⚙</span>
      <span class="font-ui text-sm font-medium text-ink">
        {{ branch.title || branch.profileId }}
      </span>
      <span class="font-ui text-[11px] text-ink-subtle">{{ branch.profileId }}</span>
      <span
        class="rounded-full px-2 py-0.5 font-ui text-[10px] uppercase tracking-wide"
        :class="statusClass"
        :data-testid="`subagent-status-${branch.id}`"
      >
        {{ statusLabel }}
      </span>

      <!-- Budget meters -->
      <div class="ml-auto">
        <SubagentBudgetMeter
          :tokens-used="branch.tokensUsed"
          :budget-tokens="branch.budgetTokens"
          :elapsed-s="branch.elapsedS"
          :budget-time-s="branch.budgetTimeS"
        />
      </div>

      <!-- Control buttons -->
      <div class="flex shrink-0 gap-2">
        <button
          v-if="isRunning"
          type="button"
          class="rounded border border-border-muted px-2 py-1 font-ui text-xs text-ink hover:bg-surface-2"
          data-testid="subagent-pause-btn"
          @click="emit('pause', branch.id)"
        >
          Pause
        </button>
        <button
          v-if="isPaused"
          type="button"
          class="rounded border border-accent px-2 py-1 font-ui text-xs text-accent hover:bg-accent/10"
          data-testid="subagent-resume-btn"
          @click="emit('resume', branch.id)"
        >
          Resume
        </button>
        <button
          v-if="!isComplete"
          type="button"
          class="rounded border border-status-error/30 px-2 py-1 font-ui text-xs text-status-error hover:bg-status-error/10"
          data-testid="subagent-abort-btn"
          @click="confirmAbort"
        >
          Abort
        </button>
      </div>
    </div>

    <!-- Transcript area (consumers wire in their own MessageList) -->
    <div class="flex-1 overflow-y-auto" data-testid="subagent-transcript">
      <slot name="transcript">
        <div class="flex h-full items-center justify-center font-ui text-sm text-ink-subtle">
          Open a sub-agent branch to see its transcript here.
        </div>
      </slot>
    </div>

    <!-- Steering composer (enabled while running/paused, locked when complete) -->
    <div
      class="border-t border-border-muted bg-surface-0 px-4 py-3"
      :data-testid="`subagent-composer-${isComplete ? 'disabled' : 'enabled'}`"
    >
      <div
        v-if="isComplete"
        class="font-ui text-xs text-ink-subtle"
      >
        Sub-agent has {{ branch.subagentStatus }}. No more messages can be sent.
      </div>
      <template v-else>
        <div class="flex gap-2">
          <input
            v-model="steerInput"
            type="text"
            :placeholder="isRunning ? 'Steer the worker…' : 'Worker is paused. Resume to continue.'"
            :disabled="!isRunning && !isPaused"
            class="flex-1 rounded border border-border-muted bg-surface-1 px-3 py-1.5 font-ui text-sm text-ink placeholder:text-ink-subtle focus:outline-none focus:ring-1 focus:ring-accent disabled:opacity-60"
            data-testid="subagent-steer-input"
            @keydown.enter="sendSteer"
          />
          <button
            type="button"
            :disabled="!steerInput.trim() || (!isRunning && !isPaused)"
            class="rounded bg-accent px-3 py-1.5 font-ui text-sm text-white hover:bg-accent/90 disabled:opacity-50"
            data-testid="subagent-steer-send"
            @click="sendSteer"
          >
            Send
          </button>
        </div>
        <div v-if="steerError" class="mt-1 font-ui text-xs text-status-error">
          {{ steerError }}
        </div>
        <p class="mt-1 font-ui text-[11px] text-ink-subtle">
          Messages sent here are delivered to the worker on its next turn. Engaging
          the worker will ask for merge confirmation before the summary returns to
          the parent.
        </p>
      </template>
    </div>
  </div>
</template>
