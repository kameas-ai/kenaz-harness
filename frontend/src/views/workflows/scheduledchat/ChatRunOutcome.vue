<script setup lang="ts">
/**
 * ChatRunOutcome — one scheduled-chat run's outcome on a single line:
 * status, when, model, cost, an "Open session" link, and the error text
 * for a failed run (dogfood 2026-10-08 round 2: outcomes reached the
 * user only through a 10-second toast; the Schedules row and the Runs
 * tab showed nothing).
 *
 * Used by the Schedules row (ScheduledChatsPanel, the entry's lastRun)
 * and by each history row of the Runs tab (ScheduledInbox).
 */
import { computed } from 'vue';
import { RouterLink } from 'vue-router';
import type { ScheduledChatRunSummary } from '@/lib/scheduledChatClient';

const props = defineProps<{
  run: ScheduledChatRunSummary;
  /** Prefix copy, e.g. "Last run". Omitted on history rows. */
  label?: string;
}>();

const statusClass = computed(() => {
  if (props.run.status === 'completed') return 'text-signal-ok';
  if (props.run.status === 'running') return 'text-signal-warn';
  if (props.run.status === 'failed') return 'text-signal-danger';
  return 'text-ink-muted';
});

const when = computed(() => {
  const t = Date.parse(props.run.startedAt);
  if (Number.isNaN(t)) return props.run.startedAt;
  return new Date(t).toLocaleString();
});

const cost = computed(() =>
  typeof props.run.costUsd === 'number' && props.run.costUsd > 0
    ? `$${props.run.costUsd.toFixed(4)}`
    : '',
);
</script>

<template>
  <div class="min-w-0 space-y-0.5" :data-testid="`chat-run-outcome-${run.id}`">
    <div class="flex flex-wrap items-center gap-x-2 gap-y-0.5 font-ui text-xs">
      <span v-if="label" class="text-ink-muted">{{ label }}:</span>
      <span :class="statusClass" :data-testid="`chat-run-outcome-status-${run.id}`">{{ run.status }}</span>
      <span class="text-ink-muted" :data-testid="`chat-run-outcome-time-${run.id}`">{{ when }}</span>
      <span
        v-if="run.model"
        class="font-mono text-ink-muted truncate"
        :data-testid="`chat-run-outcome-model-${run.id}`"
      >{{ run.model }}</span>
      <span v-if="cost" class="font-mono text-ink-muted" :data-testid="`chat-run-outcome-cost-${run.id}`">{{ cost }}</span>
      <RouterLink
        v-if="run.sessionId"
        :to="`/sessions/${encodeURIComponent(run.sessionId)}`"
        class="text-accent hover:underline"
        :data-testid="`chat-run-outcome-open-${run.id}`"
        @click.stop
      >
        Open session
      </RouterLink>
    </div>
    <div
      v-if="run.status === 'failed' && run.error"
      class="font-ui text-xs text-signal-danger break-words"
      :data-testid="`chat-run-outcome-error-${run.id}`"
    >
      {{ run.error }}
    </div>
  </div>
</template>
