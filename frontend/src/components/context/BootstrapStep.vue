<script setup lang="ts">
import { ref, computed } from 'vue';
import { useHarnessClient } from '@/lib/harnessClientContext';
import { useEventStream } from '@/lib/useEventStream';
import type { ContextBootstrapRunStatus, ContextConnectorProgress } from '@/lib/harnessClient';

/**
 * BootstrapStep — the onboarding context-bootstrap step
 * (context-bootstrap-harness-integration WP07a).
 *
 * Flow:
 *   1. Interview: the user picks which connectors to learn from (seeded from
 *      the `sources` prop — the welcome checklist / recipe tool list). The
 *      parent onboarding flow is responsible for ensuring the required MCPs
 *      are installed + connected (reuse the existing MCP install/connect UI);
 *      this component only collects consent + drives the run.
 *   2. Run: "Start" calls onboarding.runBootstrap(consented), which kicks the
 *      engine and marks the bootstrap_run milestone when it finishes.
 *   3. Live progress: per-connector counts + phase, driven by the
 *      "contextbootstrap:progress" EventsEmit stream.
 *
 * Emits `done` with the run id when the run finishes.
 */

const props = defineProps<{
  /** Candidate connector ids (seeded from the recipe / welcome checklist). */
  sources: string[];
}>();

const emit = defineEmits<{
  (e: 'done', runID: string): void;
}>();

const client = useHarnessClient();

// Interview: which connectors the user consents to. Default: all selected.
const selected = ref<Set<string>>(new Set(props.sources));

function toggle(id: string): void {
  const next = new Set(selected.value);
  if (next.has(id)) next.delete(id);
  else next.add(id);
  selected.value = next;
}

const consented = computed<string[]>(() => props.sources.filter((s) => selected.value.has(s)));

// Live run status from the progress stream.
const status = ref<ContextBootstrapRunStatus | null>(null);
useEventStream<ContextBootstrapRunStatus>('contextbootstrap:progress', (s) => {
  status.value = s;
});

const running = ref(false);
const error = ref<string | null>(null);

const connectors = computed<ContextConnectorProgress[]>(() => status.value?.connectors ?? []);
const phase = computed<string>(() => status.value?.phase ?? 'idle');
const totalNodes = computed<number>(() => status.value?.total_nodes_written ?? 0);
const finished = computed<boolean>(() => phase.value === 'done' || phase.value === 'failed');

async function start(): Promise<void> {
  if (running.value || consented.value.length === 0) return;
  running.value = true;
  error.value = null;
  try {
    const runID = await client.onboarding.runBootstrap(consented.value);
    emit('done', runID);
  } catch (e: unknown) {
    error.value = e instanceof Error ? e.message : String(e);
  } finally {
    running.value = false;
  }
}
</script>

<template>
  <div class="flex flex-col gap-3" data-testid="bootstrap-step">
    <div class="flex flex-col gap-1">
      <span class="font-ui text-sm font-medium text-ink">Learn from your tools</span>
      <span class="font-ui text-xs text-ink-muted">
        Pick which connected tools the harness may read to build your context.
      </span>
    </div>

    <ul class="flex flex-col gap-1">
      <li
        v-for="id in props.sources"
        :key="id"
        class="flex items-center gap-2"
      >
        <input
          :id="`bootstrap-src-${id}`"
          type="checkbox"
          :checked="selected.has(id)"
          :disabled="running"
          :data-testid="`bootstrap-src-${id}`"
          @change="toggle(id)"
        />
        <label :for="`bootstrap-src-${id}`" class="font-ui text-sm text-ink">{{ id }}</label>
      </li>
    </ul>

    <div v-if="!running && !status">
      <button
        type="button"
        class="font-ui text-sm px-3 py-1.5 rounded-sm bg-accent text-on-accent disabled:opacity-50"
        :disabled="consented.length === 0"
        data-testid="bootstrap-start"
        @click="start"
      >
        Start
      </button>
    </div>

    <p v-if="error" class="font-ui text-xs text-signal-warning" data-testid="bootstrap-error">
      {{ error }}
    </p>

    <!-- Live run progress -->
    <div v-if="status" class="flex flex-col gap-2" data-testid="bootstrap-progress">
      <div class="flex items-center justify-between">
        <span class="font-ui text-xs text-ink-muted">Phase: {{ phase }}</span>
        <span class="font-ui text-xs text-ink-muted">{{ totalNodes }} nodes</span>
      </div>
      <ul class="flex flex-col gap-0.5">
        <li
          v-for="c in connectors"
          :key="c.connector_id"
          class="flex items-center justify-between font-ui text-xs"
          :data-testid="`bootstrap-connector-${c.connector_id}`"
        >
          <span class="text-ink">{{ c.label || c.connector_id }}</span>
          <span class="text-ink-muted tabular-nums">
            {{ c.status }} · {{ c.nodes_extracted }} nodes
          </span>
        </li>
      </ul>
      <p v-if="finished" class="font-ui text-xs text-ink-muted" data-testid="bootstrap-finished">
        {{ phase === 'failed' ? 'Run failed.' : 'Done.' }}
      </p>
    </div>
  </div>
</template>
