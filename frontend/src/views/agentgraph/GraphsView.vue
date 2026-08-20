<script setup lang="ts">
/**
 * GraphsView — Bundle A WP06 landing page for the agent-graph
 * subsystem. Lists every graph (bundled library + user-saved), lets
 * the user open the editor for an existing graph, and surfaces a
 * "Run" button that fires Graph.startRun and routes to the RunView.
 */
import { onMounted, ref } from 'vue';
import { useRouter } from 'vue-router';
import CanvasHead from '@/shell/CanvasHead.vue';
import { useHarnessClient } from '@/lib/useHarnessAPI';
import type { GraphInfo } from '@/lib/types';

const client = useHarnessClient();
const router = useRouter();

const graphs = ref<readonly GraphInfo[]>([]);
const loading = ref(false);
const error = ref<string | null>(null);
const runError = ref<string | null>(null);

async function refresh() {
  loading.value = true;
  error.value = null;
  try {
    graphs.value = await client.graph.listGraphs('');
  } catch (err) {
    error.value = err instanceof Error ? err.message : String(err);
    graphs.value = [];
  } finally {
    loading.value = false;
  }
}

async function runGraph(g: GraphInfo) {
  if (g.invalid) return;
  runError.value = null;
  try {
    const resp = await client.graph.startRun({ graphId: g.id });
    void router.push({ name: 'graph-run', params: { runId: resp.runId } });
  } catch (err) {
    runError.value = err instanceof Error ? err.message : String(err);
  }
}

function openEditor(g: GraphInfo) {
  void router.push({ name: 'graph-editor', params: { id: g.id } });
}

function newGraph() {
  void router.push({ name: 'graph-editor', params: { id: '__new__' } });
}

async function remove(g: GraphInfo) {
  if (g.scope !== 'user') return;
  if (
    typeof window !== 'undefined' &&
    typeof window.confirm === 'function' &&
    !window.confirm(`Delete user graph "${g.id}"?`)
  ) {
    return;
  }
  try {
    await client.graph.deleteGraph(g.id);
    await refresh();
  } catch (err) {
    error.value = err instanceof Error ? err.message : String(err);
  }
}

onMounted(() => {
  void refresh();
});

defineExpose({ refresh });
</script>

<template>
  <div>
    <CanvasHead
      number="12"
      section="GRAPHS"
      title="Agent graphs"
      subtitle="Library of compute / control / state graphs the kernel runs against a session. Bundled graphs are read-only; user graphs persist under DataDir."
    >
      <template #trailing>
        <button
          type="button"
          class="rounded-sm border border-accent bg-surface-2 px-3 py-1 font-ui text-[12px] uppercase tracking-[0.18em] text-accent"
          data-testid="graphs-new"
          @click="newGraph"
        >
          New graph
        </button>
      </template>
    </CanvasHead>

    <div class="px-6 py-4 max-w-4xl">
      <div
        v-if="error"
        class="mb-3 rounded-md border border-signal-danger bg-surface-1 px-3 py-2 font-ui text-[12px] text-signal-danger"
        role="alert"
      >
        {{ error }}
      </div>
      <div
        v-if="runError"
        class="mb-3 rounded-md border border-signal-danger bg-surface-1 px-3 py-2 font-ui text-[12px] text-signal-danger"
        role="alert"
      >
        {{ runError }}
      </div>

      <div v-if="loading" class="font-ui text-[12px] text-ink-muted">
        Loading graphs…
      </div>

      <div
        v-else-if="graphs.length === 0"
        class="rounded-md border border-border-muted bg-surface-1 px-4 py-6 font-ui text-[13px] text-ink-muted"
        data-testid="graphs-empty"
      >
        No graphs available.
      </div>

      <ul v-else class="space-y-2" data-testid="graphs-list">
        <li
          v-for="g in graphs"
          :key="`${g.scope}:${g.id}`"
          class="rounded-md border border-border-muted bg-surface-1 px-4 py-3"
          :data-testid="`graph-row-${g.id}`"
        >
          <div class="flex items-baseline justify-between gap-3">
            <div>
              <div class="flex items-center gap-2">
                <button
                  type="button"
                  class="font-ui text-[14px] font-semibold text-ink hover:underline"
                  @click="openEditor(g)"
                >
                  {{ g.name || g.id }}
                </button>
                <span
                  class="rounded-sm border border-border-muted px-2 py-0 font-ui text-[10px] uppercase tracking-[0.18em] text-ink-dim"
                >
                  {{ g.scope }}
                </span>
                <span
                  v-if="g.invalid"
                  class="rounded-sm border border-signal-danger px-2 py-0 font-ui text-[10px] uppercase tracking-[0.18em] text-signal-danger"
                  :data-testid="`graph-invalid-${g.id}`"
                  :title="g.invalidReason"
                >
                  invalid
                </span>
              </div>
              <div
                v-if="g.description"
                class="mt-1 font-ui text-[12px] text-ink-muted"
              >
                {{ g.description }}
              </div>
              <div
                v-if="g.invalid"
                class="mt-1 font-ui text-[11px] text-signal-danger"
                :data-testid="`graph-invalid-reason-${g.id}`"
              >
                {{ g.invalidReason || 'This graph does not pass validation and cannot run.' }}
              </div>
            </div>
            <div class="flex shrink-0 items-center gap-2">
              <button
                type="button"
                class="rounded-sm border border-accent px-2 py-1 font-ui text-[11px] uppercase tracking-[0.18em] text-accent hover:bg-surface-2 disabled:cursor-not-allowed disabled:opacity-40"
                :data-testid="`graph-run-${g.id}`"
                :disabled="g.invalid"
                :title="g.invalid ? (g.invalidReason || 'Invalid graph') : undefined"
                @click="runGraph(g)"
              >
                Run
              </button>
              <button
                v-if="g.scope === 'user'"
                type="button"
                class="rounded-sm border border-border-muted px-2 py-1 font-ui text-[11px] uppercase tracking-[0.18em] text-signal-danger hover:bg-surface-2"
                :data-testid="`graph-delete-${g.id}`"
                @click="remove(g)"
              >
                Delete
              </button>
            </div>
          </div>
        </li>
      </ul>
    </div>
  </div>
</template>
