<script setup lang="ts">
/**
 * ProjectLandingPage — landing page for a single project (/projects/:id).
 *
 * WP02 ships the placeholder shell: project metadata header + sessions
 * list. WP04 adds the "Project context" section that lists project-
 * scoped attachments and lets the user add / remove / reorder /
 * refresh entries pulled from the Context Library.
 */
import { computed, onMounted, ref, watch } from 'vue';
import { useRoute, useRouter } from 'vue-router';
import CanvasHead from '@/shell/CanvasHead.vue';
import { MessageSquare, Plus, Pencil, Brain } from '@/shell/icons';
import { useHarnessClient } from '@/lib/useHarnessAPI';
import AttachmentRow from '@/components/contexts/AttachmentRow.vue';
import AttachmentTreePicker from '@/components/contexts/AttachmentTreePicker.vue';
import NewSessionDialog from '@/shell/NewSessionDialog.vue';
import type { Attachment, Project, Session } from '@/lib/types';

const client = useHarnessClient();
const route = useRoute();
const router = useRouter();

const project = ref<Project | null>(null);
const sessions = ref<readonly Session[]>([]);
const loading = ref(false);
const errorMsg = ref<string | null>(null);

const attachments = ref<Attachment[]>([]);
const attachmentsError = ref<string | null>(null);
const attachmentsLoading = ref(false);
const pickerOpen = ref(false);

// Editable description state (WP07 T002).
const descEditing = ref(false);
const descDraft = ref('');
const descSaving = ref(false);
const descError = ref<string | null>(null);

// Project-scope memory count (WP07 T002).
const memoryCount = ref(0);
const memoryError = ref<string | null>(null);

// New-session affordance (WP07 T002).
const newSessionDialogOpen = ref(false);

const projectId = computed(() => {
  const v = route.params.id;
  return typeof v === 'string' ? v : '';
});

const sortedSessions = computed<readonly Session[]>(() => {
  // Sort by lastActiveAt DESC (most recent first); fall back to
  // createdAt for rows missing lastActiveAt. Mutating a copy keeps
  // the props payload stable.
  return [...sessions.value].sort((a, b) => {
    const av = a.lastActiveAt ?? a.createdAt ?? '';
    const bv = b.lastActiveAt ?? b.createdAt ?? '';
    return bv.localeCompare(av);
  });
});

async function load(id: string) {
  if (!id) return;
  loading.value = true;
  errorMsg.value = null;
  try {
    project.value = await client.projects.get(id);
    sessions.value = await client.projects.listSessions(id);
  } catch (err) {
    errorMsg.value = err instanceof Error ? err.message : String(err);
    project.value = null;
    sessions.value = [];
  } finally {
    loading.value = false;
  }
  await loadAttachments(id);
  await loadMemoryCount(id);
}

async function loadMemoryCount(id: string) {
  if (!id) {
    memoryCount.value = 0;
    return;
  }
  memoryError.value = null;
  try {
    const chunks = await client.memory.listChunks({
      scopeKind: 'project',
      scopeId: id,
    });
    memoryCount.value = chunks.length;
  } catch (err) {
    memoryCount.value = 0;
    memoryError.value = err instanceof Error ? err.message : String(err);
  }
}

function startEditDescription() {
  if (!project.value) return;
  descDraft.value = project.value.description ?? '';
  descError.value = null;
  descEditing.value = true;
}

function cancelEditDescription() {
  descEditing.value = false;
  descDraft.value = '';
  descError.value = null;
}

async function saveDescription() {
  if (!project.value || descSaving.value) return;
  descSaving.value = true;
  descError.value = null;
  const next = descDraft.value;
  try {
    await client.projects.updateDescription(project.value.id, next);
    project.value = { ...project.value, description: next };
    descEditing.value = false;
  } catch (err) {
    descError.value = err instanceof Error ? err.message : String(err);
  } finally {
    descSaving.value = false;
  }
}

function openMemoryView() {
  void router.push({
    path: '/memory',
    query: { scopeKind: 'project', scopeId: projectId.value },
  });
}

function openNewSessionDialog() {
  newSessionDialogOpen.value = true;
}

async function onNewSessionDialogClose() {
  newSessionDialogOpen.value = false;
  // Refresh in case the dialog created a session in this project.
  await load(projectId.value);
}

function formatTimestamp(iso: string | undefined): string {
  if (!iso) return '—';
  const d = new Date(iso);
  if (Number.isNaN(d.getTime())) return iso;
  return d.toLocaleString();
}

async function loadAttachments(id: string) {
  if (!id) {
    attachments.value = [];
    return;
  }
  attachmentsLoading.value = true;
  attachmentsError.value = null;
  try {
    const rows = await client.attachments.list({
      scopeKind: 'project',
      scopeId: id,
    });
    attachments.value = [...rows];
  } catch (err) {
    attachments.value = [];
    attachmentsError.value =
      err instanceof Error ? err.message : String(err);
  } finally {
    attachmentsLoading.value = false;
  }
}

onMounted(() => {
  void load(projectId.value);
});

watch(projectId, (next) => {
  void load(next);
});

function openSession(id: string) {
  void router.push(`/sessions/${id}`);
}

function openPicker() {
  pickerOpen.value = true;
}

function onAdded(att: Attachment) {
  attachments.value = [...attachments.value, att];
}

function onRefreshed(updated: Attachment) {
  attachments.value = attachments.value.map((a) =>
    a.id === updated.id ? updated : a,
  );
}

function onRemoved(id: string) {
  attachments.value = attachments.value.filter((a) => a.id !== id);
}

// HTML5 drag-and-drop reorder. We track the dragged id locally and on
// drop reorder the list, then call Attachments_Reorder with the new
// id sequence. Prefer HTML5 over a custom pointer pipeline because
// (a) lib/types.ts ships no shared DnD primitive, and (b) the row
// count is small (<50 typical).
const draggedId = ref<string | null>(null);

function onDragStart(_evt: DragEvent, id: string) {
  draggedId.value = id;
}

function onDragOver(evt: DragEvent, _overId: string) {
  // dragover.prevent is the browser-mandated way to allow drop.
  evt.preventDefault();
  if (evt.dataTransfer) evt.dataTransfer.dropEffect = 'move';
}

async function onDrop(_evt: DragEvent, overId: string) {
  const dragged = draggedId.value;
  draggedId.value = null;
  if (!dragged || dragged === overId) return;
  const list = [...attachments.value];
  const fromIdx = list.findIndex((a) => a.id === dragged);
  const toIdx = list.findIndex((a) => a.id === overId);
  if (fromIdx < 0 || toIdx < 0) return;
  const [moved] = list.splice(fromIdx, 1);
  list.splice(toIdx, 0, moved);
  attachments.value = list;
  try {
    await client.attachments.reorder(
      'project',
      projectId.value,
      list.map((a) => a.id),
    );
  } catch (err) {
    attachmentsError.value =
      err instanceof Error ? err.message : String(err);
    // Reload to recover the canonical order on failure.
    await loadAttachments(projectId.value);
  }
}

function onDragEnd() {
  draggedId.value = null;
}
</script>

<template>
  <div class="h-full flex flex-col" data-testid="project-landing">
    <CanvasHead
      number="08"
      section="PROJECT"
      :title="project?.name ?? 'Project'"
      :subtitle="
        project?.description
          ? project.description
          : 'Group related sessions; attach context at project scope.'
      "
    />

    <div
      v-if="errorMsg"
      class="mx-6 mt-4 rounded-sm border border-signal-danger bg-surface-1 px-3 py-2 font-ui text-xs text-signal-danger"
      role="alert"
      data-testid="project-error"
    >
      {{ errorMsg }}
    </div>

    <div class="flex-1 overflow-y-auto px-6 py-4 space-y-6">
      <!-- editable description (WP07 T002) -->
      <section data-testid="project-description-section">
        <div class="flex items-center justify-between">
          <h2
            class="font-ui text-[11px] font-medium uppercase tracking-[0.18em] text-ink-subtle"
          >
            Description
          </h2>
          <button
            v-if="!descEditing && project"
            type="button"
            class="flex items-center gap-1 rounded-sm border border-border-muted bg-surface-1 px-2 py-1 font-ui text-[11px] text-ink-muted hover:bg-surface-2 hover:text-ink"
            data-testid="project-edit-description"
            @click="startEditDescription"
          >
            <Pencil :size="12" />
            Edit
          </button>
        </div>
        <div v-if="!descEditing">
          <p
            v-if="project?.description"
            class="mt-2 font-ui text-sm text-ink whitespace-pre-wrap cursor-pointer"
            data-testid="project-description-text"
            @click="startEditDescription"
          >
            {{ project.description }}
          </p>
          <p
            v-else
            class="mt-2 font-ui text-xs italic text-ink-dim cursor-pointer"
            data-testid="project-description-empty"
            @click="startEditDescription"
          >
            No description. Click to add one.
          </p>
        </div>
        <div v-else class="mt-2 space-y-2" data-testid="project-description-editor">
          <textarea
            v-model="descDraft"
            class="w-full min-h-[88px] rounded-sm border border-accent bg-surface-2 px-3 py-2 font-ui text-sm text-ink focus:outline-none"
            :data-testid="'project-description-input'"
            @keydown.escape="cancelEditDescription"
          />
          <div
            v-if="descError"
            class="rounded-sm border border-signal-danger bg-surface-1 px-3 py-2 font-ui text-[11px] text-signal-danger"
            role="alert"
            data-testid="project-description-error"
          >
            {{ descError }}
          </div>
          <div class="flex items-center justify-end gap-2">
            <button
              type="button"
              class="rounded-sm px-2 py-1 font-ui text-[11px] text-ink-dim hover:text-ink"
              :disabled="descSaving"
              data-testid="project-description-cancel"
              @click="cancelEditDescription"
            >
              Cancel
            </button>
            <button
              type="button"
              class="rounded-sm border border-accent bg-surface-1 px-2 py-1 font-ui text-[11px] text-accent hover:bg-accent-glow disabled:opacity-50"
              :disabled="descSaving"
              data-testid="project-description-save"
              @click="saveDescription"
            >
              {{ descSaving ? 'Saving…' : 'Save' }}
            </button>
          </div>
        </div>
      </section>

      <!-- project-scope memory link (WP07 T002) -->
      <section data-testid="project-memory-section">
        <h2
          class="font-ui text-[11px] font-medium uppercase tracking-[0.18em] text-ink-subtle"
        >
          Project memory
          <span class="ml-1 text-ink-dim">({{ memoryCount }})</span>
        </h2>
        <p class="mt-1 font-ui text-[11px] text-ink-dim">
          Pinned snippets scoped to this project; pulled into every session
          here at retrieval time.
        </p>
        <div
          v-if="memoryError"
          class="mt-2 rounded-sm border border-signal-danger bg-surface-1 px-3 py-2 font-ui text-[11px] text-signal-danger"
          role="alert"
          data-testid="project-memory-error"
        >
          {{ memoryError }}
        </div>
        <button
          type="button"
          class="mt-2 flex items-center gap-1 rounded-sm border border-border-muted bg-surface-1 px-2 py-1 font-ui text-[11px] text-ink-muted hover:bg-surface-2 hover:text-accent"
          data-testid="project-memory-link"
          @click="openMemoryView"
        >
          <Brain :size="12" />
          Open memory at project scope
        </button>
      </section>

      <section>
        <div class="flex items-center justify-between">
          <h2
            class="font-ui text-[11px] font-medium uppercase tracking-[0.18em] text-ink-subtle"
          >
            Sessions
            <span class="ml-1 text-ink-dim">({{ sessions.length }})</span>
          </h2>
          <button
            type="button"
            class="flex items-center gap-1 rounded-sm border border-accent-hairline bg-surface-1 px-2 py-1 font-ui text-[11px] text-accent hover:bg-accent-glow"
            data-testid="project-new-session"
            @click="openNewSessionDialog"
          >
            <Plus :size="12" />
            <span>Start session in this project</span>
          </button>
        </div>
        <div
          v-if="loading"
          class="mt-2 font-ui text-xs text-ink-muted"
          data-testid="project-loading"
        >
          Loading…
        </div>
        <table
          v-else-if="sortedSessions.length > 0"
          class="mt-2 w-full text-left font-ui text-xs"
          data-testid="project-sessions-list"
        >
          <thead>
            <tr class="text-[10px] uppercase tracking-[0.18em] text-ink-subtle">
              <th class="px-3 py-1.5 font-medium">Name</th>
              <th class="px-3 py-1.5 font-medium">Last active</th>
            </tr>
          </thead>
          <tbody>
            <tr
              v-for="s in sortedSessions"
              :key="s.id"
              class="cursor-pointer hover:bg-surface-2"
              :data-testid="`project-session-row-${s.id}`"
              @click="openSession(s.id)"
            >
              <td class="px-3 py-2 text-ink">
                <button
                  type="button"
                  class="flex w-full items-center gap-2 text-left text-ink-muted hover:text-ink"
                  :data-testid="`project-session-${s.id}`"
                >
                  <MessageSquare :size="13" />
                  <span class="truncate">{{ s.name }}</span>
                </button>
              </td>
              <td
                class="px-3 py-2 text-ink-dim font-mono text-[11px]"
                :data-testid="`project-session-last-active-${s.id}`"
              >
                {{ formatTimestamp(s.lastActiveAt) }}
              </td>
            </tr>
          </tbody>
        </table>
        <p
          v-else
          class="mt-2 font-ui text-xs text-ink-muted"
          data-testid="project-sessions-empty"
        >
          No sessions yet. Use the “Start session in this project” button to
          create one.
        </p>
      </section>

      <section data-testid="project-context-section">
        <div class="flex items-center justify-between">
          <h2
            class="font-ui text-[11px] font-medium uppercase tracking-[0.18em] text-ink-subtle"
          >
            Project context
            <span class="ml-1 text-ink-dim">({{ attachments.length }})</span>
          </h2>
          <button
            type="button"
            class="flex items-center gap-1 rounded-sm border border-accent-hairline bg-surface-1 px-2 py-1 font-ui text-[11px] text-accent hover:bg-accent-glow"
            data-testid="project-add-context"
            @click="openPicker"
          >
            <Plus :size="12" />
            <span>Add context</span>
          </button>
        </div>
        <p class="mt-1 font-ui text-[11px] text-ink-dim">
          Attached files apply to every session in this project, including
          sessions created later. Reorder by drag — order matters because
          the resolved stream concatenates global → project → session.
        </p>
        <div
          v-if="attachmentsError"
          class="mt-2 rounded-sm border border-signal-danger bg-surface-1 px-3 py-2 font-ui text-[11px] text-signal-danger"
          role="alert"
          data-testid="project-attachments-error"
        >
          {{ attachmentsError }}
        </div>
        <div
          v-if="attachmentsLoading"
          class="mt-2 font-ui text-xs text-ink-muted"
          data-testid="project-attachments-loading"
        >
          Loading…
        </div>
        <ul
          v-else-if="attachments.length > 0"
          class="mt-2 space-y-2"
          data-testid="project-attachments-list"
        >
          <li v-for="a in attachments" :key="a.id">
            <AttachmentRow
              :attachment="a"
              :draggable="true"
              @refreshed="onRefreshed"
              @removed="onRemoved"
              @drag-start="onDragStart"
              @drag-over="onDragOver"
              @drop="onDrop"
              @drag-end="onDragEnd"
            />
          </li>
        </ul>
        <p
          v-else
          class="mt-2 font-ui text-xs text-ink-muted"
          data-testid="project-attachments-empty"
        >
          No project context yet. Pick a file from the library to attach.
        </p>
      </section>
    </div>

    <AttachmentTreePicker
      :open="pickerOpen"
      scope-kind="project"
      :scope-id="projectId"
      @added="onAdded"
      @close="pickerOpen = false"
    />

    <NewSessionDialog
      :open="newSessionDialogOpen"
      :initial-project-id="projectId"
      @close="onNewSessionDialogClose"
    />
  </div>
</template>
