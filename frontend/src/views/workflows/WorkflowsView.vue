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
import { useServedMode } from '@/lib/useServedMode';
import NotAvailableInServedMode from '@/components/ui/NotAvailableInServedMode.vue';
import WorkflowEditor from './WorkflowEditor.vue';
import SimpleTemplateEditor from './SimpleTemplateEditor.vue';
import WorkflowGraphEditor from '@/components/workflows/WorkflowGraphEditor.vue';
import CatalogView from './CatalogView.vue';
import CatalogPreviewDrawer from './CatalogPreviewDrawer.vue';
import RunsHistoryTab from './RunsHistoryTab.vue';
// fleet-share-and-sync-01NDFSEX14 WP03 — Publish to team catalog
import PublishDialog from '@/views/marketplace/PublishDialog.vue';
import { signedIn } from '@/lib/featureFlags';
import { push as pushToast } from '@/composables/useToastQueue';
import {
  createWorkflowsClient,
  type WorkflowsClient,
  type WorkflowsSummary,
  type WorkflowsWorkflow,
  type WorkflowsRunResult,
  type WorkflowsSaveOutput,
  type WorkflowsCatalogEntry,
} from '@/lib/workflowsClient';
import {
  createScheduledChatClient,
  type ScheduledChatClient,
} from '@/lib/scheduledChatClient';

const props = defineProps<{
  /** Test seam: tests inject a fake client; production constructs one. */
  client?: WorkflowsClient;
  /** Test seam: optional chat client for the Runs tab Chat Runs section. */
  chatClient?: ScheduledChatClient;
}>();

const servedMode = useServedMode();
const client: WorkflowsClient = props.client ?? createWorkflowsClient();
// scheduled-chat-runs-01KX5R8B WP06 — production creates a real client;
// tests can inject a fake via the chatClient prop.
const chatClient: ScheduledChatClient = props.chatClient ?? createScheduledChatClient();
// ── publish-to-team state (WP03) ──────────────────────────────────────
const publishDialogOpen = ref(false);

function openPublishDialog() {
  publishDialogOpen.value = true;
}

function onWorkflowPublished() {
  publishDialogOpen.value = false;
  pushToast('Workflow published to team catalog.');
}

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

// WP07: save / delete affordances. The full editor lands in WP09; this
// surface just exposes the client.save / client.remove round-trip so the
// import-from-yaml + delete-stored-workflow flows are reachable end-to-end.
const saving = ref(false);
const saveError = ref<string | null>(null);

async function importFromYaml(yaml: string) {
  if (!yaml.trim()) return;
  saving.value = true;
  saveError.value = null;
  try {
    const out = await client.save({ yaml });
    // Refresh the catalog so the freshly persisted row shows up, and
    // jump to the new selection so the user sees the import landed.
    await loadCatalog();
    await selectWorkflow(out.id);
  } catch (err) {
    saveError.value = err instanceof Error ? err.message : String(err);
  } finally {
    saving.value = false;
  }
}

async function deleteSelected() {
  if (!selected.value) return;
  const id = selected.value.id;
  saving.value = true;
  saveError.value = null;
  try {
    await client.remove(id);
    selectedID.value = null;
    selected.value = null;
    runResult.value = null;
    runError.value = null;
    await loadCatalog();
  } catch (err) {
    saveError.value = err instanceof Error ? err.message : String(err);
  } finally {
    saving.value = false;
  }
}

// Test seam: the spec drives import via this method so it doesn't need
// to wire a hidden file input. Production WP09 will replace it with the
// real editor's save button.
defineExpose({ importFromYaml, deleteSelected });

// WP03: tab navigation. "Library" is the default (preserving existing UX);
// "Catalog" opens the install-flow surface; "Runs" is reserved.
type Tab = 'Library' | 'Catalog' | 'Runs';
const activeTab = ref<Tab>('Library');

// WP03: catalog selection for the preview drawer.
const catalogSelectedEntry = ref<WorkflowsCatalogEntry | null>(null);

function onCatalogInstalled(_workflowId: string) {
  // Refresh the library list so the newly installed workflow shows up.
  // _workflowId currently unused — kept on the signature for the
  // event-shape contract so a future "scroll to / highlight newly-
  // installed row" behaviour doesn't have to change the emit shape.
  void loadCatalog();
  catalogSelectedEntry.value = null;
}

// WP09: editor mode. The catalog is the default surface; "New" or
// "Edit" flips us into one of the editor modes. The editors emit
// `cancel` / `saved` — both routes return us to the catalog and (on
// save) refresh + jump to the freshly persisted workflow.
//
// The `canvas-*` modes are visual-graph-authoring-01PMUX01 WP06's other
// half. `WorkflowGraphEditor` was rebuilt on the shared canvas but this
// view mounted only the template + YAML editors, so FR-006 ("workflows
// render on the shared canvas") was unreachable from the running app —
// built, but unwired. These modes are the wiring.
type EditorMode =
  | null
  | 'yaml-new'
  | 'yaml-edit'
  | 'template'
  | 'canvas-new'
  | 'canvas-edit';
const editorMode = ref<EditorMode>(null);
const newMenuOpen = ref(false);
const editorYaml = ref<string>('');
const editorTitle = ref<string>('');
/** The structured workflow the canvas editor edits; null for a new one. */
const editorWorkflow = ref<WorkflowsWorkflow | null>(null);

/** True in either editor for an EXISTING workflow, where both panes seed
 * from the same saved record and the YAML/Canvas toggle makes sense. */
const canToggleEditor = computed(
  () => editorMode.value === 'yaml-edit' || editorMode.value === 'canvas-edit',
);

/**
 * yamlFromWorkflow — synthesize a YAML stub from the structured
 * `Get` response so the WorkflowEditor can be pre-filled with the
 * existing workflow. v0.3.0-beta does not surface the canonical YAML
 * over the bridge; round-tripping through the structured shape is
 * lossy but it's enough for the typical "tweak a prompt and resave"
 * editor path. WP07's structured-update mode would be cleaner here,
 * but the beta client only routes save-by-yaml.
 */
function yamlFromWorkflow(w: WorkflowsWorkflow): string {
  const lines: string[] = [];
  lines.push(`id: '${w.id}'`);
  lines.push(`name: '${(w.name || '').replace(/'/g, "''")}'`);
  if (w.description) {
    lines.push(`description: '${w.description.replace(/'/g, "''")}'`);
  }
  lines.push(`version: ${w.version || 1}`);
  if (w.inputs && w.inputs.length > 0) {
    lines.push('inputs:');
    for (const inp of w.inputs) {
      lines.push(`  - name: ${inp.name}`);
      lines.push(`    kind: ${inp.kind}`);
      if (inp.required) lines.push('    required: true');
      if (inp.default !== undefined && inp.default !== '') {
        lines.push(`    default: '${inp.default.replace(/'/g, "''")}'`);
      }
    }
  }
  lines.push('steps:');
  for (const s of w.steps) {
    lines.push(`  - name: ${s.name}`);
    lines.push(`    kind: ${s.kind}`);
    if (s.userPrompt) {
      lines.push("    user_prompt: |");
      for (const ln of s.userPrompt.split('\n')) {
        lines.push(`      ${ln}`);
      }
    }
    if (s.cmd) lines.push(`    cmd: '${s.cmd.replace(/'/g, "''")}'`);
    if (s.args && s.args.length > 0) {
      const escaped = s.args.map((a) => `'${a.replace(/'/g, "''")}'`).join(', ');
      lines.push(`    args: [${escaped}]`);
    }
  }
  return lines.join('\n') + '\n';
}

function openTemplateEditor() {
  editorMode.value = 'template';
  newMenuOpen.value = false;
}

// STARTER_YAML — a minimal but runnable template so "New (YAML)" opens on a
// working example rather than a blank canvas. It documents the common fields
// inline (inputs, a model_turn step, {{ }} interpolation) so a first-time
// author has a shape to edit instead of guessing the schema. `model` is left
// commented so the step inherits the default LLM profile until overridden.
const STARTER_YAML = `# New workflow — edit this starter, then Save (validates before persisting).
name: 'My workflow'
description: 'What this workflow does.'
version: 1

# Typed inputs the run form collects (delete if the workflow takes none):
inputs:
  - name: topic
    kind: string
    required: true

steps:
  - name: respond
    kind: model_turn
    # model: claude-sonnet-4-6   # uncomment to override the default profile
    user_prompt: |
      Write a short paragraph about {{ inputs.topic }}.
`;

function openBlankYamlEditor() {
  editorYaml.value = STARTER_YAML;
  editorTitle.value = 'New workflow (YAML)';
  editorMode.value = 'yaml-new';
  newMenuOpen.value = false;
}

function openEditExisting() {
  if (!selected.value) return;
  editorYaml.value = yamlFromWorkflow(selected.value);
  editorWorkflow.value = selected.value;
  editorTitle.value = `Edit ${selected.value.name}`;
  editorMode.value = 'yaml-edit';
}

/**
 * Opens the selected workflow on the SHARED canvas — the same
 * `GraphCanvas` the agentgraph editor mounts (FR-001/FR-006).
 *
 * Both editors seed from the same saved record (`selected`), which is
 * what makes the toggle between them coherent: flipping panes re-reads
 * the workflow as last persisted, so the only thing it can cost is
 * unsaved edits in the pane being left. The toggle says so.
 */
function openCanvasEditor() {
  if (!selected.value) return;
  editorWorkflow.value = selected.value;
  editorYaml.value = yamlFromWorkflow(selected.value);
  editorTitle.value = `Edit ${selected.value.name}`;
  editorMode.value = 'canvas-edit';
}

function openBlankCanvasEditor() {
  editorWorkflow.value = null;
  editorTitle.value = 'New workflow (canvas)';
  editorMode.value = 'canvas-new';
  newMenuOpen.value = false;
}

/** The YAML/Canvas toggle. Only reachable while editing an existing
 * workflow — see `canToggleEditor`. */
function switchEditorPane(pane: 'yaml' | 'canvas') {
  if (!canToggleEditor.value) return;
  if (pane === 'yaml') openEditExisting();
  else openCanvasEditor();
}

function closeEditor() {
  editorMode.value = null;
  editorWorkflow.value = null;
}

/**
 * The canvas editor's save. It goes through the EXISTING structured save
 * path — `client.save({ workflow })`, the same call the pre-canvas
 * editor's emit was always destined for — so the canvas adds a surface,
 * not a second way to persist a workflow.
 */
async function onCanvasSave(wf: WorkflowsWorkflow) {
  saving.value = true;
  saveError.value = null;
  try {
    const out = await client.save({ workflow: wf });
    await onEditorSaved(out);
  } catch (err) {
    saveError.value = err instanceof Error ? err.message : String(err);
  } finally {
    saving.value = false;
  }
}

async function onEditorSaved(out: WorkflowsSaveOutput) {
  editorMode.value = null;
  await loadCatalog();
  await selectWorkflow(out.id);
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
  if (s === 'completed') return 'text-signal-ok';
  if (s === 'running') return 'text-signal-warn';
  if (s === 'failed') return 'text-signal-danger';
  if (s === 'skipped') return 'text-ink-muted';
  return 'text-ink-muted';
}

onMounted(loadCatalog);
</script>

<template>
  <NotAvailableInServedMode
    v-if="servedMode"
    feature="Workflows"
      reason="Workflow definitions and runs are not wired into the in-workbench build yet."
  />
  <div v-else>
    <CanvasHead
      number="08"
      section="WORKFLOWS"
      title="Agentic Workflows"
      subtitle="Pre-canned multi-step agent recipes."
    />
    <div class="px-6 py-4 space-y-4" data-testid="workflows-view">
      <!-- Tab nav (WP03) -->
      <nav class="flex gap-1 border-b border-border-muted" data-testid="workflows-tab-nav">
        <button
          v-for="tab in (['Library', 'Catalog', 'Runs'] as const)"
          :key="tab"
          type="button"
          class="px-4 py-2 font-ui text-sm focus:outline-none"
          :class="
            activeTab === tab
              ? 'border-b-2 border-accent text-ink'
              : 'text-ink-muted hover:text-ink'
          "
          :data-testid="`workflows-tab-${tab.toLowerCase()}`"
          @click="activeTab = tab"
        >
          {{ tab }}
        </button>
      </nav>

      <!-- Catalog tab (WP03) -->
      <template v-if="activeTab === 'Catalog'">
        <CatalogView
          :client="client"
          @select="catalogSelectedEntry = $event"
        />
        <CatalogPreviewDrawer
          :client="client"
          :entry="catalogSelectedEntry"
          @close="catalogSelectedEntry = null"
          @installed="onCatalogInstalled"
          @redirect-providers="activeTab = 'Library'"
        />
      </template>

      <!-- Runs tab (01NBUG04 — execution history + scheduled subsection) -->
      <template v-else-if="activeTab === 'Runs'">
        <RunsHistoryTab :client="client" :chat-client="chatClient" />
      </template>

      <!-- Library tab (existing content) -->
      <template v-else>
      <div class="flex items-center justify-end gap-2 relative">
        <button
          type="button"
          class="rounded-sm border border-accent bg-accent px-3 py-1.5 font-ui text-sm text-bg hover:opacity-90"
          data-testid="workflows-new-button"
          @click="newMenuOpen = !newMenuOpen"
        >
          + New
        </button>
        <div
          v-if="newMenuOpen"
          class="absolute right-0 top-full mt-1 w-56 rounded-sm border border-border-muted bg-surface-1 shadow-lg z-10"
          data-testid="workflows-new-menu"
        >
          <button
            type="button"
            class="block w-full text-left px-3 py-2 font-ui text-sm text-ink hover:bg-surface-2"
            data-testid="workflows-new-template"
            @click="openTemplateEditor"
          >
            From template
          </button>
          <button
            type="button"
            class="block w-full text-left px-3 py-2 font-ui text-sm text-ink hover:bg-surface-2"
            data-testid="workflows-new-yaml"
            @click="openBlankYamlEditor"
          >
            From YAML
          </button>
          <button
            type="button"
            class="block w-full text-left px-3 py-2 font-ui text-sm text-ink hover:bg-surface-2"
            data-testid="workflows-new-canvas"
            @click="openBlankCanvasEditor"
          >
            On the canvas
          </button>
        </div>
      </div>

      <div
        v-if="editorMode === 'template'"
        data-testid="workflows-editor-slot-template"
      >
        <SimpleTemplateEditor
          :client="client"
          @cancel="closeEditor"
          @saved="onEditorSaved"
        />
      </div>

      <!--
        The YAML/Canvas toggle. Both panes edit the same saved workflow
        and save through the same client, so this picks a VIEW of one
        artifact rather than opening a different editor — which is why
        it is a toggle and not two separate entry points.
      -->
      <div
        v-if="canToggleEditor"
        class="flex items-center gap-2"
        data-testid="workflows-editor-pane-toggle"
      >
        <button
          type="button"
          class="rounded-sm border px-3 py-1 font-ui text-xs"
          :class="
            editorMode === 'yaml-edit'
              ? 'border-accent bg-surface-2 text-accent'
              : 'border-border-muted bg-surface-1 text-ink-muted hover:bg-surface-2'
          "
          data-testid="workflows-editor-pane-yaml"
          @click="switchEditorPane('yaml')"
        >
          YAML
        </button>
        <button
          type="button"
          class="rounded-sm border px-3 py-1 font-ui text-xs"
          :class="
            editorMode === 'canvas-edit'
              ? 'border-accent bg-surface-2 text-accent'
              : 'border-border-muted bg-surface-1 text-ink-muted hover:bg-surface-2'
          "
          data-testid="workflows-editor-pane-canvas"
          @click="switchEditorPane('canvas')"
        >
          Canvas
        </button>
        <span class="font-ui text-[11px] text-ink-muted">
          Both panes read the workflow as last saved — switching discards
          unsaved edits in the pane you leave.
        </span>
      </div>

      <div
        v-if="editorMode === 'yaml-new' || editorMode === 'yaml-edit'"
        data-testid="workflows-editor-slot-yaml"
      >
        <WorkflowEditor
          :client="client"
          :initial-yaml="editorYaml"
          :title="editorTitle"
          @cancel="closeEditor"
          @saved="onEditorSaved"
          @ran="onEditorSaved($event.save)"
        />
      </div>

      <div
        v-else-if="editorMode === 'canvas-new' || editorMode === 'canvas-edit'"
        data-testid="workflows-editor-slot-canvas"
      >
        <h2 class="font-ui text-sm text-ink mb-2">{{ editorTitle }}</h2>
        <WorkflowGraphEditor
          :workflow="editorWorkflow"
          @cancel="closeEditor"
          @save="onCanvasSave"
        />
      </div>

      <div v-if="loading" class="font-ui text-sm text-ink-muted">
        Loading workflows…
      </div>

      <div
        v-else-if="loadError"
        class="rounded-sm border border-signal-danger bg-signal-danger-soft p-4 font-ui text-sm text-signal-danger"
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
                  class="text-signal-danger"
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

          <div class="flex items-center gap-2">
            <button
              type="button"
              class="rounded-sm border border-accent bg-accent px-4 py-1.5 font-ui text-sm text-bg hover:opacity-90 disabled:opacity-50"
              :disabled="running"
              data-testid="workflows-run-button"
              @click="runWorkflow"
            >
              {{ running ? 'Running…' : 'Run workflow' }}
            </button>
            <button
              type="button"
              class="rounded-sm border border-border-muted bg-surface-2 px-3 py-1.5 font-ui text-sm text-ink hover:bg-surface-1 disabled:opacity-50"
              data-testid="workflows-edit-button"
              @click="openEditExisting"
            >
              Edit
            </button>
            <button
              type="button"
              class="rounded-sm border border-border-muted bg-surface-2 px-3 py-1.5 font-ui text-sm text-ink hover:bg-surface-1 disabled:opacity-50"
              data-testid="workflows-edit-canvas-button"
              @click="openCanvasEditor"
            >
              Edit on canvas
            </button>
            <button
              type="button"
              class="rounded-sm border border-border-muted bg-surface-2 px-3 py-1.5 font-ui text-sm text-ink-muted hover:text-signal-danger hover:border-signal-danger disabled:opacity-50"
              :disabled="saving"
              data-testid="workflows-delete-button"
              @click="deleteSelected"
            >
              Delete
            </button>
            <!-- fleet-share-and-sync-01NDFSEX14 WP03 — Publish to team -->
            <button
              v-if="signedIn"
              type="button"
              class="rounded-sm border border-border-muted bg-surface-0 px-3 py-1.5 font-ui text-sm text-ink-muted hover:bg-surface-2"
              data-testid="workflows-publish-btn"
              @click="openPublishDialog"
            >
              Publish to team
            </button>
          </div>
          <!-- Publish dialog (WP03) -->
          <PublishDialog
            :open="publishDialogOpen"
            kind="workflow"
            :slug="selected?.id ?? ''"
            :payload-json="JSON.stringify(selected ?? {})"
            @close="publishDialogOpen = false"
            @published="onWorkflowPublished"
          />
          <div
            v-if="saveError"
            class="font-ui text-sm text-signal-danger"
            data-testid="workflows-save-error"
          >
            {{ saveError }}
          </div>

          <div v-if="runError" class="font-ui text-sm text-signal-danger" data-testid="workflows-run-error">
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
                  class="mt-1 font-ui text-xs text-signal-danger"
                >
                  {{ step.error }}
                </p>
              </li>
            </ol>
          </div>
        </section>
      </div>
      </template><!-- end Library tab -->
    </div>
  </div>
</template>
