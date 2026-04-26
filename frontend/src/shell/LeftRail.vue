<script setup lang="ts">
import { computed, ref, onMounted } from 'vue';
import { useRoute, useRouter } from 'vue-router';
import RailEntry from './RailEntry.vue';
import {
  Plus,
  MessageSquare,
  Wrench,
  FileText,
  Settings,
  Brain,
  GitBranch,
  Pencil,
  Trash2,
  X,
  ChevronDown,
  ChevronRight,
  Folder,
} from './icons';
import { useSessions, useProjects } from '@/lib/useHarnessAPI';
import NewSessionDialog from './NewSessionDialog.vue';
import type { Project, Session } from '@/lib/types';

/**
 * LeftRail — three vertical regions (plan §2.1):
 *   1. New-session affordance (top)
 *   2. Sessions list grouped by project (middle, scrollable)
 *      - Each project header is collapsible.
 *      - Right-click project header → Rename / Delete.
 *      - "Loose" group at the bottom for sessions outside any project.
 *   3. Primary-surfaces nav (bottom)
 */
const {
  list: sessionList,
  refresh: refreshSessions,
  rename: renameSession,
  remove: removeSession,
  moveToProject: moveSessionToProject,
} = useSessions();
const {
  list: projectList,
  refresh: refreshProjects,
  create: createProject,
  rename: renameProject,
  remove: removeProject,
} = useProjects();

const newSessionDialogOpen = ref(false);
const deletingId = ref<string | null>(null);
const renamingId = ref<string | null>(null);
const renameDraft = ref('');

// WP07 — drag-and-drop session-to-project membership. The dragged
// session id is captured at dragstart; project headers + the Loose
// header act as drop targets. drag-over targets are highlighted via
// `dropTargetId` so the user gets a visual hint during the drag.
const draggedSessionId = ref<string | null>(null);
const dropTargetId = ref<string | null>(null); // project id or '__loose__'
let focusedRenameId: string | null = null;
function setRenameInputRef(el: Element | null) {
  if (!(el instanceof HTMLInputElement)) {
    focusedRenameId = null;
    return;
  }
  if (focusedRenameId === renamingId.value) return;
  focusedRenameId = renamingId.value;
  el.focus();
  el.select();
}
const lastError = ref<string | null>(null);
const route = useRoute();
const router = useRouter();

// Project rename / delete state.
const newProjectPromptOpen = ref(false);
const newProjectDraft = ref('');
const renamingProjectId = ref<string | null>(null);
const renameProjectDraft = ref('');
const projectMenu = ref<{ id: string; x: number; y: number } | null>(null);
const deleteModal = ref<{ project: Project; cascade: boolean } | null>(null);
const collapsed = ref<Set<string>>(new Set());
let focusedProjectRenameId: string | null = null;
function setProjectRenameRef(el: Element | null) {
  if (!(el instanceof HTMLInputElement)) {
    focusedProjectRenameId = null;
    return;
  }
  if (focusedProjectRenameId === renamingProjectId.value) return;
  focusedProjectRenameId = renamingProjectId.value;
  el.focus();
  el.select();
}

const activeSessionId = computed(() => {
  const p = route.params.id;
  return typeof p === 'string' && route.name === 'sessions' ? p : '';
});
const activeProjectId = computed(() => {
  const p = route.params.id;
  return typeof p === 'string' && route.name === 'project' ? p : '';
});

const sessionsByProject = computed(() => {
  const map = new Map<string, Session[]>();
  for (const s of sessionList.value) {
    const key = s.projectId && s.projectId !== '' ? s.projectId : '__loose__';
    const list = map.get(key) ?? [];
    list.push(s);
    map.set(key, list);
  }
  return map;
});

const looseSessions = computed<Session[]>(
  () => sessionsByProject.value.get('__loose__') ?? [],
);

function sessionsFor(projectId: string): Session[] {
  return sessionsByProject.value.get(projectId) ?? [];
}

function isCollapsed(projectId: string): boolean {
  return collapsed.value.has(projectId);
}

function toggleCollapsed(projectId: string) {
  const next = new Set(collapsed.value);
  if (next.has(projectId)) {
    next.delete(projectId);
  } else {
    next.add(projectId);
  }
  collapsed.value = next;
}

onMounted(() => {
  refreshSessions();
  refreshProjects();
});

function newSession() {
  newSessionDialogOpen.value = true;
}

async function onNewSessionDialogClose() {
  newSessionDialogOpen.value = false;
  await refreshSessions();
  await refreshProjects();
}

function openSession(id: string) {
  if (!id || deletingId.value === id || renamingId.value === id) return;
  void router.push(`/sessions/${id}`);
}

function openProjectPage(id: string) {
  if (!id) return;
  void router.push(`/projects/${id}`);
}

function startRename(id: string, currentName: string, event: Event) {
  event.preventDefault();
  event.stopPropagation();
  if (renamingId.value || deletingId.value) return;
  renamingId.value = id;
  renameDraft.value = currentName;
}

function cancelRename() {
  renamingId.value = null;
  renameDraft.value = '';
  focusedRenameId = null;
}

async function commitRename(id: string) {
  const next = renameDraft.value.trim();
  if (!next) {
    cancelRename();
    return;
  }
  const current = sessionList.value.find((s) => s.id === id);
  if (current && current.name === next) {
    cancelRename();
    return;
  }
  try {
    await renameSession(id, next);
  } catch (err) {
    const msg = err instanceof Error ? err.message : String(err);
    lastError.value = `Rename failed: ${msg}`;
  } finally {
    cancelRename();
  }
}

async function deleteSession(id: string, event: Event) {
  event.preventDefault();
  event.stopPropagation();
  if (!id || deletingId.value) return;
  deletingId.value = id;
  lastError.value = null;
  try {
    if (activeSessionId.value === id) {
      await router.push('/sessions');
    }
    await removeSession(id);
  } catch (err) {
    const msg = err instanceof Error ? err.message : String(err);
    console.error('Delete session failed:', err);
    lastError.value = `Delete failed: ${msg}`;
    await refreshSessions();
  } finally {
    deletingId.value = null;
  }
}

async function clearAll() {
  if (sessionList.value.length === 0) return;
  if (
    !window.confirm(
      `Delete all ${sessionList.value.length} sessions? This cannot be undone.`,
    )
  ) {
    return;
  }
  lastError.value = null;
  if (activeSessionId.value) {
    await router.push('/sessions');
  }
  const failures: string[] = [];
  for (const s of [...sessionList.value]) {
    try {
      await removeSession(s.id);
    } catch (err) {
      const msg = err instanceof Error ? err.message : String(err);
      failures.push(`${s.id}: ${msg}`);
    }
  }
  if (failures.length > 0) {
    lastError.value = `Some deletes failed:\n${failures.join('\n')}`;
  }
  await refreshSessions();
}

function dismissError() {
  lastError.value = null;
}

// ── projects affordances ──────────────────────────────────────────────

function openNewProjectPrompt() {
  newProjectPromptOpen.value = true;
  newProjectDraft.value = '';
}

async function commitNewProject() {
  const name = newProjectDraft.value.trim();
  newProjectPromptOpen.value = false;
  newProjectDraft.value = '';
  if (!name) return;
  try {
    await createProject(name);
  } catch (err) {
    const msg = err instanceof Error ? err.message : String(err);
    lastError.value = `Create project failed: ${msg}`;
  }
}

function cancelNewProject() {
  newProjectPromptOpen.value = false;
  newProjectDraft.value = '';
}

function openProjectMenu(p: Project, event: MouseEvent) {
  event.preventDefault();
  projectMenu.value = { id: p.id, x: event.clientX, y: event.clientY };
}

function closeProjectMenu() {
  projectMenu.value = null;
}

function startProjectRename(p: Project) {
  closeProjectMenu();
  renamingProjectId.value = p.id;
  renameProjectDraft.value = p.name;
}

function cancelProjectRename() {
  renamingProjectId.value = null;
  renameProjectDraft.value = '';
  focusedProjectRenameId = null;
}

async function commitProjectRename(id: string) {
  const name = renameProjectDraft.value.trim();
  if (!name) {
    cancelProjectRename();
    return;
  }
  const current = projectList.value.find((p) => p.id === id);
  if (current && current.name === name) {
    cancelProjectRename();
    return;
  }
  try {
    await renameProject(id, name);
  } catch (err) {
    const msg = err instanceof Error ? err.message : String(err);
    lastError.value = `Rename project failed: ${msg}`;
  } finally {
    cancelProjectRename();
  }
}

function startProjectDelete(p: Project) {
  closeProjectMenu();
  deleteModal.value = { project: p, cascade: false };
}

function cancelProjectDelete() {
  deleteModal.value = null;
}

async function commitProjectDelete() {
  const ctx = deleteModal.value;
  if (!ctx) return;
  deleteModal.value = null;
  try {
    await removeProject(ctx.project.id, ctx.cascade);
    if (activeProjectId.value === ctx.project.id) {
      await router.push('/sessions');
    }
    await refreshSessions();
  } catch (err) {
    const msg = err instanceof Error ? err.message : String(err);
    lastError.value = `Delete project failed: ${msg}`;
  }
}

// ── drag-and-drop session-to-project (WP07 T001) ─────────────────────
//
// HTML5 native DnD, mirroring the AttachmentRow approach in
// ProjectLandingPage.vue. The session row is draggable; project
// headers and the Loose group header are drop targets. Drop calls
// moveToProject, then refreshSessions through the composable.
//
// A keyboard-accessible alternative remains available through the
// per-session menu (rename / delete buttons + future "Move to
// project" entry — wired separately).

function onSessionDragStart(evt: DragEvent, sessionId: string) {
  draggedSessionId.value = sessionId;
  if (evt.dataTransfer) {
    evt.dataTransfer.effectAllowed = 'move';
    // Setting data is required on Firefox for the drag to commit.
    try {
      evt.dataTransfer.setData('text/x-kenaz-session-id', sessionId);
    } catch {
      /* harmless — Firefox 100+ tolerates omission */
    }
  }
}

function onSessionDragEnd() {
  draggedSessionId.value = null;
  dropTargetId.value = null;
}

function onProjectDragOver(evt: DragEvent, projectId: string) {
  if (!draggedSessionId.value) return;
  evt.preventDefault();
  if (evt.dataTransfer) evt.dataTransfer.dropEffect = 'move';
  dropTargetId.value = projectId;
}

function onProjectDragLeave(_evt: DragEvent, projectId: string) {
  if (dropTargetId.value === projectId) dropTargetId.value = null;
}

async function onProjectDrop(evt: DragEvent, projectId: string) {
  evt.preventDefault();
  const sid = draggedSessionId.value;
  draggedSessionId.value = null;
  dropTargetId.value = null;
  if (!sid) return;
  // Skip the move when the session is already inside this project.
  const current = sessionList.value.find((s) => s.id === sid);
  const currentPid = current?.projectId ?? '';
  const targetPid = projectId === '__loose__' ? '' : projectId;
  if (currentPid === targetPid) return;
  try {
    await moveSessionToProject(sid, targetPid);
  } catch (err) {
    const msg = err instanceof Error ? err.message : String(err);
    lastError.value = `Move session failed: ${msg}`;
  }
}
</script>

<template>
  <div class="flex flex-col h-full" @click="closeProjectMenu">
    <!-- new-session affordance -->
    <div class="px-2 pt-3 pb-2">
      <button
        type="button"
        class="flex items-center gap-2 px-3 py-2 rounded-sm w-full text-left text-sm font-ui text-accent border border-accent-hairline hover:bg-accent-glow transition-fast ease-kenaz disabled:opacity-50"
        aria-label="New session"
        @click="newSession"
      >
        <Plus :size="14" />
        <span class="hidden two-col:inline">New session</span>
      </button>
    </div>
    <NewSessionDialog
      :open="newSessionDialogOpen"
      @close="onNewSessionDialogClose"
    />

    <!-- error banner -->
    <div
      v-if="lastError"
      class="mx-2 mb-1 rounded-sm border border-signal-danger bg-surface-1 px-2 py-1.5 font-ui text-[11px] text-signal-danger"
      role="alert"
    >
      <pre class="whitespace-pre-wrap break-words">{{ lastError }}</pre>
      <button
        type="button"
        class="mt-1 text-[10px] uppercase tracking-[0.18em] text-ink-dim hover:text-ink"
        @click="dismissError"
      >
        Dismiss
      </button>
    </div>

    <!-- sessions list (grouped by project) -->
    <nav
      class="flex-1 overflow-y-auto px-2 py-1"
      aria-label="Sessions"
    >
      <!-- Projects header + add affordance -->
      <div class="flex items-center gap-1 px-2 pt-1 pb-1">
        <span
          class="flex-1 font-ui text-[10px] uppercase tracking-[0.18em] text-ink-subtle"
        >
          Projects
        </span>
        <button
          type="button"
          class="text-[11px] text-ink-dim hover:text-accent flex items-center gap-1"
          aria-label="New project"
          data-testid="new-project"
          @click.stop="openNewProjectPrompt"
        >
          <Plus :size="12" />
          <span class="hidden two-col:inline">project</span>
        </button>
      </div>

      <!-- inline new-project prompt -->
      <div v-if="newProjectPromptOpen" class="px-2 pb-2">
        <input
          v-model="newProjectDraft"
          class="w-full rounded-sm border border-accent bg-surface-2 px-2 py-1 text-sm font-ui text-ink focus:outline-none"
          placeholder="Project name…"
          autofocus
          data-testid="new-project-input"
          @keydown.enter="commitNewProject"
          @keydown.escape="cancelNewProject"
          @blur="commitNewProject"
        />
      </div>

      <!-- per-project groups -->
      <ul class="space-y-1">
        <li
          v-for="project in projectList"
          :key="project.id"
          :data-testid="`project-group-${project.id}`"
        >
          <div
            class="flex items-stretch gap-1 rounded-sm"
            :class="dropTargetId === project.id ? 'bg-accent-glow ring-1 ring-accent' : ''"
            @dragover="onProjectDragOver($event, project.id)"
            @dragleave="onProjectDragLeave($event, project.id)"
            @drop="onProjectDrop($event, project.id)"
          >
            <template v-if="renamingProjectId === project.id">
              <input
                :ref="setProjectRenameRef"
                v-model="renameProjectDraft"
                class="flex-1 min-w-0 px-3 py-1.5 rounded-sm border border-accent bg-surface-2 text-sm font-ui text-ink focus:outline-none"
                :data-testid="`rename-project-input-${project.id}`"
                @keydown.enter="commitProjectRename(project.id)"
                @keydown.escape="cancelProjectRename"
                @blur="commitProjectRename(project.id)"
              />
            </template>
            <template v-else>
              <button
                type="button"
                class="flex-1 min-w-0 flex items-center gap-1 px-2 py-1.5 rounded-sm text-left text-sm font-ui text-ink-muted hover:text-ink hover:bg-surface-2"
                :data-testid="`project-header-${project.id}`"
                :aria-expanded="!isCollapsed(project.id)"
                @click="toggleCollapsed(project.id)"
                @contextmenu="openProjectMenu(project, $event)"
              >
                <component
                  :is="isCollapsed(project.id) ? ChevronRight : ChevronDown"
                  :size="12"
                  class="text-ink-dim"
                />
                <Folder :size="13" />
                <span class="truncate flex-1 hidden two-col:inline">{{ project.name }}</span>
                <span class="text-[10px] text-ink-dim">{{ sessionsFor(project.id).length }}</span>
              </button>
              <button
                type="button"
                class="shrink-0 p-1.5 rounded-sm text-ink-dim hover:text-ink hover:bg-surface-3 focus:outline-none focus:ring-1 focus:ring-accent"
                :aria-label="`Open project ${project.name}`"
                :data-testid="`open-project-${project.id}`"
                @click.stop="openProjectPage(project.id)"
              >
                <FileText :size="12" />
              </button>
            </template>
          </div>

          <!-- nested sessions for this project -->
          <ul
            v-if="!isCollapsed(project.id) && sessionsFor(project.id).length > 0"
            class="mt-1 ml-3 space-y-1 border-l border-border-muted pl-2"
          >
            <li
              v-for="session in sessionsFor(project.id)"
              :key="session.id"
              class="flex items-stretch gap-1"
              :draggable="renamingId !== session.id"
              :data-testid="`session-row-${session.id}`"
              @dragstart="onSessionDragStart($event, session.id)"
              @dragend="onSessionDragEnd"
            >
              <template v-if="renamingId === session.id">
                <input
                  :ref="setRenameInputRef"
                  v-model="renameDraft"
                  class="flex-1 min-w-0 px-3 py-2 rounded-sm border border-accent bg-surface-2 text-sm font-ui text-ink focus:outline-none"
                  :data-testid="`rename-session-input-${session.id}`"
                  @keydown.enter="commitRename(session.id)"
                  @keydown.escape="cancelRename"
                  @blur="commitRename(session.id)"
                />
              </template>
              <template v-else>
                <button
                  type="button"
                  class="flex-1 min-w-0 flex items-center gap-2 px-3 py-2 rounded-sm text-left text-sm font-ui transition-fast ease-kenaz"
                  :class="
                    activeSessionId === session.id
                      ? 'text-ink bg-surface-2 ring-1 ring-accent-hairline'
                      : 'text-ink-muted hover:text-ink hover:bg-surface-2'
                  "
                  :title="session.name || session.id"
                  :aria-current="activeSessionId === session.id ? 'page' : undefined"
                  :data-testid="`open-session-${session.id}`"
                  @click="openSession(session.id)"
                >
                  <MessageSquare
                    :size="14"
                    :class="activeSessionId === session.id ? 'text-accent' : ''"
                  />
                  <span class="truncate hidden two-col:inline">
                    {{ session.name }}
                  </span>
                </button>
                <button
                  type="button"
                  class="shrink-0 p-2 rounded-sm text-ink-dim hover:text-ink hover:bg-surface-3"
                  :aria-label="`Rename session ${session.name}`"
                  :data-testid="`rename-session-${session.id}`"
                  @click="startRename(session.id, session.name, $event)"
                >
                  <Pencil :size="13" />
                </button>
                <button
                  type="button"
                  class="shrink-0 p-2 rounded-sm text-ink-dim hover:text-signal-danger hover:bg-surface-3"
                  :aria-label="`Delete session ${session.name}`"
                  :disabled="deletingId === session.id"
                  :data-testid="`delete-session-${session.id}`"
                  @click="deleteSession(session.id, $event)"
                >
                  <X :size="14" />
                </button>
              </template>
            </li>
          </ul>
        </li>
      </ul>

      <!-- loose sessions (no project) -->
      <div
        v-if="looseSessions.length > 0 || projectList.length > 0"
        class="mt-3"
      >
        <div
          class="px-2 py-1 rounded-sm font-ui text-[10px] uppercase tracking-[0.18em] text-ink-subtle"
          :class="dropTargetId === '__loose__' ? 'bg-accent-glow ring-1 ring-accent' : ''"
          data-testid="loose-header"
          @dragover="onProjectDragOver($event, '__loose__')"
          @dragleave="onProjectDragLeave($event, '__loose__')"
          @drop="onProjectDrop($event, '__loose__')"
        >
          Loose
        </div>
        <ul class="space-y-1">
          <li
            v-for="session in looseSessions"
            :key="session.id"
            class="flex items-stretch gap-1"
            :draggable="renamingId !== session.id"
            :data-testid="`session-row-${session.id}`"
            @dragstart="onSessionDragStart($event, session.id)"
            @dragend="onSessionDragEnd"
          >
            <template v-if="renamingId === session.id">
              <input
                :ref="setRenameInputRef"
                v-model="renameDraft"
                class="flex-1 min-w-0 px-3 py-2 rounded-sm border border-accent bg-surface-2 text-sm font-ui text-ink focus:outline-none"
                :data-testid="`rename-session-input-${session.id}`"
                @keydown.enter="commitRename(session.id)"
                @keydown.escape="cancelRename"
                @blur="commitRename(session.id)"
              />
            </template>
            <template v-else>
              <button
                type="button"
                class="flex-1 min-w-0 flex items-center gap-2 px-3 py-2 rounded-sm text-left text-sm font-ui transition-fast ease-kenaz"
                :class="
                  activeSessionId === session.id
                    ? 'text-ink bg-surface-2 ring-1 ring-accent-hairline'
                    : 'text-ink-muted hover:text-ink hover:bg-surface-2'
                "
                :title="session.name || session.id"
                :aria-current="activeSessionId === session.id ? 'page' : undefined"
                :data-testid="`open-session-${session.id}`"
                @click="openSession(session.id)"
              >
                <MessageSquare
                  :size="14"
                  :class="activeSessionId === session.id ? 'text-accent' : ''"
                />
                <span class="truncate hidden two-col:inline">
                  {{ session.name }}
                </span>
              </button>
              <button
                type="button"
                class="shrink-0 p-2 rounded-sm text-ink-dim hover:text-ink hover:bg-surface-3"
                :aria-label="`Rename session ${session.name}`"
                :data-testid="`rename-session-${session.id}`"
                @click="startRename(session.id, session.name, $event)"
              >
                <Pencil :size="13" />
              </button>
              <button
                type="button"
                class="shrink-0 p-2 rounded-sm text-ink-dim hover:text-signal-danger hover:bg-surface-3"
                :aria-label="`Delete session ${session.name}`"
                :disabled="deletingId === session.id"
                :data-testid="`delete-session-${session.id}`"
                @click="deleteSession(session.id, $event)"
              >
                <X :size="14" />
              </button>
            </template>
          </li>
        </ul>
      </div>

      <li
        v-if="sessionList.length === 0 && projectList.length === 0"
        class="px-3 py-2 text-xs text-ink-subtle list-none"
      >
        No sessions yet
      </li>

      <div v-if="sessionList.length > 0" class="mt-2 px-2">
        <button
          type="button"
          class="flex items-center gap-1.5 px-2 py-1 rounded-sm text-[11px] uppercase tracking-[0.16em] text-ink-dim hover:text-signal-danger transition-fast"
          :title="`Delete all ${sessionList.length} sessions`"
          :data-testid="'clear-all-sessions'"
          @click="clearAll"
        >
          <Trash2 :size="12" />
          <span class="hidden two-col:inline">
            Clear all ({{ sessionList.length }})
          </span>
        </button>
      </div>
    </nav>

    <!-- project context menu -->
    <div
      v-if="projectMenu"
      class="fixed z-50 rounded-sm border border-border-muted bg-surface-0 shadow-lg py-1"
      :style="{ left: projectMenu.x + 'px', top: projectMenu.y + 'px' }"
      data-testid="project-menu"
      @click.stop
    >
      <button
        v-for="p in projectList.filter((x) => x.id === projectMenu?.id)"
        :key="p.id"
        type="button"
        class="block w-full px-3 py-1.5 text-left font-ui text-xs text-ink hover:bg-surface-2"
        :data-testid="`project-menu-rename-${p.id}`"
        @click="startProjectRename(p)"
      >
        Rename
      </button>
      <button
        v-for="p in projectList.filter((x) => x.id === projectMenu?.id)"
        :key="p.id + '-del'"
        type="button"
        class="block w-full px-3 py-1.5 text-left font-ui text-xs text-signal-danger hover:bg-surface-2"
        :data-testid="`project-menu-delete-${p.id}`"
        @click="startProjectDelete(p)"
      >
        Delete
      </button>
    </div>

    <!-- delete project confirmation modal -->
    <div
      v-if="deleteModal"
      class="fixed inset-0 z-50 flex items-center justify-center"
      role="dialog"
      aria-modal="true"
      data-testid="delete-project-modal"
    >
      <div class="absolute inset-0 bg-modal-overlay" @click="cancelProjectDelete" />
      <div
        class="relative z-10 w-[420px] max-w-[90vw] rounded-md border border-border-muted bg-surface-0 shadow-lg p-5"
      >
        <h2 class="font-ui text-base font-semibold text-ink">
          Delete project “{{ deleteModal.project.name }}”?
        </h2>
        <p class="mt-2 font-ui text-xs text-ink-muted">
          Sessions in this project become loose unless you opt to delete
          them as well.
        </p>
        <label class="mt-3 flex items-center gap-2 font-ui text-xs text-ink">
          <input
            v-model="deleteModal.cascade"
            type="checkbox"
            data-testid="delete-project-cascade"
          />
          Also delete all sessions in this project
        </label>
        <div class="mt-4 flex justify-end gap-2">
          <button
            type="button"
            class="font-ui text-xs px-3 py-1.5 text-ink-dim hover:text-ink"
            @click="cancelProjectDelete"
          >
            Cancel
          </button>
          <button
            type="button"
            class="font-ui text-xs px-3 py-1.5 rounded-sm border border-signal-danger text-signal-danger hover:bg-surface-2"
            data-testid="delete-project-confirm"
            @click="commitProjectDelete"
          >
            Delete
          </button>
        </div>
      </div>
    </div>

    <!-- primary-surfaces nav -->
    <nav class="px-2 py-2 border-t border-border-muted" aria-label="Surfaces">
      <ul class="space-y-1">
        <li><RailEntry :icon="MessageSquare" label="Sessions" to="/sessions" /></li>
        <li><RailEntry :icon="Wrench" label="Tools" to="/tools" /></li>
        <li><RailEntry :icon="GitBranch" label="Workflows" to="/workflows" /></li>
        <li><RailEntry :icon="FileText" label="Contexts" to="/contexts" /></li>
        <li><RailEntry :icon="Brain" label="Memory" to="/memory" /></li>
        <li><RailEntry :icon="FileText" label="Audit log" to="/audit" /></li>
        <li><RailEntry :icon="Settings" label="Settings" to="/settings" /></li>
      </ul>
    </nav>
  </div>
</template>
