<script setup lang="ts">
/**
 * GraphEditor — Bundle A WP06 YAML editor for graph specs. Loads a
 * graph by id, lets the user edit the YAML in a textarea, runs the
 * kernel validator on demand, and persists user-scoped graphs back to
 * disk via Graph_SaveGraph. Library-scoped graphs are read-only;
 * SaveGraph rejects collisions and the UI disables Save accordingly.
 *
 * The editor uses a textarea (rather than a Monaco instance) to match
 * the existing repo conventions and keep the bundle size small.
 */
import { computed, onMounted, ref, watch } from 'vue';
import { useRoute, useRouter } from 'vue-router';
import CanvasHead from '@/shell/CanvasHead.vue';
import { useHarnessClient } from '@/lib/useHarnessAPI';
import type {
  GraphSpec,
  GraphValidationResult,
} from '@/lib/types';

const client = useHarnessClient();
const route = useRoute();
const router = useRouter();

const newGraphTemplate = `spec_version: "1"
id: my_graph
name: My Graph
entrypoints: [start]
nodes:
  - id: start
    kind: plan
    attrs:
      verbosity: standard
`;

const id = computed(() => String(route.params.id ?? ''));

const yaml = ref('');
const scope = ref<'library' | 'user'>('user');
const editingId = ref('');
const error = ref<string | null>(null);
const validation = ref<GraphValidationResult | null>(null);
const saved = ref(false);

const readOnly = computed(() => scope.value === 'library');
const canSave = computed(() => !readOnly.value && !!yaml.value.trim());

async function load() {
  error.value = null;
  validation.value = null;
  saved.value = false;
  if (id.value === '__new__') {
    yaml.value = newGraphTemplate;
    scope.value = 'user';
    editingId.value = 'my_graph';
    return;
  }
  try {
    const spec = await client.graph.loadGraph(id.value);
    yaml.value = spec.yaml;
    scope.value = spec.scope;
    editingId.value = spec.id;
  } catch (err) {
    error.value = err instanceof Error ? err.message : String(err);
  }
}

async function validate() {
  error.value = null;
  try {
    validation.value = await client.graph.validate(yaml.value);
  } catch (err) {
    error.value = err instanceof Error ? err.message : String(err);
  }
}

async function save() {
  if (readOnly.value) return;
  saved.value = false;
  error.value = null;
  // Validate first; abort save on red.
  let v: GraphValidationResult;
  try {
    v = await client.graph.validate(yaml.value);
  } catch (err) {
    error.value = err instanceof Error ? err.message : String(err);
    return;
  }
  validation.value = v;
  if (!v.ok) {
    error.value = 'Resolve validation errors before saving.';
    return;
  }
  // Extract id from yaml — the SaveGraph contract requires id == top-level YAML id.
  const matched = /^id:\s*"?(.+?)"?\s*$/m.exec(yaml.value);
  if (matched) {
    editingId.value = matched[1].trim();
  }
  const spec: GraphSpec = {
    id: editingId.value,
    scope: 'user',
    yaml: yaml.value,
  };
  try {
    await client.graph.saveGraph(spec);
    saved.value = true;
  } catch (err) {
    error.value = err instanceof Error ? err.message : String(err);
  }
}

function backToList() {
  void router.push({ name: 'graphs' });
}

onMounted(() => {
  void load();
});

watch(
  () => id.value,
  () => {
    void load();
  },
);

defineExpose({ load, validate, save });
</script>

<template>
  <div>
    <CanvasHead
      number="12"
      section="GRAPHS"
      :title="readOnly ? 'View graph (read-only)' : 'Edit graph'"
      :subtitle="`Editing ${editingId || 'new graph'}`"
    >
      <template #trailing>
        <div class="flex gap-2">
          <button
            type="button"
            class="rounded-sm border border-border-muted px-3 py-1 font-ui text-[12px] uppercase tracking-[0.18em] text-ink-dim hover:bg-surface-2"
            @click="backToList"
          >
            Back
          </button>
          <button
            type="button"
            class="rounded-sm border border-border-muted px-3 py-1 font-ui text-[12px] uppercase tracking-[0.18em] text-ink-dim hover:bg-surface-2"
            data-testid="editor-validate"
            @click="validate"
          >
            Validate
          </button>
          <button
            v-if="!readOnly"
            type="button"
            :disabled="!canSave"
            class="rounded-sm border border-accent bg-surface-2 px-3 py-1 font-ui text-[12px] uppercase tracking-[0.18em] text-accent disabled:opacity-50"
            data-testid="editor-save"
            @click="save"
          >
            Save
          </button>
        </div>
      </template>
    </CanvasHead>

    <div class="px-6 py-4 max-w-5xl space-y-3">
      <div
        v-if="error"
        class="rounded-md border border-signal-danger bg-surface-1 px-3 py-2 font-ui text-[12px] text-signal-danger"
        role="alert"
      >
        {{ error }}
      </div>
      <div
        v-if="saved"
        class="rounded-md border border-signal-ok bg-surface-1 px-3 py-2 font-ui text-[12px] text-signal-ok"
        data-testid="editor-saved"
      >
        Graph saved.
      </div>

      <div
        v-if="readOnly"
        class="rounded-md border border-border-muted bg-surface-1 px-3 py-2 font-ui text-[12px] text-ink-muted"
        data-testid="editor-readonly-banner"
      >
        Library graph — read-only. Copy + change the id to save your own.
      </div>

      <textarea
        v-model="yaml"
        :readonly="readOnly"
        spellcheck="false"
        class="h-[480px] w-full rounded-md border border-border-muted bg-surface-0 px-3 py-2 font-mono text-[12px] text-ink"
        data-testid="editor-yaml"
      />

      <div
        v-if="validation"
        class="rounded-md border border-border-muted bg-surface-1 px-3 py-2"
        data-testid="editor-validation"
      >
        <div
          v-if="validation.ok"
          class="font-ui text-[12px] text-signal-ok"
          data-testid="editor-validation-ok"
        >
          Validation passed.
        </div>
        <ul v-else class="space-y-1 font-ui text-[12px]">
          <li
            v-for="(issue, idx) in validation.issues"
            :key="idx"
            class="text-signal-danger"
            :data-testid="`editor-validation-issue-${idx}`"
          >
            <span
              class="inline-block rounded-sm border border-signal-danger px-1 mr-1 text-[10px] uppercase tracking-[0.18em]"
            >
              {{ issue.rule }}
            </span>
            {{ issue.message }}
          </li>
        </ul>
      </div>
    </div>
  </div>
</template>
