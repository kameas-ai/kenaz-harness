<script setup lang="ts">
/**
 * WorkflowsSettingsPanel — Settings → Workflows tab.
 *
 * Mission workflow-extensions-01KW2D3Y, WP02.
 *
 * Lists all installed user workflows (builtin + user) with CRUD
 * affordances. Delegates to WorkflowGraphEditor for visual editing
 * and SimpleTemplateEditor for template-based creation.
 */
import { computed, ref, onMounted } from 'vue';
import WorkflowGraphEditor from '@/components/workflows/WorkflowGraphEditor.vue';
import SimpleTemplateEditor from '@/views/workflows/SimpleTemplateEditor.vue';
import {
  createWorkflowsClient,
  type WorkflowsClient,
  type WorkflowsSummary,
  type WorkflowsWorkflow,
  type WorkflowsSaveOutput,
  type WorkflowsScheduleEntry,
} from '@/lib/workflowsClient';

const props = defineProps<{
  /** Test seam: tests inject a fake client; production constructs one. */
  client?: WorkflowsClient;
}>();

const wfClient: WorkflowsClient = props.client ?? createWorkflowsClient();

// ── catalog state ─────────────────────────────────────────────────────

const catalog = ref<WorkflowsSummary[]>([]);
const loading = ref(false);
const loadError = ref<string | null>(null);

async function loadCatalog() {
  loading.value = true;
  loadError.value = null;
  try {
    catalog.value = await wfClient.list();
  } catch (err) {
    loadError.value = err instanceof Error ? err.message : String(err);
  } finally {
    loading.value = false;
  }
}

onMounted(loadCatalog);

// ── schedule state (per workflow) ──────────────────────────────────────
//
// Use the canonical WorkflowsScheduleEntry from workflowsClient so the
// timezone-optional contract matches the wire shape (the local copy
// previously declared timezone:string required, which tsc rejected as
// incompatible with the client's optional return type).

const schedules = ref<WorkflowsScheduleEntry[]>([]);

async function loadSchedules() {
  try {
    const list = await wfClient.scheduleList();
    schedules.value = list;
  } catch {
    schedules.value = [];
  }
}

onMounted(loadSchedules);

function scheduleFor(workflowId: string): WorkflowsScheduleEntry | undefined {
  return schedules.value.find((s) => s.workflowId === workflowId);
}

// ── editor mode ────────────────────────────────────────────────────────

type EditorMode = null | 'template' | 'graph-new' | 'graph-edit';
const editorMode = ref<EditorMode>(null);
const editingWorkflow = ref<WorkflowsWorkflow | null>(null);
const newMenuOpen = ref(false);

function openTemplateEditor() {
  editorMode.value = 'template';
  newMenuOpen.value = false;
  editingWorkflow.value = null;
}

function openBlankGraphEditor() {
  editorMode.value = 'graph-new';
  newMenuOpen.value = false;
  editingWorkflow.value = null;
}

async function openGraphEditorForExisting(id: string) {
  try {
    const w = await wfClient.get(id);
    editingWorkflow.value = w;
    editorMode.value = 'graph-edit';
  } catch (err) {
    loadError.value = err instanceof Error ? err.message : String(err);
  }
}

function closeEditor() {
  editorMode.value = null;
  editingWorkflow.value = null;
}

async function onGraphEditorSave(wf: WorkflowsWorkflow) {
  try {
    await wfClient.save({ workflow: wf });
    closeEditor();
    await loadCatalog();
  } catch (err) {
    loadError.value = err instanceof Error ? err.message : String(err);
  }
}

async function onTemplateSaved(_out: WorkflowsSaveOutput) {
  closeEditor();
  await loadCatalog();
}

// ── delete ─────────────────────────────────────────────────────────────

const pendingDeleteId = ref<string | null>(null);
const pendingDeleteHasSchedule = computed<boolean>(() => {
  if (!pendingDeleteId.value) return false;
  return !!scheduleFor(pendingDeleteId.value);
});

const deleteError = ref<string | null>(null);
const deleting = ref(false);

function confirmDelete(id: string) {
  pendingDeleteId.value = id;
  deleteError.value = null;
}

function cancelDelete() {
  pendingDeleteId.value = null;
}

async function executeDelete() {
  if (!pendingDeleteId.value) return;
  deleting.value = true;
  deleteError.value = null;
  try {
    await wfClient.remove(pendingDeleteId.value);
    pendingDeleteId.value = null;
    await loadCatalog();
    await loadSchedules();
  } catch (err) {
    deleteError.value = err instanceof Error ? err.message : String(err);
  } finally {
    deleting.value = false;
  }
}

// ── schedule modal ──────────────────────────────────────────────────────

const scheduleModalId = ref<string | null>(null);
const scheduleCron = ref('0 7 * * *');
const scheduleTimezone = ref('UTC');
const scheduleSaving = ref(false);
const scheduleError = ref<string | null>(null);

function openScheduleModal(id: string) {
  scheduleModalId.value = id;
  const existing = scheduleFor(id);
  scheduleCron.value = existing?.cron ?? '0 7 * * *';
  scheduleTimezone.value = existing?.timezone ?? 'UTC';
  scheduleError.value = null;
}

function closeScheduleModal() {
  scheduleModalId.value = null;
}

async function saveSchedule() {
  if (!scheduleModalId.value) return;
  scheduleSaving.value = true;
  scheduleError.value = null;
  try {
    await wfClient.scheduleSet({
      workflowId: scheduleModalId.value,
      cron: scheduleCron.value,
      timezone: scheduleTimezone.value,
    });
    closeScheduleModal();
    await loadSchedules();
  } catch (err) {
    scheduleError.value = err instanceof Error ? err.message : String(err);
  } finally {
    scheduleSaving.value = false;
  }
}

async function clearSchedule(id: string) {
  try {
    await wfClient.scheduleClear(id);
    await loadSchedules();
  } catch (err) {
    loadError.value = err instanceof Error ? err.message : String(err);
  }
}
</script>

<template>
  <div data-testid="workflows-settings-panel" class="px-6 py-4 space-y-4">
    <!-- Header -->
    <div class="flex items-center justify-between">
      <div>
        <h2 class="font-ui text-base text-ink">Workflows</h2>
        <p class="font-ui text-sm text-ink-muted">
          Manage your saved workflows. Create, edit, schedule, or delete.
        </p>
      </div>
      <div class="relative">
        <button
          type="button"
          class="rounded-sm border border-accent bg-accent px-3 py-1.5 font-ui text-sm text-bg hover:opacity-90"
          data-testid="wsp-new-button"
          @click="newMenuOpen = !newMenuOpen"
        >
          + New
        </button>
        <div
          v-if="newMenuOpen"
          class="absolute right-0 top-full mt-1 w-48 rounded-sm border border-border-muted bg-surface-1 shadow-lg z-10"
          data-testid="wsp-new-menu"
        >
          <button
            type="button"
            class="block w-full text-left px-3 py-2 font-ui text-sm text-ink hover:bg-surface-2"
            data-testid="wsp-new-template"
            @click="openTemplateEditor"
          >
            From template
          </button>
          <button
            type="button"
            class="block w-full text-left px-3 py-2 font-ui text-sm text-ink hover:bg-surface-2"
            data-testid="wsp-new-graph"
            @click="openBlankGraphEditor"
          >
            Visual editor
          </button>
        </div>
      </div>
    </div>

    <!-- Editor slots -->
    <div v-if="editorMode === 'template'" data-testid="wsp-editor-template">
      <SimpleTemplateEditor
        :client="wfClient"
        @cancel="closeEditor"
        @saved="onTemplateSaved"
      />
    </div>

    <div
      v-else-if="editorMode === 'graph-new' || editorMode === 'graph-edit'"
      data-testid="wsp-editor-graph"
    >
      <WorkflowGraphEditor
        :workflow="editingWorkflow"
        @save="onGraphEditorSave"
        @cancel="closeEditor"
      />
    </div>

    <!-- Load error -->
    <div
      v-if="loadError"
      class="rounded-sm border border-signal-danger bg-signal-danger-soft p-3 font-ui text-sm text-signal-danger"
      data-testid="wsp-load-error"
    >
      {{ loadError }}
    </div>

    <!-- Loading -->
    <div v-if="loading" class="font-ui text-sm text-ink-muted">
      Loading workflows…
    </div>

    <!-- Empty state -->
    <div
      v-else-if="!loading && catalog.length === 0 && editorMode === null"
      class="rounded-sm border border-border-muted bg-surface-1 p-6"
      data-testid="wsp-empty"
    >
      <p class="font-ui text-sm text-ink">No workflows installed yet.</p>
      <p class="font-ui text-sm text-ink-muted mt-1">
        Click "+ New" to create your first workflow.
      </p>
    </div>

    <!-- Workflow table -->
    <div
      v-else-if="!loading && catalog.length > 0 && editorMode === null"
      class="rounded-sm border border-border-muted overflow-hidden"
      data-testid="wsp-table"
    >
      <table class="w-full font-ui text-sm">
        <thead>
          <tr class="border-b border-border-muted bg-surface-1">
            <th class="text-left px-3 py-2 text-xs uppercase tracking-wide text-ink-muted">Name</th>
            <th class="text-left px-3 py-2 text-xs uppercase tracking-wide text-ink-muted">Steps</th>
            <th class="text-left px-3 py-2 text-xs uppercase tracking-wide text-ink-muted">Source</th>
            <th class="text-left px-3 py-2 text-xs uppercase tracking-wide text-ink-muted">Ver.</th>
            <th class="text-left px-3 py-2 text-xs uppercase tracking-wide text-ink-muted">Schedule</th>
            <th class="px-3 py-2 text-xs uppercase tracking-wide text-ink-muted text-right">Actions</th>
          </tr>
        </thead>
        <tbody>
          <tr
            v-for="wf in catalog"
            :key="wf.id"
            class="border-b border-border-muted last:border-0 hover:bg-surface-1"
            :data-testid="`wsp-row-${wf.id}`"
          >
            <td class="px-3 py-2 text-ink font-medium">{{ wf.name }}</td>
            <td class="px-3 py-2 text-ink-muted">{{ wf.stepCount }}</td>
            <td class="px-3 py-2 text-ink-muted">{{ wf.source }}</td>
            <td class="px-3 py-2 text-ink-muted font-mono text-xs">v{{ wf.version }}</td>
            <td class="px-3 py-2">
              <span
                v-if="scheduleFor(wf.id)"
                class="inline-flex items-center gap-1 rounded-full px-2 py-0.5 font-mono text-xs bg-signal-ok-soft text-signal-ok"
                :data-testid="`wsp-schedule-badge-${wf.id}`"
              >
                {{ scheduleFor(wf.id)!.cron }}
              </span>
              <span
                v-else
                class="font-ui text-xs text-ink-muted"
              >
                —
              </span>
            </td>
            <td class="px-3 py-2">
              <div class="flex items-center justify-end gap-1">
                <button
                  type="button"
                  class="rounded-sm px-2 py-1 font-ui text-xs text-ink-muted hover:text-ink hover:bg-surface-2"
                  :data-testid="`wsp-edit-${wf.id}`"
                  @click="openGraphEditorForExisting(wf.id)"
                >
                  Edit
                </button>
                <button
                  type="button"
                  class="rounded-sm px-2 py-1 font-ui text-xs text-ink-muted hover:text-ink hover:bg-surface-2"
                  :data-testid="`wsp-schedule-${wf.id}`"
                  @click="openScheduleModal(wf.id)"
                >
                  {{ scheduleFor(wf.id) ? 'Reschedule' : 'Schedule' }}
                </button>
                <button
                  v-if="scheduleFor(wf.id)"
                  type="button"
                  class="rounded-sm px-2 py-1 font-ui text-xs text-ink-muted hover:text-signal-danger hover:border-signal-danger"
                  :data-testid="`wsp-clear-schedule-${wf.id}`"
                  @click="clearSchedule(wf.id)"
                >
                  Unschedule
                </button>
                <button
                  v-if="wf.source === 'user'"
                  type="button"
                  class="rounded-sm px-2 py-1 font-ui text-xs text-ink-muted hover:text-signal-danger"
                  :data-testid="`wsp-delete-${wf.id}`"
                  @click="confirmDelete(wf.id)"
                >
                  Delete
                </button>
              </div>
            </td>
          </tr>
        </tbody>
      </table>
    </div>

    <!-- Delete confirmation dialog -->
    <dialog
      v-if="pendingDeleteId !== null"
      open
      class="fixed inset-0 z-50 m-auto w-full max-w-sm rounded-sm border border-border-muted bg-surface-1 p-5 shadow-xl"
      data-testid="wsp-delete-dialog"
    >
      <h3 class="font-ui text-base text-ink mb-2">Delete workflow?</h3>
      <p v-if="pendingDeleteHasSchedule" class="font-ui text-sm text-signal-warn mb-3">
        This workflow has an active cron schedule. Deleting it will also remove the schedule.
      </p>
      <p class="font-ui text-sm text-ink-muted mb-4">
        This action cannot be undone.
      </p>
      <div
        v-if="deleteError"
        class="mb-3 font-ui text-sm text-signal-danger"
        data-testid="wsp-delete-error"
      >
        {{ deleteError }}
      </div>
      <div class="flex gap-2 justify-end">
        <button
          type="button"
          class="rounded-sm border border-border-muted px-3 py-1.5 font-ui text-sm text-ink hover:bg-surface-2"
          data-testid="wsp-delete-cancel"
          @click="cancelDelete"
        >
          Cancel
        </button>
        <button
          type="button"
          class="rounded-sm bg-signal-danger px-3 py-1.5 font-ui text-sm text-white hover:brightness-90 disabled:opacity-50"
          :disabled="deleting"
          data-testid="wsp-delete-confirm"
          @click="executeDelete"
        >
          {{ deleting ? 'Deleting…' : 'Delete' }}
        </button>
      </div>
    </dialog>

    <!-- Schedule modal -->
    <dialog
      v-if="scheduleModalId !== null"
      open
      class="fixed inset-0 z-50 m-auto w-full max-w-sm rounded-sm border border-border-muted bg-surface-1 p-5 shadow-xl"
      data-testid="wsp-schedule-modal"
    >
      <h3 class="font-ui text-base text-ink mb-3">Set schedule</h3>
      <div class="space-y-3 mb-4">
        <div>
          <label class="font-ui text-xs text-ink-muted block mb-1" for="wsp-cron">
            Cron expression (5-field)
          </label>
          <input
            id="wsp-cron"
            v-model="scheduleCron"
            type="text"
            class="w-full rounded-sm border border-border-muted bg-surface-2 px-2 py-1 font-mono text-sm text-ink"
            data-testid="wsp-cron-input"
            placeholder="0 7 * * *"
          />
        </div>
        <div>
          <label class="font-ui text-xs text-ink-muted block mb-1" for="wsp-tz">
            Timezone (IANA)
          </label>
          <input
            id="wsp-tz"
            v-model="scheduleTimezone"
            type="text"
            class="w-full rounded-sm border border-border-muted bg-surface-2 px-2 py-1 font-mono text-sm text-ink"
            data-testid="wsp-tz-input"
            placeholder="UTC"
          />
        </div>
      </div>
      <div
        v-if="scheduleError"
        class="mb-3 font-ui text-sm text-signal-danger"
        data-testid="wsp-schedule-error"
      >
        {{ scheduleError }}
      </div>
      <div class="flex gap-2 justify-end">
        <button
          type="button"
          class="rounded-sm border border-border-muted px-3 py-1.5 font-ui text-sm text-ink hover:bg-surface-2"
          data-testid="wsp-schedule-cancel"
          @click="closeScheduleModal"
        >
          Cancel
        </button>
        <button
          type="button"
          class="rounded-sm border border-accent bg-accent px-3 py-1.5 font-ui text-sm text-bg hover:opacity-90 disabled:opacity-50"
          :disabled="scheduleSaving"
          data-testid="wsp-schedule-save"
          @click="saveSchedule"
        >
          {{ scheduleSaving ? 'Saving…' : 'Save schedule' }}
        </button>
      </div>
    </dialog>
  </div>
</template>
