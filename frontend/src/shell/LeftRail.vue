<script setup lang="ts">
import { computed, ref, onMounted, onBeforeUnmount } from 'vue';
import { useRoute, useRouter } from 'vue-router';
import RailEntry from './RailEntry.vue';
import SessionTreeRow from './SessionTreeRow.vue';
import {
  Archive,
  Plus,
  MessageSquare,
  Package,
  Wrench,
  FileText,
  Settings,
  Brain,
  GitBranch,
  Globe,
  Route,
  Trash2,
  X,
  ChevronDown,
  ChevronRight,
  Folder,
} from './icons';
import { useSessions, useProjects, useHarnessClient } from '@/lib/useHarnessAPI';
import { signedIn, capability } from '@/lib/featureFlags';
import { isServedMode } from '@/lib/useServedMode';
import { useConnectionState } from '@/lib/useConnectionState';
import NewSessionDialog from './NewSessionDialog.vue';
import MemoryBadge from './MemoryBadge.vue';
import WorkflowRunsSection from '@/components/workflows/WorkflowRunsSection.vue';
import ConfirmDialog from '@/components/ui/ConfirmDialog.vue';
import { useConfirmDialog } from '@/composables/useConfirmDialog';
import type { Project, Session } from '@/lib/types';
import '@/styles/sessions.css';

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
const newSessionProjectId = ref<string | undefined>(undefined);
const deletingId = ref<string | null>(null);
const { confirmState, confirm } = useConfirmDialog();

// Served-mode connection gate (FR-003): creating a session hits the backend
// (Sessions_Create). When the served transport is lost, disable the
// new-session affordances so a click can't silently fail. No-op in native
// mode — isServedMode() is false there, so the desktop rail is never gated.
const served = isServedMode();
const connection = useConnectionState();
const backendUnavailable = computed(() => served && connection.value === 'lost');

// WP07 — drag-and-drop session-to-project membership. The dragged
// session id is captured at dragstart; project headers + the Loose
// header act as drop targets. drag-over targets are highlighted via
// `dropTargetId` so the user gets a visual hint during the drag.
const draggedSessionId = ref<string | null>(null);
const dropTargetId = ref<string | null>(null); // project id or '__loose__'
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
// eslint-disable-next-line @typescript-eslint/no-explicit-any
function setProjectRenameRef(el: any) {
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

// ── Branching UX (branching-ux-polish-01KQ8TD7 WP03) ────────────────────────

/**
 * HARNESS_BRANCHING_POLISH feature flag. Defaults to true (on) per plan §4.
 * Can be overridden by localStorage for operator debugging.
 */
const BRANCHING_POLISH_ENABLED: boolean = (() => {
  try {
    const v = localStorage.getItem('harness.feature.branchingPolish');
    if (v === 'off' || v === 'false') return false;
  } catch { /* ignore */ }
  return true;
})();

/**
 * hadStoredBranchCollapsed — true iff localStorage held a branch-collapse
 * entry (possibly empty) at mount time. Distinguishes "the user has never
 * touched a collapse toggle" (WP03 may auto-seed) from "the user's chosen
 * set happens to be empty" (WP03 must not overwrite that choice).
 */
let hadStoredBranchCollapsed = false;

/** Branch-collapse state — keyed by parent session id. */
const branchCollapsed = ref<Set<string>>((() => {
  try {
    const raw = localStorage.getItem('harness.sidebar.branchCollapsed.v1');
    if (raw) {
      hadStoredBranchCollapsed = true;
      const arr: string[] = JSON.parse(raw);
      if (Array.isArray(arr)) return new Set(arr);
    }
  } catch { /* ignore */ }
  return new Set<string>();
})());

function saveBranchCollapsed(s: Set<string>) {
  try {
    localStorage.setItem(
      'harness.sidebar.branchCollapsed.v1',
      JSON.stringify([...s]),
    );
  } catch { /* ignore */ }
}

function isBranchCollapsed(parentId: string): boolean {
  return branchCollapsed.value.has(parentId);
}

function toggleBranchCollapsed(parentId: string) {
  const next = new Set(branchCollapsed.value);
  if (next.has(parentId)) {
    next.delete(parentId);
  } else {
    next.add(parentId);
  }
  branchCollapsed.value = next;
  saveBranchCollapsed(next);
}

/**
 * sessionsByParent — Map<parentSessionId | '__root__', Session[]>.
 * Used by the branch tree renderer to group children under their parent.
 */
const sessionsByParent = computed<Map<string, Session[]>>(() => {
  const map = new Map<string, Session[]>();
  for (const s of sessionList.value) {
    const key = s.parentSessionId ?? '__root__';
    const list = map.get(key) ?? [];
    list.push(s);
    map.set(key, list);
  }
  return map;
});

function childSessionsOf(parentId: string): Session[] {
  return sessionsByParent.value.get(parentId) ?? [];
}

function hasChildren(sessionId: string): boolean {
  return (sessionsByParent.value.get(sessionId)?.length ?? 0) > 0;
}

/**
 * Max visible branch depth. Seeded from settings in onMounted below
 * (controls-and-readouts-that-tell-the-truth-01PMZ808 WP01) — the
 * hardcoded 5 here is only the pre-settings-load fallback for the first
 * paint, matching LegendBar.vue's client.settings.get() pattern one
 * directory over.
 */
const maxBranchDepth = ref<number>(5);
const client = useHarnessClient();

// WP09: Listen for the palette's 'New Session' action dispatched via
// kenaz:open-new-session CustomEvent from useCommandPalette.
function onOpenNewSessionEvent() {
  newSession();
}

onMounted(async () => {
  await refreshSessions();
  refreshProjects();
  window.addEventListener('kenaz:open-new-session', onOpenNewSessionEvent);

  // controls-and-readouts-that-tell-the-truth-01PMZ808 WP01 + WP03.
  // client.settings.get() returns a Promise; it cannot be read in the
  // ref initialiser above (synchronous), so both dials are seeded here.
  try {
    const s = await client.settings.get();
    if (typeof s.maxVisibleBranchDepth === 'number' && s.maxVisibleBranchDepth > 0) {
      maxBranchDepth.value = s.maxVisibleBranchDepth;
    }
    // s.autoCollapseBranchesInSidebar is optional-boolean over the wire;
    // `?? true` mirrors the same fallback SettingsView.vue's toggle
    // already uses and matches the backend's EffectiveAutoCollapseBranches-
    // InSidebar() default. Only seed when the user has never touched a
    // collapse toggle in this browser profile — a real (even empty)
    // stored choice always wins.
    const autoCollapse = s.autoCollapseBranchesInSidebar ?? true;
    if (autoCollapse && !hadStoredBranchCollapsed) {
      const parentsWithChildren = new Set<string>();
      for (const [parentId, children] of sessionsByParent.value) {
        if (parentId !== '__root__' && children.length > 0) {
          parentsWithChildren.add(parentId);
        }
      }
      if (parentsWithChildren.size > 0) {
        branchCollapsed.value = parentsWithChildren;
      }
    }
  } catch {
    // Settings unavailable — both dials keep their pre-seed fallbacks
    // (maxBranchDepth=5, branchCollapsed from localStorage/empty).
  }
});

onBeforeUnmount(() => {
  window.removeEventListener('kenaz:open-new-session', onOpenNewSessionEvent);
});

function newSession(projectId?: string) {
  newSessionProjectId.value = projectId;
  newSessionDialogOpen.value = true;
}

async function onNewSessionDialogClose() {
  newSessionDialogOpen.value = false;
  // Defensive double-refresh: the dialog calls
  //   client.sessions.create() → client.sessions.moveToProject()
  // directly (not via the useSessions composable) AND emits 'close'
  // synchronously between those steps and a router.push() — so the
  // first refresh can race with the moveToProject's commit visibility.
  // A second refresh on a 100ms tail catches any commit lag and
  // guarantees the new session appears under its project header.
  await refreshSessions();
  await refreshProjects();
  setTimeout(() => {
    void refreshSessions();
    void refreshProjects();
  }, 120);
}

function openSession(id: string) {
  if (!id || deletingId.value === id) return;
  void router.push(`/sessions/${id}`);
}

function openProjectPage(id: string) {
  if (!id) return;
  void router.push(`/projects/${id}`);
}

async function deleteSession(id: string, event: Event) {
  event.preventDefault();
  event.stopPropagation();
  if (!id || deletingId.value) return;
  // WP03 review fix: route single-session delete through ConfirmDialog.
  const session = sessionList.value.find((s) => s.id === id);
  const sessionName = session?.name || id;
  const ok = await confirm({
    title: `Delete "${sessionName}"?`,
    message: 'This cannot be undone.',
    danger: true,
    confirmLabel: 'Delete',
  });
  if (!ok) return;
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
  const ok = await confirm({
    title: `Delete all ${sessionList.value.length} sessions?`,
    message: 'This cannot be undone.',
    danger: true,
    confirmLabel: 'Delete all',
  });
  if (!ok) {
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

/**
 * Opens the project context menu positioned relative to a trigger element.
 * Used by the ⋯ keyboard-accessible icon button (WP04 D-01 fix).
 */
function openProjectMenuFromElement(p: Project, el: EventTarget | null) {
  const rect = (el instanceof HTMLElement ? el : null)?.getBoundingClientRect?.();
  const x = rect ? rect.left : 0;
  const y = rect ? rect.bottom : 0;
  projectMenu.value = { id: p.id, x, y };
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

/**
 * Native drag handler for tree-view SessionTreeRow rows.
 * SessionTreeRow emits no component event for drag — the native dragstart
 * bubbles up from the <li> root. We read the session id from the
 * data-session-id attribute so the handler works even on detached elements
 * (important for the second leg of a project→loose drag in tests).
 */
function onTreeSessionDragStart(evt: DragEvent) {
  const target = evt.currentTarget as HTMLElement | null;
  const sessionId = target?.dataset?.sessionId ?? '';
  if (sessionId) onSessionDragStart(evt, sessionId);
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
    <!-- new-session affordance + memory badge -->
    <div class="px-2 pt-3 pb-2 flex items-center gap-1">
      <button
        type="button"
        class="flex items-center gap-2 px-3 py-2 rounded-sm flex-1 text-left text-sm font-ui text-accent border border-accent-hairline hover:bg-accent-glow transition-fast ease-kenaz disabled:opacity-50 disabled:cursor-not-allowed"
        aria-label="New session"
        :disabled="backendUnavailable"
        :title="backendUnavailable ? 'Connection to the harness backend lost — reconnecting…' : undefined"
        @click="newSession(activeProjectId || undefined)"
      >
        <Plus :size="14" />
        <span class="hidden two-col:inline">New session</span>
      </button>
      <!-- Memory trust badge (v0.5.6) — shows memory chunk count for the
           active project (or all chunks when no project is selected). -->
      <MemoryBadge :project-id="activeProjectId || undefined" />
    </div>
    <NewSessionDialog
      :open="newSessionDialogOpen"
      :initial-project-id="newSessionProjectId"
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
              <!-- D-01 fix (WP04): keyboard-accessible ⋯ options button -->
              <button
                type="button"
                class="shrink-0 p-1.5 rounded-sm text-ink-dim hover:text-ink hover:bg-surface-3 focus:outline-none focus:ring-1 focus:ring-accent"
                :aria-label="`Project options for ${project.name}`"
                aria-haspopup="menu"
                :aria-expanded="projectMenu?.id === project.id ? 'true' : 'false'"
                :data-testid="`project-options-${project.id}`"
                @click.stop="openProjectMenuFromElement(project, $event.currentTarget)"
              >
                ⋯
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
              <button
                type="button"
                class="shrink-0 p-1.5 rounded-sm text-ink-dim hover:text-accent hover:bg-surface-3 focus:outline-none focus:ring-1 focus:ring-accent disabled:opacity-50 disabled:cursor-not-allowed"
                :aria-label="`New session in ${project.name}`"
                :data-testid="`new-session-in-project-${project.id}`"
                :disabled="backendUnavailable"
                @click.stop="newSession(project.id)"
              >
                <Plus :size="12" />
              </button>
            </template>
          </div>

          <!-- nested sessions for this project -->
          <ul
            v-if="!isCollapsed(project.id)"
            class="mt-1 ml-3 space-y-1 pl-2"
            :class="{ 'border-l border-border-muted': sessionsFor(project.id).length > 0 }"
          >
            <li
              v-for="session in sessionsFor(project.id)"
              :key="session.id"
              class="group flex items-stretch gap-1"
              :draggable="true"
              :data-testid="`session-row-${session.id}`"
              @dragstart="onSessionDragStart($event, session.id)"
              @dragend="onSessionDragEnd"
            >
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
                <span
                  class="truncate hidden two-col:inline"
                  :class="session.autoTitled ? 'session-row__name--auto' : ''"
                  :data-testid="session.autoTitled ? `auto-titled-name-${session.id}` : undefined"
                >
                  {{ session.name }}
                </span>
              </button>
              <!-- Delete is the only inline action; rename/export/move live in
                   the chat-pane header. Hidden at rest so the name owns the
                   row; revealed on hover or keyboard focus. -->
              <div class="shrink-0 hidden group-hover:flex group-focus-within:flex items-center">
                <button
                  type="button"
                  class="p-2 rounded-sm text-ink-dim hover:text-signal-danger hover:bg-surface-3"
                  :aria-label="`Delete session ${session.name}`"
                  :disabled="deletingId === session.id"
                  :data-testid="`delete-session-${session.id}`"
                  @click="deleteSession(session.id, $event)"
                >
                  <X :size="14" />
                </button>
              </div>
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
          Global
        </div>
        <!-- Branch-tree view (branching-ux-polish-01KQ8TD7 WP03) -->
        <ul v-if="BRANCHING_POLISH_ENABLED" class="space-y-0.5" data-testid="loose-sessions-tree">
          <template
            v-for="session in looseSessions.filter((s) => !s.parentSessionId)"
            :key="session.id"
          >
            <SessionTreeRow
              :session="session"
              :depth="0"
              :has-children="hasChildren(session.id)"
              :is-collapsed="isBranchCollapsed(session.id)"
              :is-active="activeSessionId === session.id"
              :max-depth="maxBranchDepth"
              :draggable="true"
              @open="openSession"
              @toggle-collapse="toggleBranchCollapsed"
              @dragstart="onTreeSessionDragStart"
              @dragend="onSessionDragEnd"
            >
              <template #actions>
                <button
                  type="button"
                  class="p-2 rounded-sm text-ink-dim hover:text-signal-danger hover:bg-surface-3"
                  :aria-label="`Delete session ${session.name}`"
                  :disabled="deletingId === session.id"
                  :data-testid="`delete-session-${session.id}`"
                  @click.stop="deleteSession(session.id, $event)"
                >
                  <X :size="14" />
                </button>
              </template>
            </SessionTreeRow>
            <!-- Recursively render children if not collapsed -->
            <template v-if="hasChildren(session.id) && !isBranchCollapsed(session.id)">
              <SessionTreeRow
                v-for="child in childSessionsOf(session.id)"
                :key="child.id"
                :session="child"
                :depth="(child.branchDepth ?? 1)"
                :has-children="hasChildren(child.id)"
                :is-collapsed="isBranchCollapsed(child.id)"
                :is-active="activeSessionId === child.id"
                :max-depth="maxBranchDepth"
                @open="openSession"
                @toggle-collapse="toggleBranchCollapsed"
              >
                <template #actions>
                  <button
                    type="button"
                    class="p-2 rounded-sm text-ink-dim hover:text-signal-danger hover:bg-surface-3"
                    :aria-label="`Delete session ${child.name}`"
                    :disabled="deletingId === child.id"
                    :data-testid="`delete-session-${child.id}`"
                    @click.stop="deleteSession(child.id, $event)"
                  >
                    <X :size="14" />
                  </button>
                </template>
              </SessionTreeRow>
            </template>
          </template>
        </ul>
        <!-- Flat view (feature flag off) -->
        <ul v-else class="space-y-1">
          <li
            v-for="session in looseSessions"
            :key="session.id"
            class="group flex items-stretch gap-1"
            :draggable="true"
            :data-testid="`session-row-${session.id}`"
            @dragstart="onSessionDragStart($event, session.id)"
            @dragend="onSessionDragEnd"
          >
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
              <span
                class="truncate hidden two-col:inline"
                :class="session.autoTitled ? 'session-row__name--auto' : ''"
                :data-testid="session.autoTitled ? `auto-titled-name-${session.id}` : undefined"
              >
                {{ session.name }}
              </span>
            </button>
            <!-- Delete is the only inline action; rename/export/move live in
                 the chat-pane header. Hidden at rest; revealed on hover/focus. -->
            <div class="shrink-0 hidden group-hover:flex group-focus-within:flex items-center">
              <button
                type="button"
                class="p-2 rounded-sm text-ink-dim hover:text-signal-danger hover:bg-surface-3"
                :aria-label="`Delete session ${session.name}`"
                :disabled="deletingId === session.id"
                :data-testid="`delete-session-${session.id}`"
                @click="deleteSession(session.id, $event)"
              >
                <X :size="14" />
              </button>
            </div>
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

    <!-- project context menu (D-01 WP04: role=menu for keyboard accessibility) -->
    <div
      v-if="projectMenu"
      role="menu"
      :aria-label="`Options for project`"
      class="fixed z-50 rounded-sm border border-border-muted bg-surface-0 shadow-lg py-1"
      :style="{ left: projectMenu.x + 'px', top: projectMenu.y + 'px' }"
      data-testid="project-menu"
      @click.stop
      @keydown.escape.stop="closeProjectMenu"
    >
      <button
        v-for="p in projectList.filter((x) => x.id === projectMenu?.id)"
        :key="p.id"
        type="button"
        role="menuitem"
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
        role="menuitem"
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
          Sessions in this project become global unless you opt to delete
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

    <!-- WP10 — workflow runs section (hidden when no runs) -->
    <WorkflowRunsSection />

    <!-- primary-surfaces nav -->
    <nav class="px-2 py-2 border-t border-border-muted" aria-label="Surfaces">
      <ul class="space-y-1">
        <li><RailEntry :icon="MessageSquare" label="Sessions" to="/sessions" /></li>
        <li><RailEntry :icon="Wrench" label="Tools" to="/tools" /></li>
        <li><RailEntry :icon="GitBranch" label="Workflows" to="/workflows" /></li>
        <li><RailEntry :icon="FileText" label="Contexts" to="/contexts" /></li>
        <li><RailEntry :icon="Brain" label="Memory" to="/memory" /></li>
        <li><RailEntry :icon="Archive" label="Artifacts" to="/artifacts" /></li>
        <!-- agentgraph-total-convergence-01PMGX01 WP16: Agent graphs RESTORED to
             top-level nav, reversing nav-settings-ia-cleanup WP03's demotion.
             WP03 demoted it because the surface had nothing real in it: a
             library of hand-authored templates and an editor whose palette
             offered kinds that crashed at run time. Since then every run
             materializes as a graph (WP12) and the routed topology is the
             production chat path, so this is now where you go to see what the
             agent actually did. A substrate nobody can reach is a substrate
             that rots — 19 of 34 kinds went unexercised while this was
             palette-only. The nav.agentgraph command-palette action stays. -->
        <li data-testid="nav-agentgraph">
          <RailEntry :icon="Route" label="Agent graphs" to="/agentgraph" />
        </li>
        <!-- nav-settings-ia-cleanup WP04: Audit log demoted from top-level nav.
             Viewer is accessible via Settings → Security → Audit Log. /audit route
             and the command palette entry (nav.audit) remain intact. -->
        <!-- Sites + Marketplace are desktop-only. Neither route is registered
             in main-served.ts, and the served RPC allowlist
             (core/serve/methods.go) carries no Sites_* or Catalog_* method, so
             in a browser build these entries would navigate into the
             not-found route at best. See docs/served-mode-boundary.md.
             (docs/dead-code-audit-2026-08-16.md findings A4 + B4) -->
        <li
          v-if="!served && signedIn && capability('sites_hosting')"
          data-testid="nav-sites"
        >
          <RailEntry :icon="Globe" label="Sites" to="/sites" />
        </li>
        <li
          v-if="!served && signedIn"
          data-testid="nav-marketplace"
        >
          <RailEntry :icon="Package" label="Marketplace" to="/marketplace" />
        </li>
        <li><RailEntry :icon="Settings" label="Settings" to="/settings" /></li>
      </ul>
    </nav>
  </div>

  <!-- Destructive action confirmation (WP03) -->
  <ConfirmDialog
    v-bind="confirmState"
    @confirm="confirmState.resolve(true)"
    @cancel="confirmState.resolve(false)"
  />
</template>
