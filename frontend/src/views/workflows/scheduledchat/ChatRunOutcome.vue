<script setup lang="ts">
/**
 * ChatRunOutcome — one scheduled-chat run's outcome: status, start time
 * (and duration once ended), model, cost, an "Open session" link when
 * the run's session exists, and the error text of a failed run.
 *
 * The single renderer for a run outcome: the Schedules row and the Runs
 * tab's collapsed row show the entry's lastRun with it, and each Runs-tab
 * history row is one of these. `testidPrefix` keeps each surface's test
 * ids distinct.
 */
import { computed } from 'vue';
import { RouterLink } from 'vue-router';
import type { ScheduledChatRunSummary } from '@/lib/scheduledChatClient';
import { formatCost } from '@/lib/formatCost';
import { formatDuration, formatTimestamp } from '@/lib/formatTime';

const props = withDefaults(
  defineProps<{
    run: ScheduledChatRunSummary;
    /** Prefix copy, e.g. "Last run". Omitted on history rows. */
    label?: string;
    testidPrefix?: string;
  }>(),
  { label: undefined, testidPrefix: 'chat-run-outcome' },
);

const statusClass = computed(() => {
  if (props.run.status === 'completed') return 'text-signal-ok';
  if (props.run.status === 'running') return 'text-signal-warn';
  if (props.run.status === 'failed') return 'text-signal-danger';
  return 'text-ink-muted';
});

const duration = computed(() => formatDuration(props.run.startedAt, props.run.endedAt));

const cost = computed(() =>
  typeof props.run.costUsd === 'number' && props.run.costUsd > 0 ? formatCost(props.run.costUsd) : '',
);

const tid = (part: string) => `${props.testidPrefix}-${part}-${props.run.id}`;
</script>

<template>
  <div class="min-w-0 space-y-0.5" :data-testid="tid('row')">
    <div class="flex flex-wrap items-center gap-x-2 gap-y-0.5 font-ui text-xs">
      <span v-if="label" class="text-ink-muted">{{ label }}:</span>
      <span class="capitalize" :class="statusClass" :data-testid="tid('status')">{{ run.status }}</span>
      <span class="text-ink-muted" :data-testid="tid('time')">
        {{ formatTimestamp(run.startedAt) }}<template v-if="duration"> ({{ duration }})</template>
      </span>
      <span v-if="run.model" class="font-mono text-ink-muted truncate" :data-testid="tid('model')">{{ run.model }}</span>
      <span v-if="cost" class="font-mono text-ink-muted" :data-testid="tid('cost')">{{ cost }}</span>
      <RouterLink
        v-if="run.sessionId"
        :to="`/sessions/${encodeURIComponent(run.sessionId)}`"
        class="text-accent hover:underline"
        :data-testid="tid('open')"
        @click.stop
      >
        Open session
      </RouterLink>
    </div>
    <div
      v-if="run.status === 'failed' && run.error"
      class="font-ui text-xs text-signal-danger break-words"
      :data-testid="tid('error')"
    >
      {{ run.error }}
    </div>
  </div>
</template>
