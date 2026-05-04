<script setup lang="ts">
/**
 * SimpleTemplateEditor — form-driven workflow author for non-power
 * users (mission workflows-01KQ8TDG, WP09).
 *
 * The user picks a starter template, fills in a small set of fields,
 * and we assemble a YAML document that the engine can validate. For
 * v0.3.0 beta the catalog is intentionally tiny (3 templates):
 *
 *   1. single_llm    — one model_turn step.
 *   2. plan_execute  — two model_turn steps chained (plan, execute).
 *   3. http_save     — http_request → write_artifact pipeline.
 *
 * The output YAML is a string built by hand (no js-yaml dependency
 * needed for templates this small) and routed through the same
 * client.save({yaml}) path as the YAML editor so the backend validator
 * is the single source of truth.
 */
import { computed, ref } from 'vue';
import type {
  WorkflowsClient,
  WorkflowsSaveOutput,
} from '@/lib/workflowsClient';

const props = defineProps<{
  client: WorkflowsClient;
}>();

const emit = defineEmits<{
  (e: 'cancel'): void;
  (e: 'saved', payload: WorkflowsSaveOutput): void;
}>();

type TemplateId = 'single_llm' | 'plan_execute' | 'http_save';

interface TemplateMeta {
  id: TemplateId;
  label: string;
  blurb: string;
  needsModel: boolean;
  needsPrompt: boolean;
  needsUrl: boolean;
  needsArtifact: boolean;
}

const TEMPLATES: TemplateMeta[] = [
  {
    id: 'single_llm',
    label: 'Single LLM call',
    blurb: 'One model_turn step — useful for one-shot prompts.',
    needsModel: true,
    needsPrompt: true,
    needsUrl: false,
    needsArtifact: false,
  },
  {
    id: 'plan_execute',
    label: 'Two-step plan-then-execute',
    blurb: 'A planning model_turn followed by an execution model_turn.',
    needsModel: true,
    needsPrompt: true,
    needsUrl: false,
    needsArtifact: false,
  },
  {
    id: 'http_save',
    label: 'HTTP GET → save artifact',
    blurb: 'Fetch a URL with http_request and persist the body as an artifact.',
    needsModel: false,
    needsPrompt: false,
    needsUrl: true,
    needsArtifact: true,
  },
];

const templateId = ref<TemplateId>('single_llm');
const name = ref<string>('');
const description = ref<string>('');
const model = ref<string>('claude-3-5-sonnet');
const promptTemplate = ref<string>('');
const url = ref<string>('https://example.com/');
const artifactPath = ref<string>('out.txt');

const saving = ref(false);
const saveError = ref<string | null>(null);

const meta = computed<TemplateMeta>(
  () => TEMPLATES.find((t) => t.id === templateId.value) as TemplateMeta,
);

/**
 * yamlString — escape a value for a single-quoted YAML scalar.
 * Single-quoted strings only need ' doubled; we sidestep multiline by
 * preferring block scalars for the prompt template instead.
 */
function yamlString(v: string): string {
  return `'${v.replace(/'/g, "''")}'`;
}

function blockScalar(label: string, value: string, indent = 6): string {
  const pad = ' '.repeat(indent);
  const body = value
    .split('\n')
    .map((line) => `${pad}${line}`)
    .join('\n');
  return `${label}: |\n${body}`;
}

/**
 * assembleYaml — build the output YAML from the chosen template +
 * form values. Exposed via defineExpose so tests can drive it without
 * routing through the client.save round-trip.
 */
function assembleYaml(): string {
  const id = (name.value || `wf-${Date.now()}`)
    .toLowerCase()
    .replace(/[^a-z0-9._-]+/g, '-')
    .replace(/^-+|-+$/g, '');
  const header =
    `id: ${yamlString(id)}\n` +
    `name: ${yamlString(name.value || id)}\n` +
    (description.value ? `description: ${yamlString(description.value)}\n` : '') +
    `version: 1\n`;

  if (templateId.value === 'single_llm') {
    return (
      header +
      `steps:\n` +
      `  - name: respond\n` +
      `    kind: model_turn\n` +
      `    model: ${yamlString(model.value)}\n` +
      `    ${blockScalar('user_prompt', promptTemplate.value || 'Say hello.')}\n`
    );
  }

  if (templateId.value === 'plan_execute') {
    return (
      header +
      `steps:\n` +
      `  - name: plan\n` +
      `    kind: model_turn\n` +
      `    model: ${yamlString(model.value)}\n` +
      `    ${blockScalar('user_prompt', `Plan: ${promptTemplate.value || 'the task'}`)}\n` +
      `  - name: execute\n` +
      `    kind: model_turn\n` +
      `    model: ${yamlString(model.value)}\n` +
      `    ${blockScalar('user_prompt', `Execute the plan from {{ steps.plan.output }}`)}\n`
    );
  }

  // http_save
  return (
    header +
    `steps:\n` +
    `  - name: fetch\n` +
    `    kind: http_request\n` +
    `    method: GET\n` +
    `    url: ${yamlString(url.value)}\n` +
    `  - name: save\n` +
    `    kind: write_artifact\n` +
    `    path: ${yamlString(artifactPath.value)}\n` +
    `    content: '{{ steps.fetch.body }}'\n`
  );
}

async function save() {
  if (!name.value.trim()) {
    saveError.value = 'Workflow name is required.';
    return;
  }
  saving.value = true;
  saveError.value = null;
  try {
    const yaml = assembleYaml();
    const out = await props.client.save({ yaml });
    emit('saved', out);
  } catch (err) {
    saveError.value = err instanceof Error ? err.message : String(err);
  } finally {
    saving.value = false;
  }
}

function cancel() {
  emit('cancel');
}

// Test seam — the spec drives YAML assembly directly to assert the
// shape of the output without depending on client.save's plumbing.
defineExpose({ assembleYaml });
</script>

<template>
  <div
    class="rounded-sm border border-border-muted bg-surface-1 p-4 space-y-3"
    data-testid="workflow-template-editor"
  >
    <header class="flex items-baseline justify-between">
      <div>
        <h3 class="font-ui text-base text-ink">New workflow from template</h3>
        <p class="font-ui text-xs text-ink-muted">
          Pick a starter shape, fill in the blanks, save.
        </p>
      </div>
      <span
        class="font-ui text-[10px] uppercase tracking-wide text-amber-300"
        aria-label="beta surface"
      >
        beta
      </span>
    </header>

    <div class="space-y-2">
      <label
        class="font-ui text-xs uppercase tracking-wide text-ink-muted"
        for="template-picker"
      >
        Template
      </label>
      <select
        id="template-picker"
        v-model="templateId"
        class="w-full rounded-sm border border-border-muted bg-surface-2 px-2 py-1 font-ui text-sm text-ink"
        data-testid="template-picker"
      >
        <option v-for="t in TEMPLATES" :key="t.id" :value="t.id">
          {{ t.label }}
        </option>
      </select>
      <p class="font-ui text-xs text-ink-muted">{{ meta.blurb }}</p>
    </div>

    <div class="grid grid-cols-2 gap-3">
      <div>
        <label class="font-ui text-xs text-ink" for="wf-name">Name</label>
        <input
          id="wf-name"
          v-model="name"
          type="text"
          class="w-full rounded-sm border border-border-muted bg-surface-2 px-2 py-1 font-mono text-sm text-ink"
          data-testid="template-name"
        />
      </div>
      <div>
        <label class="font-ui text-xs text-ink" for="wf-desc">Description</label>
        <input
          id="wf-desc"
          v-model="description"
          type="text"
          class="w-full rounded-sm border border-border-muted bg-surface-2 px-2 py-1 font-mono text-sm text-ink"
          data-testid="template-description"
        />
      </div>
    </div>

    <div v-if="meta.needsModel">
      <label class="font-ui text-xs text-ink" for="wf-model">Model</label>
      <input
        id="wf-model"
        v-model="model"
        type="text"
        class="w-full rounded-sm border border-border-muted bg-surface-2 px-2 py-1 font-mono text-sm text-ink"
        data-testid="template-model"
      />
    </div>

    <div v-if="meta.needsPrompt">
      <label class="font-ui text-xs text-ink" for="wf-prompt">
        Prompt template
      </label>
      <textarea
        id="wf-prompt"
        v-model="promptTemplate"
        rows="4"
        class="w-full rounded-sm border border-border-muted bg-surface-2 px-2 py-1 font-mono text-xs text-ink"
        data-testid="template-prompt"
      />
    </div>

    <div v-if="meta.needsUrl">
      <label class="font-ui text-xs text-ink" for="wf-url">URL</label>
      <input
        id="wf-url"
        v-model="url"
        type="text"
        class="w-full rounded-sm border border-border-muted bg-surface-2 px-2 py-1 font-mono text-sm text-ink"
        data-testid="template-url"
      />
    </div>

    <div v-if="meta.needsArtifact">
      <label class="font-ui text-xs text-ink" for="wf-artifact">
        Artifact path
      </label>
      <input
        id="wf-artifact"
        v-model="artifactPath"
        type="text"
        class="w-full rounded-sm border border-border-muted bg-surface-2 px-2 py-1 font-mono text-sm text-ink"
        data-testid="template-artifact"
      />
    </div>

    <div
      v-if="saveError"
      class="rounded-sm border border-red-700 bg-red-950 p-2 font-ui text-sm text-red-200"
      data-testid="template-editor-error"
    >
      {{ saveError }}
    </div>

    <div class="flex items-center gap-2">
      <button
        type="button"
        class="rounded-sm border border-accent bg-accent px-4 py-1.5 font-ui text-sm text-bg hover:opacity-90 disabled:opacity-50"
        :disabled="saving"
        data-testid="template-save"
        @click="save"
      >
        {{ saving ? 'Saving…' : 'Save workflow' }}
      </button>
      <button
        type="button"
        class="rounded-sm border border-border-muted px-3 py-1.5 font-ui text-sm text-ink-muted hover:text-ink"
        data-testid="template-cancel"
        @click="cancel"
      >
        Cancel
      </button>
    </div>
  </div>
</template>
