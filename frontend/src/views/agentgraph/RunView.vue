<script setup lang="ts">
/**
 * RunView — Bundle A WP06 surface for a single kernel run. Polls
 * Graph_GetRunStatus + Graph_GetRunTrace at 500 ms and renders:
 *
 *   - the lifecycle state pill
 *   - per-counter readouts (LLM tokens / calls / tools / cost)
 *   - the trace tail (last N events) with kind + node-id badges
 *   - paused-state UI: reads PendingAsk and exposes a resume input
 */
import { computed, onBeforeUnmount, onMounted, ref } from 'vue';
import { useRoute, useRouter } from 'vue-router';
import CanvasHead from '@/shell/CanvasHead.vue';
import { useHarnessClient } from '@/lib/useHarnessAPI';
import type { GraphRunStatus, GraphRunTraceEvent } from '@/lib/types';

const client = useHarnessClient();
const route = useRoute();
const router = useRouter();

const runId = computed(() => String(route.params.runId ?? ''));

const status = ref<GraphRunStatus | null>(null);
const events = ref<readonly GraphRunTraceEvent[]>([]);
const error = ref<string | null>(null);
const askResponse = ref('');
const submittingAsk = ref(false);

let pollHandle: ReturnType<typeof setTimeout> | null = null;
let cancelled = false;

const POLL_MS = 500;

async function pollOnce() {
  const id = runId.value;
  if (!id) return;
  try {
    const st = await client.graph.getRunStatus(id);
    status.value = st;
    const since = events.value.length > 0 ? events.value[events.value.length - 1].seq : 0;
    const tail = await client.graph.getRunTrace(id, since);
    if (tail.length > 0) {
      events.value = [...events.value, ...tail];
    }
  } catch (err) {
    error.value = err instanceof Error ? err.message : String(err);
  }
}

function schedulePoll() {
  if (cancelled) return;
  pollHandle = setTimeout(async () => {
    await pollOnce();
    const st = status.value;
    if (st && (st.state === 'completed' || st.state === 'failed')) return;
    schedulePoll();
  }, POLL_MS);
}

async function resume() {
  const id = runId.value;
  if (!id) return;
  submittingAsk.value = true;
  try {
    await client.graph.resume(id, askResponse.value);
    askResponse.value = '';
    await pollOnce();
    schedulePoll();
  } catch (err) {
    error.value = err instanceof Error ? err.message : String(err);
  } finally {
    submittingAsk.value = false;
  }
}

async function cancelRun() {
  const id = runId.value;
  if (!id) return;
  try {
    await client.graph.cancelRun(id);
    await pollOnce();
  } catch (err) {
    error.value = err instanceof Error ? err.message : String(err);
  }
}

function backToList() {
  void router.push({ name: 'graphs' });
}

onMounted(async () => {
  await pollOnce();
  schedulePoll();
});

onBeforeUnmount(() => {
  cancelled = true;
  if (pollHandle) clearTimeout(pollHandle);
});

const stateLabel = computed(() => {
  const st = status.value?.state;
  if (st === 'running') return 'Running';
  if (st === 'paused') return 'Paused';
  if (st === 'completed') return 'Completed';
  if (st === 'failed') return 'Failed';
  return 'Pending';
});

const stateClass = computed(() => {
  const st = status.value?.state;
  if (st === 'running') return 'border-accent text-accent';
  if (st === 'paused') return 'border-signal-warn text-signal-warn';
  if (st === 'completed') return 'border-signal-ok text-signal-ok';
  if (st === 'failed') return 'border-signal-danger text-signal-danger';
  return 'border-border-muted text-ink-dim';
});

defineExpose({ pollOnce });
</script>

<template>
  <div>
    <CanvasHead
      number="12"
      section="GRAPHS"
      title="Run"
      :subtitle="`Run ${runId.slice(0, 16)}…`"
    >
      <template #trailing>
        <button
          type="button"
          class="rounded-sm border border-border-muted px-3 py-1 font-ui text-[12px] uppercase tracking-[0.18em] text-ink-dim hover:bg-surface-2"
          @click="backToList"
        >
          Back
        </button>
      </template>
    </CanvasHead>

    <div class="px-6 py-4 max-w-5xl space-y-4">
      <div
        v-if="error"
        class="rounded-md border border-signal-danger bg-surface-1 px-3 py-2 font-ui text-[12px] text-signal-danger"
        role="alert"
      >
        {{ error }}
      </div>

      <div
        class="rounded-md border border-border-muted bg-surface-1 px-4 py-3"
        data-testid="run-status"
      >
        <div class="flex items-center gap-3">
          <span
            class="rounded-sm border px-2 py-0 font-ui text-[10px] uppercase tracking-[0.18em]"
            :class="stateClass"
            data-testid="run-state"
          >
            {{ stateLabel }}
          </span>
          <span class="font-ui text-[13px] text-ink">
            graph: <span class="font-semibold">{{ status?.graphId || '—' }}</span>
          </span>
          <span v-if="status?.error" class="font-ui text-[12px] text-signal-danger">
            error: {{ status.error }}
          </span>
        </div>
        <div class="mt-2 flex flex-wrap gap-x-4 gap-y-1 font-ui text-[12px] text-ink-muted">
          <span data-testid="run-counter-nodes">nodes complete: {{ status?.nodesComplete ?? 0 }}</span>
          <span>llm tokens: {{ status?.llmTokens ?? 0 }}</span>
          <span>llm calls: {{ status?.llmCalls ?? 0 }}</span>
          <span>tool calls: {{ status?.toolCalls ?? 0 }}</span>
          <span>cost: ${{ (status?.costUsd ?? 0).toFixed(4) }}</span>
        </div>
        <div
          v-if="status?.state === 'running' || status?.state === 'paused'"
          class="mt-3"
        >
          <button
            type="button"
            class="rounded-sm border border-signal-danger px-2 py-1 font-ui text-[11px] uppercase tracking-[0.18em] text-signal-danger hover:bg-surface-2"
            data-testid="run-cancel"
            @click="cancelRun"
          >
            Cancel
          </button>
        </div>
      </div>

      <div
        v-if="status?.pendingAsk"
        class="rounded-md border border-signal-warn bg-surface-1 px-4 py-3"
        data-testid="run-pending-ask"
      >
        <div class="font-ui text-[12px] uppercase tracking-[0.18em] text-signal-warn">
          AskNode parked the run
        </div>
        <div class="mt-1 font-ui text-[14px] font-semibold text-ink">
          {{ status.pendingAsk.question }}
        </div>
        <div class="mt-2 flex items-end gap-2">
          <input
            v-model="askResponse"
            type="text"
            data-testid="run-ask-input"
            class="flex-1 rounded-sm border border-border-muted bg-surface-0 px-2 py-1 font-ui text-[13px] text-ink"
            placeholder="Type your answer…"
          />
          <button
            type="button"
            :disabled="submittingAsk"
            class="rounded-sm border border-accent bg-surface-2 px-3 py-1 font-ui text-[12px] uppercase tracking-[0.18em] text-accent disabled:opacity-50"
            data-testid="run-ask-submit"
            @click="resume"
          >
            Resume
          </button>
        </div>
      </div>

      <div class="rounded-md border border-border-muted bg-surface-1">
        <div
          class="border-b border-border-muted px-4 py-2 font-ui text-[11px] uppercase tracking-[0.18em] text-ink-dim"
        >
          Trace tail
        </div>
        <ul
          v-if="events.length > 0"
          class="max-h-[320px] overflow-y-auto"
          data-testid="run-trace"
        >
          <li
            v-for="ev in events"
            :key="ev.seq"
            class="border-b border-border-muted px-4 py-2 font-ui text-[12px] text-ink-muted last:border-b-0"
            :data-testid="`trace-event-${ev.seq}`"
          >
            <span class="font-mono text-[10px] text-ink-dim">#{{ ev.seq }}</span>
            <span class="ml-2 rounded-sm border border-border-muted px-1 py-0 text-[10px] uppercase tracking-[0.18em] text-ink-dim">
              {{ ev.kind }}
            </span>
            <span v-if="ev.nodeId" class="ml-2 font-mono text-[11px] text-ink">
              {{ ev.nodeId }}
            </span>
          </li>
        </ul>
        <div
          v-else
          class="px-4 py-3 font-ui text-[12px] text-ink-muted"
          data-testid="run-trace-empty"
        >
          No events yet.
        </div>
      </div>
    </div>
  </div>
</template>
