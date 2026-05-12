<script setup lang="ts">
/**
 * ScheduledChatsPanel — Settings → Scheduled Chats tab.
 *
 * Mission: scheduled-chat-runs-01KX5R8B (WP05).
 *
 * Lists all scheduled chat runs with create / edit / delete /
 * enable-toggle affordances.  Delegates to ScheduledChatFormModal
 * for create/edit.
 */
import { ref, onMounted } from 'vue';
import ScheduledChatFormModal from './ScheduledChatFormModal.vue';
import {
  createScheduledChatClient,
  type ScheduledChatClient,
  type ScheduledChatEntry,
} from '@/lib/scheduledChatClient';

const props = defineProps<{
  /** Test seam: inject a fake client; production creates one. */
  client?: ScheduledChatClient;
}>();

const chatClient: ScheduledChatClient = props.client ?? createScheduledChatClient();

// ── state ─────────────────────────────────────────────────────────────────

const runs = ref<ScheduledChatEntry[]>([]);
const loading = ref(false);
const loadError = ref<string | null>(null);
const actionError = ref<string | null>(null);

// ── modal state ───────────────────────────────────────────────────────────

const showModal = ref(false);
const editingRun = ref<ScheduledChatEntry | null>(null);

function openCreate() {
  editingRun.value = null;
  showModal.value = true;
}

function openEdit(run: ScheduledChatEntry) {
  editingRun.value = run;
  showModal.value = true;
}

function closeModal() {
  showModal.value = false;
  editingRun.value = null;
}

// ── data loading ──────────────────────────────────────────────────────────

async function loadRuns() {
  loading.value = true;
  loadError.value = null;
  try {
    runs.value = await chatClient.list();
  } catch (err) {
    loadError.value = err instanceof Error ? err.message : String(err);
  } finally {
    loading.value = false;
  }
}

onMounted(loadRuns);

// ── actions ───────────────────────────────────────────────────────────────

async function handleSaved(_entry: ScheduledChatEntry) {
  closeModal();
  await loadRuns();
}

async function deleteRun(id: string) {
  actionError.value = null;
  try {
    await chatClient.remove(id);
    await loadRuns();
  } catch (err) {
    actionError.value = err instanceof Error ? err.message : String(err);
  }
}

async function toggleEnabled(run: ScheduledChatEntry) {
  actionError.value = null;
  try {
    await chatClient.setEnabled(run.id, !run.enabled);
    await loadRuns();
  } catch (err) {
    actionError.value = err instanceof Error ? err.message : String(err);
  }
}

async function runNow(id: string) {
  actionError.value = null;
  try {
    await chatClient.runNow(id);
    // Show brief confirmation by reloading (history update).
  } catch (err) {
    actionError.value = err instanceof Error ? err.message : String(err);
  }
}

function fmtDate(iso: string): string {
  try {
    return new Date(iso).toLocaleString();
  } catch {
    return iso;
  }
}
</script>

<template>
  <div class="space-y-4" data-testid="scheduled-chats-panel">
    <!-- Header row -->
    <div class="flex items-center justify-between">
      <h2 class="font-ui text-sm font-medium text-ink">Scheduled Chats</h2>
      <button
        type="button"
        class="rounded-sm border border-border-muted bg-surface-2 px-3 py-1.5 font-ui text-xs text-ink hover:bg-surface-1"
        data-testid="scheduled-chats-create-btn"
        @click="openCreate"
      >
        + New
      </button>
    </div>

    <!-- Action error banner -->
    <div
      v-if="actionError"
      class="rounded-sm border border-red-700 bg-red-950 px-3 py-2 font-ui text-sm text-red-200"
      data-testid="scheduled-chats-action-error"
    >
      {{ actionError }}
    </div>

    <!-- Loading -->
    <div
      v-if="loading && runs.length === 0"
      class="font-ui text-sm text-ink-muted py-4"
      data-testid="scheduled-chats-loading"
    >
      Loading…
    </div>

    <!-- Load error -->
    <div
      v-else-if="loadError"
      class="rounded-sm border border-red-700 bg-red-950 p-4 font-ui text-sm text-red-200"
      data-testid="scheduled-chats-load-error"
    >
      {{ loadError }}
    </div>

    <!-- Empty state -->
    <div
      v-else-if="runs.length === 0"
      class="rounded-sm border border-border-muted bg-surface-1 p-6"
      data-testid="scheduled-chats-empty"
    >
      <p class="font-ui text-sm text-ink mb-1">No scheduled chats yet.</p>
      <p class="font-ui text-sm text-ink-muted">
        Create one with the "+ New" button to run a prompt on a cron schedule.
      </p>
    </div>

    <!-- Run list -->
    <ul v-else class="space-y-2" data-testid="scheduled-chats-list">
      <li
        v-for="run in runs"
        :key="run.id"
        class="rounded-sm border border-border-muted bg-surface-1 px-4 py-3"
        :data-testid="`scheduled-chat-row-${run.id}`"
      >
        <div class="flex items-start justify-between gap-3">
          <!-- Info -->
          <div class="min-w-0">
            <div class="flex items-center gap-2">
              <span class="font-ui text-sm font-medium text-ink truncate">
                {{ run.name || run.id }}
              </span>
              <span
                v-if="!run.enabled"
                class="rounded px-1.5 py-0.5 font-ui text-xs bg-surface-2 text-ink-muted"
              >
                disabled
              </span>
            </div>
            <div class="mt-0.5 flex items-center gap-3">
              <span class="font-mono text-xs text-ink-muted">{{ run.cron }}</span>
              <span v-if="run.timezone" class="font-mono text-xs text-ink-muted">
                {{ run.timezone }}
              </span>
              <span v-if="run.model" class="font-ui text-xs text-ink-muted">
                {{ run.model }}
              </span>
              <span class="font-ui text-xs text-ink-muted">→ {{ run.outputSink }}</span>
            </div>
            <div class="mt-0.5 font-ui text-xs text-ink-muted">
              Updated {{ fmtDate(run.updatedAt) }}
            </div>
          </div>

          <!-- Actions -->
          <div class="flex items-center gap-1.5 shrink-0">
            <button
              type="button"
              class="rounded-sm border border-border-muted bg-surface-2 px-2 py-0.5 font-ui text-xs text-ink hover:bg-surface-1"
              :data-testid="`scheduled-chat-run-now-${run.id}`"
              @click="runNow(run.id)"
            >
              Run now
            </button>
            <button
              type="button"
              class="rounded-sm border border-border-muted bg-surface-2 px-2 py-0.5 font-ui text-xs text-ink hover:bg-surface-1"
              :data-testid="`scheduled-chat-edit-${run.id}`"
              @click="openEdit(run)"
            >
              Edit
            </button>
            <button
              type="button"
              class="rounded-sm border border-border-muted bg-surface-2 px-2 py-0.5 font-ui text-xs text-ink-muted hover:text-ink"
              :data-testid="`scheduled-chat-toggle-${run.id}`"
              @click="toggleEnabled(run)"
            >
              {{ run.enabled ? 'Pause' : 'Resume' }}
            </button>
            <button
              type="button"
              class="rounded-sm border border-red-800 bg-red-950 px-2 py-0.5 font-ui text-xs text-red-300 hover:text-red-100"
              :data-testid="`scheduled-chat-delete-${run.id}`"
              @click="deleteRun(run.id)"
            >
              Delete
            </button>
          </div>
        </div>
      </li>
    </ul>

    <!-- Create/Edit modal -->
    <ScheduledChatFormModal
      v-if="showModal"
      :client="chatClient"
      :editing="editingRun"
      @saved="handleSaved"
      @cancel="closeModal"
    />
  </div>
</template>
