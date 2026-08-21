<script setup lang="ts">
/**
 * SessionHeader — WP05 suggest-title affordance + plan-mode badge.
 *
 * Renders the session name and a "Suggest new title" button.
 * When the session already has a user-set title (name is non-empty and
 * autoTitled is falsy), clicking the button first shows a confirm modal
 * before calling Sessions_SuggestTitle. Auto-titled sessions get
 * re-titled immediately without a modal (the title was machine-chosen,
 * so there's nothing to protect).
 *
 * The PLAN MODE badge (plan-mode-posture-01KZNP3F WP06) is shown
 * whenever the session is in plan_mode. Initial state is seeded from
 * the session's resolved autonomy (so reopened plan_mode sessions show
 * the badge immediately); live updates arrive via the plan_mode_changed
 * event handled by usePlanMode.
 */
import { ref, nextTick, onMounted } from 'vue';
import { useHarnessClient, useProjects } from '@/lib/useHarnessAPI';
import type { Session } from '@/lib/types';
import AutonomyChip from '@/components/chat/AutonomyChip.vue';
import PlanModeBadge from '@/components/chat/PlanModeBadge.vue';
import { usePlanMode } from '@/lib/planmode';

const props = defineProps<{
  session: Session;
}>();

const emit = defineEmits<{
  'title-changed': [];
}>();

const client = useHarnessClient();

// ── Rename (session-actions-relocation) ──────────────────────────────
// Rename moved out of the left-rail rows: clicking the title here turns
// it into an inline editor. Empty submission clears the title so the
// session falls back to auto-titling (mirrors the rail's old behavior).
const renaming = ref(false);
const renameDraft = ref('');

function startRename() {
  renameDraft.value = props.session.name ?? '';
  renaming.value = true;
}

function setRenameInputRef(el: unknown) {
  if (el instanceof HTMLInputElement) {
    el.focus();
    el.select();
  }
}

async function commitRename() {
  if (!renaming.value) return;
  const next = renameDraft.value.trim();
  renaming.value = false;
  lastError.value = null;
  try {
    if (!next) {
      await client.sessions.clearTitle(props.session.id);
    } else {
      if (next === props.session.name) return;
      await client.sessions.rename(props.session.id, next);
    }
    emit('title-changed');
  } catch (err) {
    lastError.value = err instanceof Error ? err.message : String(err);
  }
}

function cancelRename() {
  renaming.value = false;
  renameDraft.value = '';
}

// ── Move to project (session-actions-relocation) ─────────────────────
const { list: projectList, refresh: refreshProjects } = useProjects();
const moveMenuOpen = ref(false);

async function toggleMoveMenu() {
  moveMenuOpen.value = !moveMenuOpen.value;
  if (moveMenuOpen.value) {
    await refreshProjects();
    await nextTick();
  }
}

async function moveTo(projectId: string) {
  moveMenuOpen.value = false;
  const targetPid = projectId === '__loose__' ? '' : projectId;
  if ((props.session.projectId ?? '') === targetPid) return;
  lastError.value = null;
  try {
    await client.sessions.moveToProject(props.session.id, targetPid);
    emit('title-changed');
  } catch (err) {
    lastError.value = err instanceof Error ? err.message : String(err);
  }
}

// ── Plan-mode badge state ─────────────────────────────────────────────
const { isActive: planModeActive, pendingPlanId, setActive: setPlanModeActive } = usePlanMode(props.session.id);

// Seed initial plan-mode state from the session's autonomy profile so
// reopened plan_mode sessions show the badge immediately on mount.
//
// NG7 (served-mode-is-a-real-mode-01PMZ707 WP04): the line below used to
// claim "badge defaults to hidden; live events correct it" on a failed
// read. That escape hatch does not hold — NO producer emits a plan-mode
// event at all (audit dead-code-audit-2026-08-18.md:1385), so a badge that
// misses this seed stays hidden for the rest of the session, on desktop
// too, not just when resolveAutonomy is refused. Sessions_ResolveAutonomy
// IS ported for served mode (WP04), so this read now succeeds there; the
// comment is corrected rather than the catch behaviour changed, because
// there is still no live-event fallback to invoke.
onMounted(async () => {
  try {
    const resolved = await client.sessions.resolveAutonomy(props.session.id);
    if (resolved?.session?.postureMode === 'plan_mode') {
      setPlanModeActive(true);
    }
  } catch {
    // The badge stays hidden on a failed read. There is no live-event
    // correction path today (see NG7 above) — this is a real gap, not a
    // deferred one.
  }
});

const suggesting = ref(false);
const confirmOpen = ref(false);
const lastError = ref<string | null>(null);

// ── Export affordance (session-export-01NDFSEX05 WP03) ───────────────
// WP03: replaced window.confirm picker with a proper dropdown menu.
const exporting = ref(false);
const exportMenuOpen = ref(false);

function openExportMenu() {
  exportMenuOpen.value = !exportMenuOpen.value;
}

async function exportAs(format: 'markdown' | 'json') {
  exportMenuOpen.value = false;
  if (exporting.value) return;
  exporting.value = true;
  lastError.value = null;
  try {
    await client.sessions.export(props.session.id, format);
  } catch (err) {
    const msg = err instanceof Error ? err.message : String(err);
    if (!msg.includes('cancelled')) {
      lastError.value = `Export failed: ${msg}`;
    }
  } finally {
    exporting.value = false;
  }
}

/** True when the session has a user-set title that needs confirmation before overwriting. */
function needsConfirm(): boolean {
  return Boolean(props.session.name) && !props.session.autoTitled;
}

async function onSuggestClick() {
  lastError.value = null;
  if (needsConfirm()) {
    confirmOpen.value = true;
    return;
  }
  await doSuggest();
}

async function doSuggest() {
  confirmOpen.value = false;
  if (suggesting.value) return;
  suggesting.value = true;
  try {
    await client.sessions.suggestTitle(props.session.id);
    emit('title-changed');
  } catch (err) {
    lastError.value = err instanceof Error ? err.message : String(err);
  } finally {
    suggesting.value = false;
  }
}

function cancelConfirm() {
  confirmOpen.value = false;
}
</script>

<template>
  <header
    data-testid="session-header"
    class="flex items-center gap-2 px-4 py-2 border-b border-hairline"
  >
    <input
      v-if="renaming"
      :ref="setRenameInputRef"
      v-model="renameDraft"
      class="flex-1 min-w-0 px-2 py-1 rounded-sm border border-accent bg-surface-2 text-sm font-ui text-ink focus:outline-none"
      :data-testid="`rename-session-input-${session.id}`"
      @keydown.enter="commitRename"
      @keydown.escape="cancelRename"
      @blur="commitRename"
    />
    <button
      v-else
      type="button"
      class="flex-1 min-w-0 truncate text-left text-sm font-medium text-ink hover:text-accent transition-colors"
      title="Rename session"
      data-testid="rename-session-title"
      @click="startRename"
    >
      {{ session.name || session.id }}
    </button>

    <PlanModeBadge
      :is-active="planModeActive"
      :pending-plan-id="pendingPlanId"
    />

    <AutonomyChip :session-id="session.id" />

    <!-- Export dropdown (WP03: replaced window.confirm picker) -->
    <div class="relative shrink-0">
      <button
        type="button"
        data-testid="export-session-btn"
        class="text-xs px-2 py-1 rounded text-ink-muted hover:text-ink hover:bg-surface-2 transition-colors"
        :disabled="exporting"
        aria-haspopup="menu"
        :aria-expanded="exportMenuOpen ? 'true' : 'false'"
        @click="openExportMenu"
      >
        Export…
      </button>
      <button
        v-if="exportMenuOpen"
        type="button"
        class="fixed inset-0 z-40 cursor-default"
        aria-hidden="true"
        tabindex="-1"
        @click="exportMenuOpen = false"
      />
      <div
        v-if="exportMenuOpen"
        class="absolute right-0 top-full z-50 mt-1 min-w-[9rem] rounded-sm border border-border-muted bg-surface-2 shadow-lg"
        role="menu"
      >
        <button
          type="button"
          role="menuitem"
          class="block w-full px-3 py-1.5 text-left text-xs text-ink hover:bg-surface-3"
          data-testid="export-markdown"
          @click="exportAs('markdown')"
        >
          Markdown (.md)
        </button>
        <button
          type="button"
          role="menuitem"
          class="block w-full px-3 py-1.5 text-left text-xs text-ink hover:bg-surface-3"
          data-testid="export-json"
          @click="exportAs('json')"
        >
          JSON (.json)
        </button>
        <button
          type="button"
          role="menuitem"
          class="block w-full px-3 py-1.5 text-left text-xs text-ink-muted hover:bg-surface-3"
          data-testid="export-cancel"
          @click="exportMenuOpen = false"
        >
          Cancel
        </button>
      </div>
    </div>

    <!-- Move-to-project (relocated from the left-rail rows) -->
    <div class="relative shrink-0">
      <button
        type="button"
        data-testid="move-session-btn"
        class="text-xs px-2 py-1 rounded text-ink-muted hover:text-ink hover:bg-surface-2 transition-colors"
        aria-haspopup="menu"
        :aria-expanded="moveMenuOpen ? 'true' : 'false'"
        @click="toggleMoveMenu"
      >
        Move…
      </button>
      <!-- click-away backdrop -->
      <button
        v-if="moveMenuOpen"
        type="button"
        class="fixed inset-0 z-40 cursor-default"
        aria-hidden="true"
        tabindex="-1"
        @click="moveMenuOpen = false"
      />
      <div
        v-if="moveMenuOpen"
        role="menu"
        aria-label="Move session to project"
        data-testid="move-session-menu"
        class="absolute right-0 top-full mt-1 z-50 rounded-sm border border-border-muted bg-surface-0 shadow-lg py-1 min-w-[160px]"
      >
        <div class="px-3 py-1 font-ui text-[10px] uppercase tracking-[0.18em] text-ink-subtle">
          Move to…
        </div>
        <button
          v-for="p in projectList.filter((x) => x.id !== (session.projectId ?? ''))"
          :key="p.id"
          type="button"
          role="menuitem"
          class="block w-full px-3 py-1.5 text-left font-ui text-xs text-ink hover:bg-surface-2"
          :data-testid="`move-session-to-${p.id}`"
          @click="moveTo(p.id)"
        >
          {{ p.name }}
        </button>
        <button
          v-if="session.projectId"
          type="button"
          role="menuitem"
          class="block w-full px-3 py-1.5 text-left font-ui text-xs text-ink hover:bg-surface-2"
          data-testid="move-session-to-global"
          @click="moveTo('__loose__')"
        >
          Global (no project)
        </button>
        <div
          v-if="!session.projectId && projectList.filter((x) => x.id !== (session.projectId ?? '')).length === 0"
          class="px-3 py-1.5 font-ui text-xs text-ink-subtle"
        >
          No other projects
        </div>
      </div>
    </div>

    <button
      type="button"
      data-testid="suggest-title-btn"
      class="shrink-0 text-xs px-2 py-1 rounded text-ink-muted hover:text-ink hover:bg-surface-2 transition-colors"
      :disabled="suggesting"
      @click="onSuggestClick"
    >
      Suggest new title
    </button>

    <p
      v-if="lastError"
      class="w-full text-xs text-signal-danger mt-1"
    >
      {{ lastError }}
    </p>

    <!-- Confirm-overwrite modal — shown only when session has a user-set title -->
    <div
      v-if="confirmOpen"
      data-testid="suggest-title-confirm-modal"
      class="fixed inset-0 z-50 flex items-center justify-center bg-black/40"
      role="dialog"
      aria-modal="true"
    >
      <div class="bg-surface-1 rounded-lg shadow-xl p-6 max-w-sm w-full mx-4 flex flex-col gap-4">
        <p class="text-sm text-ink">
          This will replace the current title with an auto-generated one. Continue?
        </p>
        <div class="flex justify-end gap-2">
          <button
            type="button"
            data-testid="suggest-title-confirm-cancel"
            class="px-3 py-1.5 text-sm rounded text-ink-muted hover:bg-surface-2"
            @click="cancelConfirm"
          >
            Cancel
          </button>
          <button
            type="button"
            data-testid="suggest-title-confirm-ok"
            class="px-3 py-1.5 text-sm rounded bg-accent text-white hover:bg-accent/90"
            @click="doSuggest"
          >
            Replace title
          </button>
        </div>
      </div>
    </div>
  </header>
</template>
