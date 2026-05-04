<script setup lang="ts">
/**
 * WorkflowEditor — full-YAML editor for the v0.3.0-beta workflows
 * surface (mission workflows-01KQ8TDG, WP09).
 *
 * Beta scope (intentionally minimal):
 *   - Styled <textarea> with monospace font + a left gutter line-number
 *     column (no Monaco/Codemirror dependency yet — the task spec
 *     explicitly OKs this for beta).
 *   - Save → client.workflows.save({yaml}). On validation failure the
 *     backend message is surfaced inline; if the message references a
 *     specific step name, we extract it for an extra-visible hint.
 *   - "Run from editor" saves first, then triggers run() with empty
 *     inputs (the catalog page is the place for typed input forms in
 *     beta — runs from the editor are smoke runs).
 *   - Cancel returns to the catalog without persisting anything.
 */
import { computed, ref, watch } from 'vue';
import type {
  WorkflowsClient,
  WorkflowsRunResult,
  WorkflowsSaveOutput,
} from '@/lib/workflowsClient';

const props = defineProps<{
  client: WorkflowsClient;
  /** Pre-fill the textarea (edit-existing path). Empty string = blank canvas. */
  initialYaml?: string;
  /** Optional human label shown in the header (e.g. workflow name). */
  title?: string;
}>();

const emit = defineEmits<{
  (e: 'cancel'): void;
  (e: 'saved', payload: WorkflowsSaveOutput): void;
  (e: 'ran', payload: { save: WorkflowsSaveOutput; run: WorkflowsRunResult }): void;
}>();

const yaml = ref<string>(props.initialYaml ?? '');
watch(
  () => props.initialYaml,
  (next) => {
    if (typeof next === 'string') yaml.value = next;
  },
);

const saving = ref(false);
const running = ref(false);
const saveError = ref<string | null>(null);
const offendingStep = ref<string | null>(null);

const lineCount = computed(() => Math.max(1, yaml.value.split('\n').length));
const lineNumbers = computed(() =>
  Array.from({ length: lineCount.value }, (_, i) => i + 1),
);

/**
 * extractOffendingStep — the backend validator wraps step errors as
 * either `step <name>: ...` or `step "<name>" ...`. Pull the name out
 * for an inline hint; fall back to null if the message is generic.
 */
function extractOffendingStep(msg: string): string | null {
  const m =
    msg.match(/step\s+"([^"]+)"/i) ||
    msg.match(/step\s+([A-Za-z0-9_.-]+)\s*[:\s]/i);
  return m ? m[1] : null;
}

async function save(): Promise<WorkflowsSaveOutput | null> {
  saving.value = true;
  saveError.value = null;
  offendingStep.value = null;
  try {
    const out = await props.client.save({ yaml: yaml.value });
    emit('saved', out);
    return out;
  } catch (err) {
    const msg = err instanceof Error ? err.message : String(err);
    saveError.value = msg;
    offendingStep.value = extractOffendingStep(msg);
    return null;
  } finally {
    saving.value = false;
  }
}

async function runFromEditor() {
  running.value = true;
  try {
    const out = await save();
    if (!out) return;
    const result = await props.client.run(out.id, {});
    emit('ran', { save: out, run: result });
  } catch (err) {
    saveError.value = err instanceof Error ? err.message : String(err);
  } finally {
    running.value = false;
  }
}

function cancel() {
  emit('cancel');
}
</script>

<template>
  <div
    class="rounded-sm border border-border-muted bg-surface-1 p-4 space-y-3"
    data-testid="workflow-editor"
  >
    <header class="flex items-baseline justify-between">
      <div>
        <h3 class="font-ui text-base text-ink">
          {{ title || 'Workflow editor (YAML)' }}
        </h3>
        <p class="font-ui text-xs text-ink-muted">
          Edit raw YAML. Save validates against the engine schema before
          persisting.
        </p>
      </div>
      <span
        class="font-ui text-[10px] uppercase tracking-wide text-amber-300"
        aria-label="beta surface"
      >
        beta
      </span>
    </header>

    <div
      class="flex rounded-sm border border-border-muted bg-surface-2 overflow-hidden"
    >
      <pre
        aria-hidden="true"
        class="select-none px-2 py-1 font-mono text-xs text-ink-muted text-right border-r border-border-muted"
        data-testid="workflow-editor-gutter"
      ><span v-for="n in lineNumbers" :key="n" class="block">{{ n }}</span></pre>
      <textarea
        v-model="yaml"
        spellcheck="false"
        rows="20"
        class="w-full px-2 py-1 font-mono text-xs text-ink bg-transparent resize-y outline-none"
        data-testid="workflow-editor-textarea"
        aria-label="workflow yaml"
      />
    </div>

    <div
      v-if="saveError"
      class="rounded-sm border border-red-700 bg-red-950 p-2 font-ui text-sm text-red-200"
      data-testid="workflow-editor-error"
    >
      <p>Save failed: {{ saveError }}</p>
      <p
        v-if="offendingStep"
        class="mt-1 text-xs text-red-300"
        data-testid="workflow-editor-error-step"
      >
        Offending step: <code class="font-mono">{{ offendingStep }}</code>
      </p>
    </div>

    <div class="flex items-center gap-2">
      <button
        type="button"
        class="rounded-sm border border-accent bg-accent px-4 py-1.5 font-ui text-sm text-bg hover:opacity-90 disabled:opacity-50"
        :disabled="saving || running"
        data-testid="workflow-editor-save"
        @click="save"
      >
        {{ saving ? 'Saving…' : 'Save' }}
      </button>
      <button
        type="button"
        class="rounded-sm border border-border-muted bg-surface-2 px-3 py-1.5 font-ui text-sm text-ink hover:bg-surface-1 disabled:opacity-50"
        :disabled="saving || running"
        data-testid="workflow-editor-run"
        @click="runFromEditor"
      >
        {{ running ? 'Running…' : 'Save + Run' }}
      </button>
      <button
        type="button"
        class="rounded-sm border border-border-muted px-3 py-1.5 font-ui text-sm text-ink-muted hover:text-ink"
        data-testid="workflow-editor-cancel"
        @click="cancel"
      >
        Cancel
      </button>
    </div>
  </div>
</template>
