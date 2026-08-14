<script setup lang="ts">
/**
 * WorkflowGraphEditor — workflow authoring on the SHARED canvas
 * (visual-graph-authoring-01PMUX01 WP06, FR-001 / FR-006).
 *
 * This component used to be a self-described "visual DAG canvas" that
 * was actually one SVG rect per step in declaration order, with ↑/↓
 * buttons for reordering, zero drag handling and no way to express a
 * dependency at all — while `core/workflows` has had a real DAG engine
 * (`inputs_from`, `topoSort`, cycle detection) the whole time. That
 * implementation is DELETED, not wrapped: the mission's no-partial rule
 * means no parallel editor may survive, and the SVG was the parallel one.
 *
 * What is left is a shell. The canvas is `GraphCanvas`, the same
 * component the agentgraph editor mounts, driven by
 * `buildWorkflowAdapter` — the second consumer the `CanvasAdapter` paper
 * check was designed against. Everything workflow-shaped lives in that
 * adapter; this file holds the draft, the metadata inputs, the step-config
 * form, and the save emit.
 *
 * ONE MODEL: `draft` is the only state. Canvas ops and form edits are
 * both `SpecOp`s applied by `applyOpToWorkflow`, which returns a new
 * workflow — so there is no second representation to drift, exactly as
 * on the agentgraph side.
 *
 * SAVE is unchanged: the component still emits `save` with a
 * `WorkflowsWorkflow` and the parent still calls `Workflows_Save`.
 */
import { computed, ref, watch } from 'vue';
import { stringify } from 'yaml';

import GraphCanvas from '@/components/canvas/GraphCanvas.vue';
import {
  EDITABLE_STEP_FIELDS,
  WIRE_STEP_FIELDS,
  WORKFLOW_STEP_KINDS,
  applyOpToWorkflow,
  buildWorkflowAdapter,
  droppedFieldsIn,
  lossyKindsIn,
} from '@/lib/canvas/workflowAdapter';
import type { SpecOp } from '@/lib/canvas/types';
import type { WorkflowsStep, WorkflowsWorkflow } from '@/lib/workflowsClient';

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

// ── the one model ────────────────────────────────────────────────────

function blankDraft(): WorkflowsWorkflow {
  return { id: '', name: '', version: 1, steps: [] };
}

function draftFrom(wf: WorkflowsWorkflow | null): WorkflowsWorkflow {
  if (!wf) return blankDraft();
  return {
    ...wf,
    steps: wf.steps.map((s) => ({
      ...s,
      ...(s.inputsFrom ? { inputsFrom: [...s.inputsFrom] } : {}),
      ...(s.args ? { args: [...s.args] } : {}),
    })),
  };
}

const draft = ref<WorkflowsWorkflow>(draftFrom(props.workflow));
const selectedName = ref('');

watch(
  () => props.workflow,
  (wf) => {
    draft.value = draftFrom(wf);
    selectedName.value = '';
  },
);

/**
 * `id` / `name` are workflow metadata rather than graph structure, so
 * they are plain v-model bindings onto the one draft instead of ops —
 * there is no canvas gesture that could set them, and no reference
 * rewriting to do when they change.
 */
const workflowId = computed({
  get: () => draft.value.id,
  set: (v: string) => {
    draft.value = { ...draft.value, id: v };
  },
});
const workflowName = computed({
  get: () => draft.value.name,
  set: (v: string) => {
    draft.value = { ...draft.value, name: v };
  },
});

function applyOp(op: SpecOp) {
  if (props.readonly) return;
  const result = applyOpToWorkflow(draft.value, op);
  draft.value = result.workflow;
  if (result.selected) selectedName.value = result.selected;
  if (op.type === 'delete-node' && selectedName.value === op.id) {
    selectedName.value = '';
  }
}

const adapter = computed(() =>
  buildWorkflowAdapter({
    workflow: draft.value,
    readOnly: props.readonly,
    applyOp,
  }),
);

// ── selection + step config ──────────────────────────────────────────

const selectedStep = computed<WorkflowsStep | null>(
  () => draft.value.steps.find((s) => s.name === selectedName.value) ?? null,
);

/** Fields the wire round-trips for the selected kind; see the adapter. */
const editableFields = computed<readonly string[]>(() =>
  selectedStep.value ? (EDITABLE_STEP_FIELDS[selectedStep.value.kind] ?? []) : [],
);

function has(field: string): boolean {
  return editableFields.value.includes(field);
}

/**
 * Every form control writes through a `set-attrs` op — the same path a
 * canvas gesture takes into the same draft. That is what keeps the form
 * and the canvas from becoming two models of one workflow, and it is why
 * a rename here rewrites the dependents' `inputs_from` (the adapter does
 * it) instead of quietly orphaning them.
 */
function setField(field: string, value: unknown) {
  const step = selectedStep.value;
  if (!step) return;
  applyOp({ type: 'set-attrs', id: step.name, attrs: { [field]: value } });
}

function onRename(ev: Event) {
  const next = (ev.target as HTMLInputElement).value;
  const step = selectedStep.value;
  if (!step) return;
  applyOp({ type: 'set-attrs', id: step.name, attrs: { name: next } });
}

function onArgs(ev: Event) {
  const raw = (ev.target as HTMLInputElement).value;
  setField(
    'args',
    raw
      .split(',')
      .map((s) => s.trim())
      .filter(Boolean),
  );
}

function onCanvasSelect(id: string) {
  selectedName.value = id;
}

// ── palette ──────────────────────────────────────────────────────────

/** The MIME `GraphCanvas` reads on drop; one constant, one convention. */
const KIND_MIME = 'application/x-kenaz-node-kind';

function onPaletteDragStart(ev: DragEvent, kind: string) {
  if (props.readonly || !ev.dataTransfer) return;
  ev.dataTransfer.setData(KIND_MIME, kind);
  ev.dataTransfer.setData('text/plain', kind);
  ev.dataTransfer.effectAllowed = 'copy';
}

/** Click adds too, so the palette works without a pointer drag. */
function onPaletteClick(kind: string) {
  applyOp({ type: 'add-node', kind, position: { x: 0, y: 0 } });
}

// ── lossiness warning ────────────────────────────────────────────────

/**
 * The wire `Step` is a subset of the Go one, so a structured save
 * reconstructs a workflow without the per-kind config it does not carry.
 * That predates this WP and the old editor inherited it in silence. It
 * is not fixed here — widening the wire to the full ~45-field Step is
 * its own change — but it is no longer silent.
 *
 * The banner states what SURVIVES rather than what is lost. Every step
 * kind loses something (see `UNREPRESENTED_FIELDS_BY_KIND`), so a list
 * of lossy kinds would be a list of all of them and would read as
 * noise; the nine surviving fields are short, exact, and checkable.
 */
const lossyKinds = computed(() => lossyKindsIn(draft.value));
const droppedFields = computed(() => droppedFieldsIn(draft.value));

// ── YAML preview ─────────────────────────────────────────────────────

const showYAML = ref(false);

/**
 * Rendered by the `yaml` library rather than by string concatenation.
 * The hand-rolled emitter this replaces quoted by doubling `'`, which
 * is right for single-quoted YAML and wrong for the block scalar it also
 * emitted, and it could not represent `inputs_from` at all.
 */
const yamlPreview = computed<string>(() => {
  const wf = draft.value;
  const doc: Record<string, unknown> = { id: wf.id, name: wf.name };
  if (wf.description) doc.description = wf.description;
  doc.version = wf.version;
  doc.steps = wf.steps.map((s) => {
    const out: Record<string, unknown> = { name: s.name, kind: s.kind };
    if (s.inputsFrom && s.inputsFrom.length > 0) out.inputs_from = s.inputsFrom;
    if (s.userPrompt) out.user_prompt = s.userPrompt;
    if (s.cmd) out.cmd = s.cmd;
    if (s.args && s.args.length > 0) out.args = s.args;
    if (s.method) out.method = s.method;
    if (s.url) out.url = s.url;
    if (s.mode) out.mode = s.mode;
    return out;
  });
  return stringify(doc, { lineWidth: 0 });
});

// ── validation ───────────────────────────────────────────────────────

const validationErrors = computed<string[]>(() => {
  const errs: string[] = [];
  const wf = draft.value;
  if (!wf.id.trim()) errs.push('Workflow ID is required.');
  if (!wf.name.trim()) errs.push('Workflow name is required.');
  if (wf.steps.length === 0) errs.push('At least one step is required.');
  const names = new Set<string>();
  for (const s of wf.steps) {
    if (!s.name.trim()) errs.push('A step is missing a name.');
    else if (names.has(s.name)) errs.push(`Duplicate step name: "${s.name}".`);
    else names.add(s.name);
  }
  return errs;
});

function handleSave() {
  if (validationErrors.value.length > 0) return;
  emit('save', draftFrom(draft.value));
}

defineExpose({ applyOp, adapter, draft, selectedName });
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

    <div
      v-if="lossyKinds.length > 0 && !readonly"
      class="rounded-sm border border-signal-warn bg-surface-1 px-3 py-2 font-ui text-xs text-signal-warn"
      data-testid="wge-lossy-warning"
      role="status"
    >
      Saving from this editor rebuilds the workflow from the only fields it can
      carry — <span data-testid="wge-lossy-survivors">{{ WIRE_STEP_FIELDS.join(', ') }}</span>.
      Anything else is dropped; in this workflow that means
      <span data-testid="wge-lossy-dropped">{{ droppedFields.join(', ') }}</span>.
      Use the YAML editor for those.
    </div>

    <!-- Three-column editor body -->
    <div class="grid grid-cols-[160px_1fr_220px] gap-3 min-h-[300px]">
      <!-- Left: step-kind palette -->
      <aside
        class="rounded-sm border border-border-muted bg-surface-1 p-2 overflow-y-auto"
        data-testid="wge-palette"
      >
        <p class="font-ui text-[10px] uppercase tracking-widest text-ink-muted mb-2">Steps</p>
        <ul class="space-y-1">
          <li v-for="item in WORKFLOW_STEP_KINDS" :key="item.kind">
            <button
              type="button"
              :disabled="readonly"
              :draggable="!readonly"
              class="w-full text-left rounded-sm px-2 py-1.5 font-ui text-xs text-ink hover:bg-surface-2 disabled:opacity-40 flex items-center gap-2"
              :data-testid="`wge-palette-${item.kind}`"
              :title="item.description"
              @dragstart="onPaletteDragStart($event, item.kind)"
              @click="onPaletteClick(item.kind)"
            >
              <span
                class="inline-block w-2 h-2 rounded-full flex-shrink-0"
                :class="{
                  'bg-accent': item.category === 'compute',
                  'bg-signal-warn': item.category === 'control',
                  'bg-signal-ok': item.category === 'state',
                }"
              />
              {{ item.label }}
            </button>
          </li>
        </ul>
      </aside>

      <!-- Center: THE shared canvas -->
      <div data-testid="wge-canvas">
        <GraphCanvas
          :adapter="adapter"
          :selected-node-id="selectedName"
          @select-node="onCanvasSelect"
        />
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
            <div>
              <label class="font-ui text-xs text-ink-muted block mb-1" for="wge-step-name-input">
                Step name
              </label>
              <input
                id="wge-step-name-input"
                :value="selectedStep.name"
                type="text"
                :disabled="readonly"
                class="w-full rounded-sm border border-border-muted bg-surface-2 px-2 py-1 font-mono text-xs text-ink disabled:opacity-50"
                data-testid="wge-step-name"
                @change="onRename"
              />
            </div>

            <div v-if="has('userPrompt')">
              <label class="font-ui text-xs text-ink-muted block mb-1" for="wge-step-prompt-input">
                User prompt
              </label>
              <textarea
                id="wge-step-prompt-input"
                :value="selectedStep.userPrompt ?? ''"
                :disabled="readonly"
                rows="5"
                class="w-full rounded-sm border border-border-muted bg-surface-2 px-2 py-1 font-mono text-xs text-ink resize-none disabled:opacity-50"
                data-testid="wge-step-user-prompt"
                @input="setField('userPrompt', ($event.target as HTMLTextAreaElement).value)"
              />
            </div>

            <div v-if="has('cmd')">
              <label class="font-ui text-xs text-ink-muted block mb-1" for="wge-step-cmd-input">
                Command
              </label>
              <input
                id="wge-step-cmd-input"
                :value="selectedStep.cmd ?? ''"
                type="text"
                :disabled="readonly"
                class="w-full rounded-sm border border-border-muted bg-surface-2 px-2 py-1 font-mono text-xs text-ink disabled:opacity-50"
                data-testid="wge-step-cmd"
                @input="setField('cmd', ($event.target as HTMLInputElement).value)"
              />
            </div>

            <div v-if="has('args')">
              <label class="font-ui text-xs text-ink-muted block mb-1" for="wge-step-args-input">
                Args (comma-separated)
              </label>
              <input
                id="wge-step-args-input"
                :value="(selectedStep.args ?? []).join(', ')"
                type="text"
                :disabled="readonly"
                class="w-full rounded-sm border border-border-muted bg-surface-2 px-2 py-1 font-mono text-xs text-ink disabled:opacity-50"
                data-testid="wge-step-args"
                @input="onArgs"
              />
            </div>

            <div v-if="has('method')">
              <label class="font-ui text-xs text-ink-muted block mb-1" for="wge-step-method-input">
                Method
              </label>
              <select
                id="wge-step-method-input"
                :value="selectedStep.method ?? ''"
                :disabled="readonly"
                class="w-full rounded-sm border border-border-muted bg-surface-2 px-2 py-1 font-mono text-xs text-ink disabled:opacity-50"
                data-testid="wge-step-method"
                @change="setField('method', ($event.target as HTMLSelectElement).value)"
              >
                <option value="">—</option>
                <option value="GET">GET</option>
                <option value="POST">POST</option>
                <option value="PUT">PUT</option>
                <option value="DELETE">DELETE</option>
              </select>
            </div>

            <div v-if="has('url')">
              <label class="font-ui text-xs text-ink-muted block mb-1" for="wge-step-url-input">
                URL
              </label>
              <input
                id="wge-step-url-input"
                :value="selectedStep.url ?? ''"
                type="url"
                :disabled="readonly"
                class="w-full rounded-sm border border-border-muted bg-surface-2 px-2 py-1 font-mono text-xs text-ink disabled:opacity-50"
                data-testid="wge-step-url"
                @input="setField('url', ($event.target as HTMLInputElement).value)"
              />
            </div>

            <div v-if="has('mode')">
              <label class="font-ui text-xs text-ink-muted block mb-1" for="wge-step-mode-input">
                Mode
              </label>
              <select
                id="wge-step-mode-input"
                :value="selectedStep.mode ?? ''"
                :disabled="readonly"
                class="w-full rounded-sm border border-border-muted bg-surface-2 px-2 py-1 font-mono text-xs text-ink disabled:opacity-50"
                data-testid="wge-step-mode"
                @change="setField('mode', ($event.target as HTMLSelectElement).value)"
              >
                <option value="">—</option>
                <option value="css">CSS extractor</option>
                <option value="llm">LLM extractor</option>
              </select>
            </div>

            <p
              v-if="editableFields.length === 0"
              class="font-ui text-[11px] text-ink-muted"
              data-testid="wge-step-yaml-only"
            >
              Configure this step by editing the workflow YAML directly.
            </p>

            <p class="font-ui text-[11px] text-ink-muted" data-testid="wge-step-deps">
              Depends on:
              {{
                (selectedStep.inputsFrom ?? []).length > 0
                  ? (selectedStep.inputsFrom ?? []).join(', ')
                  : 'nothing — drag from another step to connect.'
              }}
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
        class="font-ui text-xs text-signal-danger"
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
