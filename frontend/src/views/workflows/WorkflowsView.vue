<script setup lang="ts">
/**
 * WorkflowsView — agentic workflows surface (v0.3.0 beta).
 *
 * Mission workflows-01KQ8TDG, WP09 (frontend slice). Lists every
 * workflow the engine has loaded (the bundled plan→implement→review
 * recipe ships as the only entry until WP06 storage lands user
 * uploads), lets the user pick one, hit Run, and watch the steps
 * complete one by one.
 *
 * Beta scope:
 *   - List + Run + per-step transcript only. No editor, no inputs
 *     form (the bundled workflow takes a single optional `task`
 *     string the user can type into a textbox).
 *   - Synchronous Run: the v0.3.0 engine completes in-memory and
 *     returns the full transcript. WP10 will swap to broker-driven
 *     progress once a real model_turn dispatcher is wired.
 */
import { ref, computed, onMounted } from 'vue';
import CanvasHead from '@/shell/CanvasHead.vue';
import {
  createWorkflowsClient,
  type WorkflowsClient,
  type WorkflowsSummary,
  type WorkflowsWorkflow,
  type WorkflowsRunResult,
} from '@/lib/workflowsClient';

const props = defineProps<{
  /** Test seam: tests inject a fake client; production constructs one. */
  client?: WorkflowsClient;
}>();

const client: WorkflowsClient = props.client ?? createWorkflowsClient();

const catalog = ref<WorkflowsSummary[]>([]);
const loading = ref(false);
const loadError = ref<string | null>(null);

const selectedID = ref<string | null>(null);
const selected = ref<WorkflowsWorkflow | null>(null);

const inputs = ref<Record<string, string>>({});
const running = ref(false);
const runResult = ref<WorkflowsRunResult | null>(null);
const runError = ref<string | null>(null);

const hasCatalog = computed(() => catalog.value.length > 0);

async function loadCatalog() {
  loading.value = true;
  loadError.value = null;
  try {
    catalog.value = await client.list();
    if (!selectedID.value && catalog.value.length > 0) {
      await selectWorkflow(catalog.value[0].id);
    }
  } catch (err) {
    loadError.value = err instanceof Error ? err.message : String(err);
  } finally {
    loading.value = false;
  }
}

async function selectWorkflow(id: string) {
  selectedID.value = id;
  runResult.value = null;
  runError.value = null;
  try {
    const w = await client.get(id);
    selected.value = w;
    const seeded: Record<string, string> = {};
    for (const inp of w.inputs ?? []) {
      seeded[inp.name] = inp.default ?? '';
    }
    inputs.value = seeded;
  } catch (err) {
    loadError.value = err instanceof Error ? err.message : String(err);
  }
}

async function runWorkflow() {
  if (!selected.value) return;
  running.value = true;
  runResult.value = null;
  runError.value = null;
  try {
    const result = await client.run(selected.value.id, { ...inputs.value });
    runResult.value = result;
    if (result.error) runError.value = result.error;
  } catch (err) {
    runError.value = err instanceof Error ? err.message : String(err);
  } finally {
    running.value = false;
  }
}

function statusClass(s: string): string {
  if (s === 'completed') return 'text-emerald-400';
  if (s === 'running') return 'text-amber-400';
  if (s === 'failed') return 'text-red-400';
  if (s === 'skipped') return 'text-ink-muted';
  return 'text-ink-muted';
}

onMounted(loadCatalog);
</script>

<template>
  <div>
    <CanvasHead
      number="08"
      section="WORKFLOWS"
      title="Agentic Workflows"
      subtitle="Pre-canned multi-step agent recipes."
    />
    <div class="px-6 py-4 space-y-4" data-testid="workflows-view">
      <div v-if="loading" class="font-ui text-sm text-ink-muted">
        Loading workflows…
      </div>

      <div
        v-else-if="loadError"
        class="rounded-sm border border-red-700 bg-red-950 p-4 font-ui text-sm text-red-200"
        data-testid="workflows-load-error"
      >
        Failed to load workflows: {{ loadError }}
      </div>

      <div
        v-else-if="!hasCatalog"
        class="rounded-sm border border-border-muted bg-surface-1 p-6 max-w-2xl"
      >
        <p class="font-ui text-sm text-ink mb-2">No workflows installed yet.</p>
        <p class="font-ui text-sm text-ink-muted">
          Workflows chain agents together: a plan step, a research step, an
          implement step, a review step.
        </p>
      </div>

      <div v-else class="grid grid-cols-12 gap-4">
        <aside
          class="col-span-4 rounded-sm border border-border-muted bg-surface-1 p-3"
          data-testid="workflows-catalog"
        >
          <h2 class="font-ui text-xs uppercase tracking-wide text-ink-muted mb-2">
            Catalog
          </h2>
          <ul class="space-y-1">
            <li v-for="w in catalog" :key="w.id">
              <button
                type="button"
                class="w-full text-left rounded-sm px-2 py-2 font-ui text-sm hover:bg-surface-2 focus:outline-none focus:ring-1 focus:ring-accent"
                :class="
                  selectedID === w.id
                    ? 'bg-surface-2 text-ink'
                    : 'text-ink-muted'
                "
                :data-testid="`workflow-row-${w.id}`"
                @click="selectWorkflow(w.id)"
              >
                <span class="font-medium text-ink">{{ w.name }}</span>
                <span class="block text-xs text-ink-muted">
                  {{ w.stepCount }} steps · v{{ w.version }} · {{ w.source }}
                </span>
              </button>
            </li>
          </ul>
        </aside>

        <section
          v-if="selected"
          class="col-span-8 rounded-sm border border-border-muted bg-surface-1 p-4 space-y-4"
          data-testid="workflows-detail"
        >
          <header>
            <h3 class="font-ui text-base text-ink">{{ selected.name }}</h3>
            <p
              v-if="selected.description"
              class="font-ui text-sm text-ink-muted whitespace-pre-line"
            >
              {{ selected.description }}
            </p>
          </header>

          <div v-if="selected.inputs && selected.inputs.length > 0" class="space-y-2">
            <h4
              class="font-ui text-xs uppercase tracking-wide text-ink-muted"
            >
              Inputs
            </h4>
            <div
              v-for="inp in selected.inputs"
              :key="inp.name"
              class="space-y-1"
            >
              <label
                class="font-ui text-xs text-ink"
                :for="`workflow-input-${inp.name}`"
              >
                {{ inp.name }}
                <span
                  v-if="inp.required"
                  class="text-red-400"
                  aria-label="required"
                  >*</span
                >
              </label>
              <textarea
                v-if="inp.kind === 'multiline'"
                :id="`workflow-input-${inp.name}`"
                v-model="inputs[inp.name]"
                rows="3"
                class="w-full rounded-sm border border-border-muted bg-surface-2 px-2 py-1 font-mono text-sm text-ink"
              />
              <input
                v-else
                :id="`workflow-input-${inp.name}`"
                v-model="inputs[inp.name]"
                type="text"
                class="w-full rounded-sm border border-border-muted bg-surface-2 px-2 py-1 font-mono text-sm text-ink"
                :data-testid="`workflow-input-${inp.name}`"
              />
            </div>
          </div>

          <div>
            <button
              type="button"
              class="rounded-sm border border-accent bg-accent px-4 py-1.5 font-ui text-sm text-bg hover:opacity-90 disabled:opacity-50"
              :disabled="running"
              data-testid="workflows-run-button"
              @click="runWorkflow"
            >
              {{ running ? 'Running…' : 'Run workflow' }}
            </button>
          </div>

          <div v-if="runError" class="font-ui text-sm text-red-300" data-testid="workflows-run-error">
            {{ runError }}
          </div>

          <div v-if="runResult" data-testid="workflows-run-result" class="space-y-2">
            <h4 class="font-ui text-xs uppercase tracking-wide text-ink-muted">
              Run {{ runResult.runId }} —
              <span :class="statusClass(runResult.status)">{{ runResult.status }}</span>
            </h4>
            <ol class="space-y-2">
              <li
                v-for="(step, idx) in runResult.steps"
                :key="`${step.name}-${idx}`"
                class="rounded-sm border border-border-muted bg-surface-2 p-2"
                :data-testid="`workflows-step-${step.name}`"
              >
                <div class="flex items-baseline justify-between">
                  <span class="font-ui text-sm text-ink">
                    {{ idx + 1 }}. {{ step.name }}
                    <span class="text-xs text-ink-muted">({{ step.kind }})</span>
                  </span>
                  <span
                    class="font-ui text-xs"
                    :class="statusClass(step.status)"
                  >
                    {{ step.status }}
                  </span>
                </div>
                <pre
                  v-if="step.output"
                  class="mt-1 whitespace-pre-wrap font-mono text-xs text-ink-muted"
                >{{ step.output }}</pre>
                <p
                  v-if="step.error"
                  class="mt-1 font-ui text-xs text-red-300"
                >
                  {{ step.error }}
                </p>
              </li>
            </ol>
          </div>
        </section>
      </div>
    </div>
  </div>
</template>
