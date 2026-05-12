<script setup lang="ts">
/**
 * WorkflowGraphEditor — visual DAG canvas for workflow authoring.
 *
 * Mission workflow-extensions-01KW2D3Y, WP02.
 *
 * Layout:
 *   Left column  — node palette (all step kinds)
 *   Center       — SVG canvas (one rect per step, declaration order)
 *   Right column — step config form (per-kind fields)
 *   Footer       — Save / Cancel / View YAML toggle
 *
 * Constraints (C-002): no third-party graph libraries; implemented in
 * pure Vue 3 + SVG.
 */
import { computed, ref, watch } from 'vue';
import type { WorkflowsWorkflow, WorkflowsStep } from '@/lib/workflowsClient';

// ── props / emits ────────────────────────────────────────────────────

const props = withDefaults(
  defineProps<{
    /** Pre-load an existing workflow for editing. null = blank canvas. */
    workflow?: WorkflowsWorkflow | null;
    /** When true, disable editing (used for "view YAML" read-only mode). */
    readonly?: boolean;
  }>(),
  { workflow: null, readonly: false },
);

const emit = defineEmits<{
  /** Emitted when the user clicks Save. Carries the assembled workflow. */
  (e: 'save', wf: WorkflowsWorkflow): void;
  /** Emitted when the user clicks Cancel. */
  (e: 'cancel'): void;
}>();

// ── step-kind palette ────────────────────────────────────────────────

interface PaletteItem {
  kind: string;
  label: string;
  /** Tailwind bg class for the node rect accent bar. */
  color: string;
}

const PALETTE: PaletteItem[] = [
  { kind: 'model_turn',      label: 'Model turn',      color: 'bg-accent' },
  { kind: 'tool_call',       label: 'Tool call',        color: 'bg-violet-600' },
  { kind: 'shell',           label: 'Shell',            color: 'bg-amber-600' },
  { kind: 'http_request',    label: 'HTTP request',     color: 'bg-sky-600' },
  { kind: 'write_artifact',  label: 'Write artifact',   color: 'bg-emerald-600' },
  { kind: 'read_artifact',   label: 'Read artifact',    color: 'bg-teal-600' },
  { kind: 'transform',       label: 'Transform',        color: 'bg-fuchsia-600' },
  { kind: 'conditional',     label: 'Conditional',      color: 'bg-orange-600' },
  { kind: 'web_fetch',       label: 'Web fetch',        color: 'bg-blue-600' },
  { kind: 'web_scrape',      label: 'Web scrape',       color: 'bg-indigo-600' },
  { kind: 'notify',          label: 'Notify',           color: 'bg-pink-600' },
  { kind: 'wait_until',      label: 'Wait until',       color: 'bg-slate-500' },
  { kind: 'aggregate',       label: 'Aggregate',        color: 'bg-lime-600' },
];

function paletteColor(kind: string): string {
  return PALETTE.find((p) => p.kind === kind)?.color ?? 'bg-surface-2';
}

// ── step state ───────────────────────────────────────────────────────

interface GraphStep extends WorkflowsStep {
  // Extended fields used by the editor form but not in the wire shape.
  // On save these are serialised into userPrompt / cmd / args etc.
  userPrompt?: string;
  cmd?: string;
  args?: string[];
  /** For http_request */
  method?: string;
  url?: string;
  /** For web_fetch / web_scrape */
  scrapeMode?: string;
}

// ── workflow metadata ─────────────────────────────────────────────────
// Declared BEFORE the immediate watch so the watch callback doesn't hit
// a temporal dead zone when the prop is set at mount time.

const workflowId = ref(props.workflow?.id ?? '');
const workflowName = ref(props.workflow?.name ?? '');
const workflowDescription = ref(props.workflow?.description ?? '');

const steps = ref<GraphStep[]>(props.workflow?.steps.map((s) => ({ ...s })) ?? []);

// Sync from prop changes (e.g. parent swaps to a different workflow).
watch(
  () => props.workflow,
  (wf) => {
    if (!wf) {
      steps.value = [];
      workflowId.value = '';
      workflowName.value = '';
      workflowDescription.value = '';
      return;
    }
    steps.value = wf.steps.map((s) => ({ ...s }));
    workflowId.value = wf.id;
    workflowName.value = wf.name;
    workflowDescription.value = wf.description ?? '';
  },
);

// ── canvas layout ────────────────────────────────────────────────────

// Each node is rendered as a 200×56 rect arranged vertically with
// 24px gaps. Simple sequential layout for v0.8.5.
const NODE_W = 200;
const NODE_H = 56;
const NODE_GAP = 24;
const CANVAS_PADDING = 16;

const canvasHeight = computed(() =>
  steps.value.length === 0
    ? 120
    : CANVAS_PADDING * 2 + steps.value.length * (NODE_H + NODE_GAP) - NODE_GAP,
);

function nodeY(idx: number): number {
  return CANVAS_PADDING + idx * (NODE_H + NODE_GAP);
}

// ── selection ────────────────────────────────────────────────────────

const selectedIdx = ref<number | null>(null);

const selectedStep = computed<GraphStep | null>(() =>
  selectedIdx.value !== null ? steps.value[selectedIdx.value] ?? null : null,
);

function selectNode(idx: number) {
  if (props.readonly) return;
  selectedIdx.value = idx;
}

// ── mutations ────────────────────────────────────────────────────────

let stepCounter = 0;

function addStep(kind: string) {
  if (props.readonly) return;
  stepCounter++;
  const newStep: GraphStep = {
    name: `step_${stepCounter}`,
    kind,
  };
  steps.value.push(newStep);
  selectedIdx.value = steps.value.length - 1;
}

function removeStep(idx: number) {
  if (props.readonly) return;
  steps.value.splice(idx, 1);
  if (selectedIdx.value !== null) {
    if (selectedIdx.value === idx) {
      selectedIdx.value = null;
    } else if (selectedIdx.value > idx) {
      selectedIdx.value -= 1;
    }
  }
}

function moveUp(idx: number) {
  if (idx === 0) return;
  const arr = steps.value;
  [arr[idx - 1], arr[idx]] = [arr[idx], arr[idx - 1]];
  if (selectedIdx.value === idx) selectedIdx.value = idx - 1;
  else if (selectedIdx.value === idx - 1) selectedIdx.value = idx;
}

function moveDown(idx: number) {
  if (idx >= steps.value.length - 1) return;
  const arr = steps.value;
  [arr[idx], arr[idx + 1]] = [arr[idx + 1], arr[idx]];
  if (selectedIdx.value === idx) selectedIdx.value = idx + 1;
  else if (selectedIdx.value === idx + 1) selectedIdx.value = idx;
}

// ── YAML preview ─────────────────────────────────────────────────────

const showYAML = ref(false);

const yamlPreview = computed<string>(() => {
  const lines: string[] = [];
  lines.push(`id: '${workflowId.value}'`);
  lines.push(`name: '${workflowName.value.replace(/'/g, "''")}'`);
  if (workflowDescription.value) {
    lines.push(`description: '${workflowDescription.value.replace(/'/g, "''")}'`);
  }
  lines.push('steps:');
  for (const s of steps.value) {
    lines.push(`  - name: ${s.name}`);
    lines.push(`    kind: ${s.kind}`);
    if (s.userPrompt) {
      lines.push('    user_prompt: |');
      for (const ln of s.userPrompt.split('\n')) {
        lines.push(`      ${ln}`);
      }
    }
    if (s.cmd) lines.push(`    cmd: '${s.cmd.replace(/'/g, "''")}'`);
    if (s.args && s.args.length > 0) {
      const esc = s.args.map((a) => `'${a.replace(/'/g, "''")}'`).join(', ');
      lines.push(`    args: [${esc}]`);
    }
    if (s.method) lines.push(`    method: '${s.method}'`);
    if (s.url) lines.push(`    url: '${s.url}'`);
    if (s.scrapeMode) lines.push(`    mode: '${s.scrapeMode}'`);
  }
  return lines.join('\n') + '\n';
});

// ── validation ───────────────────────────────────────────────────────

const validationErrors = computed<string[]>(() => {
  const errs: string[] = [];
  if (!workflowId.value.trim()) errs.push('Workflow ID is required.');
  if (!workflowName.value.trim()) errs.push('Workflow name is required.');
  if (steps.value.length === 0) errs.push('At least one step is required.');
  const names = new Set<string>();
  for (const s of steps.value) {
    if (!s.name.trim()) {
      errs.push(`A step is missing a name.`);
    } else if (names.has(s.name)) {
      errs.push(`Duplicate step name: "${s.name}".`);
    } else {
      names.add(s.name);
    }
  }
  return errs;
});

// ── save ─────────────────────────────────────────────────────────────

function handleSave() {
  if (validationErrors.value.length > 0) return;
  const wf: WorkflowsWorkflow = {
    id: workflowId.value,
    name: workflowName.value,
    description: workflowDescription.value || undefined,
    version: props.workflow?.version ?? 1,
    inputs: props.workflow?.inputs,
    steps: steps.value.map((s) => ({
      name: s.name,
      kind: s.kind,
      userPrompt: s.userPrompt,
      cmd: s.cmd,
      args: s.args,
    })),
  };
  emit('save', wf);
}
</script>

<template>
  <div class="flex flex-col gap-4" data-testid="workflow-graph-editor">
    <!-- Metadata row -->
    <div class="grid grid-cols-2 gap-3">
      <div>
        <label class="font-ui text-xs text-ink-muted block mb-1" for="wge-id">Workflow ID</label>
        <input
          id="wge-id"
          v-model="workflowId"
          type="text"
          :disabled="readonly"
          class="w-full rounded-sm border border-border-muted bg-surface-2 px-2 py-1 font-mono text-sm text-ink disabled:opacity-50"
          data-testid="wge-id"
          placeholder="my-workflow"
        />
      </div>
      <div>
        <label class="font-ui text-xs text-ink-muted block mb-1" for="wge-name">Name</label>
        <input
          id="wge-name"
          v-model="workflowName"
          type="text"
          :disabled="readonly"
          class="w-full rounded-sm border border-border-muted bg-surface-2 px-2 py-1 font-ui text-sm text-ink disabled:opacity-50"
          data-testid="wge-name"
          placeholder="My Workflow"
        />
      </div>
    </div>

    <!-- Three-column editor body -->
    <div class="grid grid-cols-[160px_1fr_220px] gap-3 min-h-[300px]">
      <!-- Left: node palette -->
      <aside
        class="rounded-sm border border-border-muted bg-surface-1 p-2 overflow-y-auto"
        data-testid="wge-palette"
      >
        <p class="font-ui text-[10px] uppercase tracking-widest text-ink-muted mb-2">Steps</p>
        <ul class="space-y-1">
          <li v-for="item in PALETTE" :key="item.kind">
            <button
              type="button"
              :disabled="readonly"
              class="w-full text-left rounded-sm px-2 py-1.5 font-ui text-xs text-ink hover:bg-surface-2 disabled:opacity-40 flex items-center gap-2"
              :data-testid="`wge-palette-${item.kind}`"
              @click="addStep(item.kind)"
            >
              <span
                class="inline-block w-2 h-2 rounded-full flex-shrink-0"
                :class="item.color"
              />
              {{ item.label }}
            </button>
          </li>
        </ul>
      </aside>

      <!-- Center: SVG canvas -->
      <div
        class="rounded-sm border border-border-muted bg-surface-1 overflow-auto"
        data-testid="wge-canvas"
      >
        <div
          v-if="steps.length === 0"
          class="flex items-center justify-center h-full min-h-[200px] font-ui text-sm text-ink-muted p-4 text-center"
        >
          Click a step kind from the palette to add it to the canvas.
        </div>
        <svg
          v-else
          :height="canvasHeight"
          :width="NODE_W + CANVAS_PADDING * 2"
          class="block mx-auto"
          role="img"
          :aria-label="`Workflow canvas with ${steps.length} steps`"
        >
          <!-- Arrow connectors between sequential nodes -->
          <line
            v-for="(_, idx) in steps.slice(0, -1)"
            :key="`arrow-${idx}`"
            :x1="CANVAS_PADDING + NODE_W / 2"
            :y1="nodeY(idx) + NODE_H"
            :x2="CANVAS_PADDING + NODE_W / 2"
            :y2="nodeY(idx + 1)"
            stroke="var(--color-border-muted)"
            stroke-width="1.5"
            marker-end="url(#arrowhead)"
          />
          <!-- Arrowhead marker -->
          <defs>
            <marker
              id="arrowhead"
              markerWidth="6"
              markerHeight="6"
              refX="3"
              refY="3"
              orient="auto"
            >
              <path d="M0,0 L0,6 L6,3 z" fill="var(--color-border-muted)" />
            </marker>
          </defs>

          <!-- Step nodes -->
          <g
            v-for="(step, idx) in steps"
            :key="`node-${idx}`"
            :transform="`translate(${CANVAS_PADDING}, ${nodeY(idx)})`"
            :data-testid="`wge-node-${step.name}`"
            style="cursor: pointer"
            @click="selectNode(idx)"
          >
            <!-- Node background -->
            <rect
              :width="NODE_W"
              :height="NODE_H"
              rx="4"
              :fill="selectedIdx === idx ? 'var(--color-surface-2)' : 'var(--color-surface-1)'"
              :stroke="selectedIdx === idx ? 'var(--color-accent)' : 'var(--color-border-muted)'"
              stroke-width="1.5"
            />
            <!-- Accent left bar -->
            <rect
              width="4"
              :height="NODE_H"
              rx="2"
              :class="paletteColor(step.kind)"
              fill="currentColor"
            />
            <!-- Step index badge -->
            <text
              x="16"
              y="18"
              font-size="9"
              fill="var(--color-ink-muted)"
              font-family="monospace"
            >
              {{ idx + 1 }}
            </text>
            <!-- Step name -->
            <text
              x="16"
              y="33"
              font-size="12"
              fill="var(--color-ink)"
              font-weight="600"
              font-family="var(--font-ui)"
            >
              {{ step.name }}
            </text>
            <!-- Step kind -->
            <text
              x="16"
              y="48"
              font-size="10"
              fill="var(--color-ink-muted)"
              font-family="monospace"
            >
              {{ step.kind }}
            </text>
            <!-- Move up / down / delete buttons -->
            <foreignObject v-if="!readonly" :x="NODE_W - 54" y="4" width="50" height="20">
              <div class="flex gap-0.5">
                <button
                  type="button"
                  class="font-ui text-[10px] text-ink-muted hover:text-ink px-1 leading-tight"
                  :disabled="idx === 0"
                  :data-testid="`wge-node-up-${step.name}`"
                  @click.stop="moveUp(idx)"
                >
                  ↑
                </button>
                <button
                  type="button"
                  class="font-ui text-[10px] text-ink-muted hover:text-ink px-1 leading-tight"
                  :disabled="idx === steps.length - 1"
                  :data-testid="`wge-node-down-${step.name}`"
                  @click.stop="moveDown(idx)"
                >
                  ↓
                </button>
                <button
                  type="button"
                  class="font-ui text-[10px] text-red-400 hover:text-red-300 px-1 leading-tight"
                  :data-testid="`wge-node-delete-${step.name}`"
                  @click.stop="removeStep(idx)"
                >
                  ×
                </button>
              </div>
            </foreignObject>
          </g>
        </svg>
      </div>

      <!-- Right: step config form -->
      <aside
        class="rounded-sm border border-border-muted bg-surface-1 p-3 overflow-y-auto"
        data-testid="wge-step-form"
      >
        <div v-if="!selectedStep" class="font-ui text-xs text-ink-muted">
          Select a step on the canvas to configure it.
        </div>
        <template v-else>
          <p class="font-ui text-[10px] uppercase tracking-widest text-ink-muted mb-3">
            Configure: {{ selectedStep.kind }}
          </p>
          <div class="space-y-3">
            <!-- Common: step name -->
            <div>
              <label
                class="font-ui text-xs text-ink-muted block mb-1"
                :for="`wge-step-name-${selectedIdx}`"
              >
                Step name
              </label>
              <input
                :id="`wge-step-name-${selectedIdx}`"
                v-model="selectedStep.name"
                type="text"
                :disabled="readonly"
                class="w-full rounded-sm border border-border-muted bg-surface-2 px-2 py-1 font-mono text-xs text-ink disabled:opacity-50"
                data-testid="wge-step-name"
              />
            </div>

            <!-- model_turn: user_prompt -->
            <div v-if="selectedStep.kind === 'model_turn'">
              <label
                class="font-ui text-xs text-ink-muted block mb-1"
                :for="`wge-step-prompt-${selectedIdx}`"
              >
                User prompt
              </label>
              <textarea
                :id="`wge-step-prompt-${selectedIdx}`"
                v-model="selectedStep.userPrompt"
                :disabled="readonly"
                rows="5"
                class="w-full rounded-sm border border-border-muted bg-surface-2 px-2 py-1 font-mono text-xs text-ink resize-none disabled:opacity-50"
                data-testid="wge-step-user-prompt"
              />
            </div>

            <!-- shell: cmd + args -->
            <template v-else-if="selectedStep.kind === 'shell'">
              <div>
                <label
                  class="font-ui text-xs text-ink-muted block mb-1"
                  :for="`wge-step-cmd-${selectedIdx}`"
                >
                  Command
                </label>
                <input
                  :id="`wge-step-cmd-${selectedIdx}`"
                  v-model="selectedStep.cmd"
                  type="text"
                  :disabled="readonly"
                  class="w-full rounded-sm border border-border-muted bg-surface-2 px-2 py-1 font-mono text-xs text-ink disabled:opacity-50"
                  data-testid="wge-step-cmd"
                />
              </div>
              <div>
                <label
                  class="font-ui text-xs text-ink-muted block mb-1"
                  :for="`wge-step-args-${selectedIdx}`"
                >
                  Args (comma-separated)
                </label>
                <input
                  :id="`wge-step-args-${selectedIdx}`"
                  :value="(selectedStep.args ?? []).join(', ')"
                  type="text"
                  :disabled="readonly"
                  class="w-full rounded-sm border border-border-muted bg-surface-2 px-2 py-1 font-mono text-xs text-ink disabled:opacity-50"
                  data-testid="wge-step-args"
                  @input="(e) => { if (selectedStep) selectedStep.args = (e.target as HTMLInputElement).value.split(',').map(s => s.trim()).filter(Boolean) }"
                />
              </div>
            </template>

            <!-- http_request: method + url -->
            <template v-else-if="selectedStep.kind === 'http_request'">
              <div>
                <label
                  class="font-ui text-xs text-ink-muted block mb-1"
                  :for="`wge-step-method-${selectedIdx}`"
                >
                  Method
                </label>
                <select
                  :id="`wge-step-method-${selectedIdx}`"
                  v-model="selectedStep.method"
                  :disabled="readonly"
                  class="w-full rounded-sm border border-border-muted bg-surface-2 px-2 py-1 font-mono text-xs text-ink disabled:opacity-50"
                  data-testid="wge-step-method"
                >
                  <option value="GET">GET</option>
                  <option value="POST">POST</option>
                  <option value="PUT">PUT</option>
                  <option value="DELETE">DELETE</option>
                </select>
              </div>
              <div>
                <label
                  class="font-ui text-xs text-ink-muted block mb-1"
                  :for="`wge-step-url-${selectedIdx}`"
                >
                  URL
                </label>
                <input
                  :id="`wge-step-url-${selectedIdx}`"
                  v-model="selectedStep.url"
                  type="url"
                  :disabled="readonly"
                  class="w-full rounded-sm border border-border-muted bg-surface-2 px-2 py-1 font-mono text-xs text-ink disabled:opacity-50"
                  data-testid="wge-step-url"
                />
              </div>
            </template>

            <!-- web_scrape: URL + mode -->
            <template v-else-if="selectedStep.kind === 'web_scrape' || selectedStep.kind === 'web_fetch'">
              <div>
                <label
                  class="font-ui text-xs text-ink-muted block mb-1"
                  :for="`wge-step-url-${selectedIdx}`"
                >
                  URL
                </label>
                <input
                  :id="`wge-step-url-${selectedIdx}`"
                  v-model="selectedStep.url"
                  type="url"
                  :disabled="readonly"
                  class="w-full rounded-sm border border-border-muted bg-surface-2 px-2 py-1 font-mono text-xs text-ink disabled:opacity-50"
                  data-testid="wge-step-url"
                />
              </div>
              <div v-if="selectedStep.kind === 'web_scrape'">
                <label
                  class="font-ui text-xs text-ink-muted block mb-1"
                  :for="`wge-step-mode-${selectedIdx}`"
                >
                  Mode
                </label>
                <select
                  :id="`wge-step-mode-${selectedIdx}`"
                  v-model="selectedStep.scrapeMode"
                  :disabled="readonly"
                  class="w-full rounded-sm border border-border-muted bg-surface-2 px-2 py-1 font-mono text-xs text-ink disabled:opacity-50"
                  data-testid="wge-step-mode"
                >
                  <option value="css">CSS extractor</option>
                  <option value="llm">LLM extractor</option>
                </select>
              </div>
            </template>

            <!-- Fallback: hint for unhandled kinds -->
            <p
              v-else
              class="font-ui text-[11px] text-ink-muted"
            >
              Configure this step by editing the workflow YAML directly.
            </p>
          </div>
        </template>
      </aside>
    </div>

    <!-- Validation errors -->
    <ul
      v-if="validationErrors.length > 0 && !readonly"
      class="space-y-1"
      data-testid="wge-errors"
    >
      <li
        v-for="err in validationErrors"
        :key="err"
        class="font-ui text-xs text-red-300"
      >
        {{ err }}
      </li>
    </ul>

    <!-- YAML preview toggle -->
    <div v-if="showYAML" class="rounded-sm border border-border-muted bg-surface-1 p-3">
      <div class="flex items-center justify-between mb-2">
        <span class="font-ui text-xs uppercase tracking-widest text-ink-muted">YAML preview</span>
        <button
          type="button"
          class="font-ui text-xs text-ink-muted hover:text-ink"
          @click="showYAML = false"
        >
          Hide
        </button>
      </div>
      <pre
        class="whitespace-pre-wrap font-mono text-xs text-ink-muted overflow-x-auto"
        data-testid="wge-yaml-preview"
      >{{ yamlPreview }}</pre>
    </div>

    <!-- Footer actions -->
    <div class="flex items-center gap-2 flex-wrap">
      <button
        v-if="!readonly"
        type="button"
        class="rounded-sm border border-accent bg-accent px-4 py-1.5 font-ui text-sm text-bg hover:opacity-90 disabled:opacity-50"
        :disabled="validationErrors.length > 0"
        data-testid="wge-save"
        @click="handleSave"
      >
        Save workflow
      </button>
      <button
        type="button"
        class="rounded-sm border border-border-muted bg-surface-2 px-3 py-1.5 font-ui text-sm text-ink hover:bg-surface-1"
        data-testid="wge-cancel"
        @click="emit('cancel')"
      >
        {{ readonly ? 'Close' : 'Cancel' }}
      </button>
      <button
        type="button"
        class="ml-auto rounded-sm border border-border-muted bg-surface-1 px-3 py-1.5 font-ui text-sm text-ink-muted hover:text-ink"
        data-testid="wge-yaml-toggle"
        @click="showYAML = !showYAML"
      >
        {{ showYAML ? 'Hide YAML' : 'View YAML' }}
      </button>
    </div>
  </div>
</template>
