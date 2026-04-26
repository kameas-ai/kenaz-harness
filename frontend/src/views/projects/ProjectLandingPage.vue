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
import { MessageSquare, Plus } from '@/shell/icons';
import { useHarnessClient } from '@/lib/useHarnessAPI';
import AttachmentRow from '@/components/contexts/AttachmentRow.vue';
import AttachmentTreePicker from '@/components/contexts/AttachmentTreePicker.vue';
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

const projectId = computed(() => {
  const v = route.params.id;
  return typeof v === 'string' ? v : '';
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
      <section>
        <h2
          class="font-ui text-[11px] font-medium uppercase tracking-[0.18em] text-ink-subtle"
        >
          Sessions
          <span class="ml-1 text-ink-dim">({{ sessions.length }})</span>
        </h2>
        <div
          v-if="loading"
          class="mt-2 font-ui text-xs text-ink-muted"
          data-testid="project-loading"
        >
          Loading…
        </div>
        <ul
          v-else-if="sessions.length > 0"
          class="mt-2 space-y-1"
          data-testid="project-sessions-list"
        >
          <li v-for="s in sessions" :key="s.id">
            <button
              type="button"
              class="flex w-full items-center gap-2 rounded-sm px-3 py-2 text-left font-ui text-sm text-ink-muted hover:bg-surface-2 hover:text-ink"
              :data-testid="`project-session-${s.id}`"
              @click="openSession(s.id)"
            >
              <MessageSquare :size="14" />
              <span class="truncate">{{ s.name }}</span>
            </button>
          </li>
        </ul>
        <p
          v-else
          class="mt-2 font-ui text-xs text-ink-muted"
          data-testid="project-sessions-empty"
        >
          No sessions yet. Create one from the rail and assign it to this
          project.
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
  </div>
</template>
