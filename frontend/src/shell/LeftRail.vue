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
} from './icons';
import { useSessions } from '@/lib/useHarnessAPI';
import NewSessionDialog from './NewSessionDialog.vue';

/**
 * LeftRail — three vertical regions (plan §2.1):
 *   1. New-session affordance (top)
 *   2. Sessions list (middle, scrollable, hover-to-delete per row)
 *   3. Primary-surfaces nav (bottom)
 *
 * Collapses to icons-only at < 960px (--breakpoint-two-col, FR-016).
 */
// Destructure so each ref becomes a top-level setup binding — Vue's
// template auto-unwrap only fires for top-level refs, NOT for refs
// reached through a parent object (sessions.list would surface the
// Ref wrapper itself, which v-for would iterate as marker booleans).
const {
  list: sessionList,
  refresh: refreshSessions,
  create: createSession,
  rename: renameSession,
  remove: removeSession,
} = useSessions();
const newSessionDialogOpen = ref(false);
const deletingId = ref<string | null>(null);
const renamingId = ref<string | null>(null);
const renameDraft = ref('');
// Track which session we've already focused so the :ref callback
// (which Vue runs on every re-render of the input, not just mount)
// does not re-select all text after every keystroke.
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

const activeSessionId = computed(() => {
  const p = route.params.id;
  return typeof p === 'string' ? p : '';
});

onMounted(() => {
  refreshSessions();
});

function newSession() {
  newSessionDialogOpen.value = true;
}

async function onNewSessionDialogClose() {
  newSessionDialogOpen.value = false;
  // Dialog handles create + navigate itself; refresh so the new
  // row appears in the rail without a page reload.
  await refreshSessions();
}

function openSession(id: string) {
  if (!id || deletingId.value === id || renamingId.value === id) return;
  void router.push(`/sessions/${id}`);
}

function startRename(id: string, currentName: string, event: Event) {
  event.preventDefault();
  event.stopPropagation();
  if (renamingId.value || deletingId.value) return;
  renamingId.value = id;
  renameDraft.value = currentName;
  // Focus is wired via the :ref callback when the input mounts.
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
</script>

<template>
  <div class="flex flex-col h-full">
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

    <!-- sessions list -->
    <nav
      class="flex-1 overflow-y-auto px-2 py-1"
      aria-label="Sessions"
    >
      <ul class="space-y-1">
        <li
          v-for="session in sessionList"
          :key="session.id"
          class="flex items-stretch gap-1"
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
              class="shrink-0 p-2 rounded-sm text-ink-dim hover:text-ink hover:bg-surface-3 focus:outline-none focus:ring-1 focus:ring-accent disabled:opacity-50"
              :aria-label="`Rename session ${session.name}`"
              :data-testid="`rename-session-${session.id}`"
              @click="startRename(session.id, session.name, $event)"
            >
              <Pencil :size="13" />
            </button>
            <button
              type="button"
              class="shrink-0 p-2 rounded-sm text-ink-dim hover:text-signal-danger hover:bg-surface-3 focus:outline-none focus:ring-1 focus:ring-accent disabled:opacity-50"
              :aria-label="`Delete session ${session.name}`"
              :disabled="deletingId === session.id"
              :data-testid="`delete-session-${session.id}`"
              @click="deleteSession(session.id, $event)"
            >
              <X :size="14" />
            </button>
          </template>
        </li>
        <li v-if="sessionList.length === 0" class="px-3 py-2 text-xs text-ink-subtle">
          No sessions yet
        </li>
      </ul>
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
