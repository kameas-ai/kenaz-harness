<script setup lang="ts">
/**
 * ContextsView — the Context Library landing page.
 *
 * Three-column layout (plan §6 / spec §6):
 *   1. Tree   (left)   — folders + files rooted at <DataDir>/contexts/
 *   2. Preview (centre) — read view + in-place editor (WP05)
 *   3. Recent (right)   — N most-recently-applied paths (LRU JSON)
 *
 * WP05 additions on top of WP01:
 *   - Inline editor in ContextPreview wired through `client.contexts.save`
 *     (Save → re-fetch tree so size + modified refresh).
 *   - "Show hidden" toggle in the page header (calls `listAll` instead
 *     of `list` when on; chassis-internal .trash + .recent.json stay
 *     hidden either way).
 *   - "Import file…" button reads a local `.md` / `.markdown` / `.txt`
 *     and saves it under the currently-selected directory (or the
 *     library root when nothing is selected).
 *   - Listener on the `contexts:tree-changed` topic emitted by the Go
 *     side fsnotify watcher (WP05 watcher polish). External writes
 *     fan out to every subscriber within ~200 ms; we re-fetch the
 *     tree and surface a brief "external change detected" toast.
 *
 * WP07 additions (fleet-context-graph-sync-01NDFSEX17):
 *   - Sync status strip in the tree panel header (team cap, pull count,
 *     cursor). Hidden when fleet is not signed in / team cap absent.
 *   - "Share to team" affordance in the preview panel for files when the
 *     team-graph capability is active. Gated on `syncStatus.team_cap_enabled`.
 *   - First-publish confirm: "This entry will be visible to your org" so
 *     users don't accidentally publish secrets into a shared layer (NFR-006).
 *   - `publish` calls `client.contexts.publish` with a deterministic nodeID
 *     derived from the path (sha-ish stable ID approach: md5-hex of the path
 *     is not available in the browser; instead we use btoa(path) as the ID,
 *     which is stable across sessions for the same file path).
 */
import { computed, nextTick, onBeforeUnmount, onMounted, ref } from 'vue';
import CanvasHead from '@/shell/CanvasHead.vue';
import { Plus, FileText } from '@/shell/icons';
import { useHarnessClient } from '@/lib/useHarnessAPI';
import { useServedMode } from '@/lib/useServedMode';
import NotAvailableInServedMode from '@/components/ui/NotAvailableInServedMode.vue';
import type { ContextNode, ContextSyncStatusView, ContextPublishResult } from '@/lib/types';
import ContextTree from './ContextTree.vue';
import ContextPreview from './ContextPreview.vue';
import GlobalContextPanel from '@/components/settings/GlobalContextPanel.vue';
import ContextRecent from './ContextRecent.vue';

const servedMode = useServedMode();
const client = useHarnessClient();

const tree = ref<ContextNode | null>(null);
const recent = ref<readonly string[]>([]);
const rootPath = ref<string>('');
const treeError = ref<string | null>(null);

const selectedPath = ref<string | null>(null);
const previewContent = ref<string>('');
const previewLoading = ref(false);
const previewError = ref<string | null>(null);

const showHidden = ref(false);
const externalChangeToast = ref(false);
let externalChangeToastTimer: ReturnType<typeof setTimeout> | null = null;

const importInputRef = ref<HTMLInputElement | null>(null);
const importError = ref<string | null>(null);

// ── Fleet context-graph sync (WP07) ──────────────────────────────────────────

/** Snapshot of the fleet context-graph syncer state. */
const syncStatus = ref<ContextSyncStatusView | null>(null);

/**
 * showPublishConfirm controls the "visible to your org" first-publish
 * dialog. The dialog must be confirmed before the actual publish call.
 */
const showPublishConfirm = ref(false);
/** Result of the most recent publish call (shown inline). */
const publishResult = ref<ContextPublishResult | null>(null);
/** Error string from the last publish call. */
const publishError = ref<string | null>(null);
/** Whether a publish call is in progress. */
const publishLoading = ref(false);

/** teamCapEnabled is true when fleet has the team-graph sharing cap. */
const teamCapEnabled = computed(
  () => syncStatus.value?.team_cap_enabled ?? false,
);

/** Stable node ID for the selected file (btoa of path). */
const selectedNodeID = computed(() => {
  if (!selectedPath.value) return '';
  try {
    return btoa(selectedPath.value);
  } catch {
    return selectedPath.value;
  }
});

async function loadSyncStatus() {
  try {
    syncStatus.value = await client.contexts.syncStatus();
  } catch {
    // Fleet not wired — treat as local-only.
    syncStatus.value = null;
  }
}

/**
 * openPublishConfirm — show the "visible to your org" confirm dialog.
 * Only called when teamCapEnabled and a file is selected.
 */
function openPublishConfirm() {
  publishError.value = null;
  publishResult.value = null;
  showPublishConfirm.value = true;
}

/**
 * confirmPublish — the user clicked "Yes, share" in the confirm dialog.
 * Calls client.contexts.publish with the selected file's metadata.
 * The nodeID is derived from the path (stable cross-session).
 */
async function confirmPublish() {
  showPublishConfirm.value = false;
  if (!selectedPath.value || !previewContent.value) return;
  publishLoading.value = true;
  publishError.value = null;
  publishResult.value = null;
  try {
    const result = await client.contexts.publish({
      node_id: selectedNodeID.value,
      layer: 'team',
      kind: 'guidance',
      title: selectedPath.value.replace(/.*\//, '').replace(/\.[^.]+$/, ''),
      body: previewContent.value,
      version: 1,
    });
    publishResult.value = result;
    // Reload sync status to show updated pull count.
    await loadSyncStatus();
  } catch (e) {
    publishError.value = e instanceof Error ? e.message : 'Publish failed.';
  } finally {
    publishLoading.value = false;
  }
}

const hasFiles = computed(() => {
  const t = tree.value;
  return t !== null && Array.isArray(t.children) && t.children.length > 0;
});

async function loadTree() {
  treeError.value = null;
  try {
    tree.value = showHidden.value
      ? await client.contexts.listAll()
      : await client.contexts.list();
  } catch (e) {
    tree.value = null;
    treeError.value = e instanceof Error ? e.message : 'Failed to load library.';
  }
}

async function loadRecent() {
  try {
    recent.value = await client.contexts.recentlyApplied(10);
  } catch {
    recent.value = [];
  }
}

async function loadRoot() {
  try {
    rootPath.value = await client.contexts.rootPath();
  } catch {
    rootPath.value = '';
  }
}

async function selectFile(path: string) {
  selectedPath.value = path;
  previewContent.value = '';
  previewError.value = null;
  previewLoading.value = true;
  try {
    previewContent.value = await client.contexts.get(path);
  } catch (e) {
    previewError.value = e instanceof Error ? e.message : 'Failed to read file.';
  } finally {
    previewLoading.value = false;
  }
}

async function savePreview(payload: { path: string; content: string }) {
  // Throws on failure — ContextPreview surfaces the error inline.
  await client.contexts.save(payload.path, payload.content);
  previewContent.value = payload.content;
  await loadTree();
}

// Inline "new folder" prompt state. The "+ Folder" affordance reveals a
// text input in the tree header; the user types a name and presses Enter
// to create it. The folder nests under the current selection (a selected
// folder, or the parent of a selected file) so the user controls WHERE it
// lands, and names it so it isn't an opaque timestamp.
const creatingFolder = ref(false);
const newFolderName = ref('');
const newFolderInputRef = ref<HTMLInputElement | null>(null);

function beginCreateFolder() {
  treeError.value = null;
  creatingFolder.value = true;
  newFolderName.value = '';
  void nextTick(() => newFolderInputRef.value?.focus());
}

function cancelCreateFolder() {
  creatingFolder.value = false;
  newFolderName.value = '';
}

// folderParentPath returns the library-relative directory the new folder
// should be created in, derived from the current selection (folder → into
// it; file → its parent; nothing selected → root). Mirrors importTargetPath.
function folderParentPath(): string {
  const sel = selectedPath.value;
  if (!sel) return '';
  const node = findNode(tree.value, sel);
  if (!node) return '';
  if (node.kind === 'folder') return sel;
  const i = sel.lastIndexOf('/');
  return i === -1 ? '' : sel.slice(0, i);
}

async function confirmCreateFolder() {
  // Sanitize: a folder name is a single path segment. Strip slashes and
  // surrounding whitespace so the name can't escape the chosen parent.
  const name = newFolderName.value.trim().replace(/[/\\]+/g, '-');
  if (!name) {
    cancelCreateFolder();
    return;
  }
  const parent = folderParentPath();
  const path = parent ? `${parent}/${name}` : name;
  try {
    await client.contexts.createFolder(path);
    cancelCreateFolder();
    await loadTree();
  } catch (e) {
    treeError.value = e instanceof Error ? e.message : 'Folder create failed.';
  }
}

async function toggleShowHidden() {
  showHidden.value = !showHidden.value;
  await loadTree();
}

function openImportDialog() {
  importError.value = null;
  importInputRef.value?.click();
}

/**
 * importTargetPath — the slash-separated library-relative path the
 * imported file should land at. When the user has a folder selected
 * in the tree, the import drops in there; otherwise it lands at the
 * root. The selected file is the file's basename — we don't preserve
 * the OS-side directory tree because that would cross the library
 * boundary in confusing ways.
 */
function importTargetPath(name: string): string {
  const sel = selectedPath.value;
  if (!sel) return name;
  // If the selection is a file, drop its basename and use its
  // parent directory. If it's a folder, use it directly.
  const node = findNode(tree.value, sel);
  if (!node) return name;
  if (node.kind === 'folder') {
    return sel ? `${sel}/${name}` : name;
  }
  const i = sel.lastIndexOf('/');
  return i === -1 ? name : `${sel.slice(0, i)}/${name}`;
}

function findNode(root: ContextNode | null, path: string): ContextNode | null {
  if (!root) return null;
  if (root.path === path) return root;
  for (const c of root.children ?? []) {
    const hit = findNode(c, path);
    if (hit) return hit;
  }
  return null;
}

async function onImportChange(ev: Event) {
  const input = ev.target as HTMLInputElement;
  const file = input.files?.[0];
  // Reset so re-picking the same file fires `change` again.
  input.value = '';
  if (!file) return;
  importError.value = null;
  try {
    const text = await file.text();
    const target = importTargetPath(file.name);
    await client.contexts.save(target, text);
    await loadTree();
    selectedPath.value = target;
    previewContent.value = text;
  } catch (e) {
    importError.value = e instanceof Error ? e.message : 'Import failed.';
  }
}

function flashExternalChangeToast() {
  externalChangeToast.value = true;
  if (externalChangeToastTimer) {
    clearTimeout(externalChangeToastTimer);
  }
  externalChangeToastTimer = setTimeout(() => {
    externalChangeToast.value = false;
    externalChangeToastTimer = null;
  }, 1500);
}

let unsubscribeExternal: (() => void) | undefined;

onMounted(() => {
  void loadTree();
  void loadRecent();
  void loadRoot();
  void loadSyncStatus();
  if (typeof window !== 'undefined' && window.runtime?.EventsOn) {
    unsubscribeExternal = window.runtime.EventsOn(
      'contexts:tree-changed',
      () => {
        flashExternalChangeToast();
        void loadTree();
      },
    );
  }
});

onBeforeUnmount(() => {
  if (unsubscribeExternal) {
    unsubscribeExternal();
  }
  if (externalChangeToastTimer) {
    clearTimeout(externalChangeToastTimer);
    externalChangeToastTimer = null;
  }
});
</script>

<template>
  <NotAvailableInServedMode
    v-if="servedMode"
    feature="Contexts"
  />
  <div
    v-else
    class="h-full flex flex-col"
  >
    <CanvasHead
      number="07"
      section="CONTEXTS"
      title="Context library"
      :subtitle="
        rootPath
          ? `Markdown + text files in ${rootPath}. Drop a file in the folder or use the “+ Folder” affordance to organise.`
          : 'Markdown + text files attached to sessions, projects, or globally. Local-only — context files never leave the device (fleet config-apply ACKs and opted-in telemetry are the only egress when fleet config distribution is active).'
      "
    >
      <template #trailing>
        <div class="flex items-center gap-3">
          <label
            class="flex items-center gap-1.5 font-ui text-[11px] text-ink-muted cursor-pointer"
          >
            <input
              type="checkbox"
              class="accent-accent"
              :checked="showHidden"
              data-testid="context-show-hidden"
              @change="toggleShowHidden"
            />
            Show hidden
          </label>
          <button
            type="button"
            class="font-ui text-[11px] text-ink-dim hover:text-accent flex items-center gap-1"
            data-testid="context-import"
            @click="openImportDialog"
          >
            <Plus :size="12" />
            <span>Import file…</span>
          </button>
          <input
            ref="importInputRef"
            type="file"
            accept=".md,.markdown,.txt"
            class="hidden"
            data-testid="context-import-input"
            @change="onImportChange"
          />
        </div>
      </template>
    </CanvasHead>

    <!-- Global-scope attachments — moved here from Settings (every
         session inherits these as the prefix). -->
    <div class="px-4 pt-3 pb-1 border-b border-border-muted">
      <GlobalContextPanel />
    </div>

    <div
      v-if="externalChangeToast"
      class="px-4 py-1 bg-surface-1 border-b border-border-muted font-ui text-[11px] text-ink-muted"
      role="status"
      data-testid="context-external-change-toast"
    >
      External change detected, refreshing…
    </div>
    <div
      v-if="importError"
      class="px-4 py-1 bg-surface-1 border-b border-border-muted font-ui text-[11px] text-signal-danger"
      role="alert"
      data-testid="context-import-error"
    >
      {{ importError }}
    </div>

    <!-- Sync status strip — only shown when fleet team-graph cap is active -->
    <div
      v-if="teamCapEnabled"
      class="px-4 py-1 bg-surface-1 border-b border-border-muted font-ui text-[11px] text-ink-muted flex items-center gap-3"
      data-testid="context-sync-status-strip"
    >
      <span class="flex items-center gap-1">
        <span
          class="inline-block w-2 h-2 rounded-full bg-signal-success"
          data-testid="context-sync-cap-indicator"
        />
        <span>Team sync active</span>
      </span>
      <span v-if="syncStatus && syncStatus.pull_count > 0" data-testid="context-sync-pull-count">
        {{ syncStatus.pull_count }} shared entr{{ syncStatus.pull_count === 1 ? 'y' : 'ies' }} received
      </span>
      <span v-if="syncStatus && syncStatus.last_pull_err" class="text-signal-danger" data-testid="context-sync-pull-err">
        Pull error: {{ syncStatus.last_pull_err }}
      </span>
      <span v-if="syncStatus && syncStatus.last_push_err" class="text-signal-danger" data-testid="context-sync-push-err">
        Push error: {{ syncStatus.last_push_err }}
      </span>
    </div>

    <!-- Publish confirm dialog — first-publish "visible to your org" warning -->
    <div
      v-if="showPublishConfirm"
      class="fixed inset-0 z-50 flex items-center justify-center bg-black/40"
      data-testid="context-publish-confirm-overlay"
    >
      <div
        class="bg-surface-1 rounded-xl shadow-xl border border-border-muted p-6 max-w-sm w-full mx-4"
        data-testid="context-publish-confirm-dialog"
      >
        <h2 class="font-ui font-semibold text-sm text-ink mb-2">Share with your team?</h2>
        <p class="font-ui text-[12px] text-ink-muted leading-relaxed mb-4">
          This entry will be visible to everyone in your organisation. Do not share credentials,
          private keys, or sensitive personal information in shared layers.
        </p>
        <div class="flex justify-end gap-3">
          <button
            type="button"
            class="font-ui text-[12px] text-ink-dim hover:text-ink px-3 py-1.5 rounded-md border border-border-muted"
            data-testid="context-publish-confirm-cancel"
            @click="showPublishConfirm = false"
          >
            Cancel
          </button>
          <button
            type="button"
            class="font-ui text-[12px] text-accent hover:text-accent-muted px-3 py-1.5 rounded-md bg-accent/10 hover:bg-accent/20"
            data-testid="context-publish-confirm-ok"
            @click="confirmPublish"
          >
            Yes, share
          </button>
        </div>
      </div>
    </div>

    <!-- Publish result / error toast -->
    <div
      v-if="publishResult"
      class="px-4 py-1 bg-surface-1 border-b border-border-muted font-ui text-[11px] text-signal-success"
      data-testid="context-publish-result"
    >
      Published ({{ publishResult.accepted_nodes }} node{{ publishResult.accepted_nodes === 1 ? '' : 's' }})
      <span v-if="publishResult.conflicts && publishResult.conflicts.length > 0" class="text-signal-warning ml-2">
        · {{ publishResult.conflicts.length }} version conflict{{ publishResult.conflicts.length === 1 ? '' : 's' }}
      </span>
    </div>
    <div
      v-if="publishError"
      class="px-4 py-1 bg-surface-1 border-b border-border-muted font-ui text-[11px] text-signal-danger"
      data-testid="context-publish-error"
    >
      {{ publishError }}
    </div>

    <div class="flex-1 grid grid-cols-[260px_1fr_240px] min-h-0">
      <!-- left: tree -->
      <nav
        class="border-r border-border-muted bg-surface-0 flex flex-col min-h-0"
        aria-label="Context tree"
      >
        <header class="px-3 py-2 border-b border-border-muted flex items-center gap-2">
          <span class="font-ui text-[10px] uppercase tracking-[0.18em] text-ink-subtle flex-1">
            Library
          </span>
          <!-- Publish affordance — only when team cap enabled and a file is selected -->
          <button
            v-if="teamCapEnabled && selectedPath"
            type="button"
            class="text-[11px] text-accent hover:text-accent-muted flex items-center gap-1 disabled:opacity-50"
            :disabled="publishLoading"
            data-testid="context-publish-btn"
            @click="openPublishConfirm"
          >
            <span v-if="publishLoading">Sharing…</span>
            <span v-else>Share to team</span>
          </button>
          <button
            type="button"
            class="text-[11px] text-ink-dim hover:text-accent flex items-center gap-1"
            data-testid="context-create-folder"
            @click="beginCreateFolder"
          >
            <Plus :size="12" />
            <span>Folder</span>
          </button>
        </header>
        <!-- Inline new-folder name prompt. Revealed by "+ Folder"; the
             folder is created under the current selection (shown in the
             hint) so the user controls name AND location. -->
        <div
          v-if="creatingFolder"
          class="px-2 pt-2"
          data-testid="context-new-folder-row"
        >
          <input
            ref="newFolderInputRef"
            v-model="newFolderName"
            type="text"
            placeholder="Folder name…"
            aria-label="New folder name"
            spellcheck="false"
            autocomplete="off"
            class="w-full rounded-sm border border-border-muted bg-surface-1 px-2 py-1 font-ui text-[12px] text-ink focus:border-accent focus:outline-none"
            data-testid="context-new-folder-input"
            @keydown.enter.prevent="confirmCreateFolder"
            @keydown.esc.prevent="cancelCreateFolder"
            @blur="cancelCreateFolder"
          />
          <p class="mt-1 font-ui text-[10px] text-ink-subtle">
            Creates in
            <span class="font-mono">{{ folderParentPath() || 'library root' }}</span>
            · Enter to create, Esc to cancel
          </p>
        </div>
        <div class="flex-1 overflow-y-auto px-2 py-2">
          <div
            v-if="treeError"
            class="px-2 py-2 font-ui text-[12px] text-signal-danger"
            role="alert"
          >
            {{ treeError }}
          </div>
          <div
            v-else-if="!hasFiles"
            class="px-2 py-3 space-y-2"
            data-testid="context-empty-state"
          >
            <div class="flex items-center gap-2 text-ink">
              <FileText :size="14" />
              <span class="font-ui text-sm">No contexts yet</span>
            </div>
            <p class="font-ui text-[12px] text-ink-muted leading-relaxed">
              Drop a <span class="font-mono">.md</span> /
              <span class="font-mono">.markdown</span> /
              <span class="font-mono">.txt</span> file into the library
              folder, then refresh this page.
            </p>
            <p
              v-if="rootPath"
              class="font-mono text-[11px] text-ink-subtle break-all"
            >
              {{ rootPath }}
            </p>
            <button
              type="button"
              class="font-ui text-[12px] text-accent hover:text-accent-muted"
              data-testid="context-empty-create-folder"
              @click="beginCreateFolder"
            >
              Create a folder →
            </button>
          </div>
          <ContextTree
            v-else-if="tree"
            :node="tree"
            :selected-path="selectedPath"
            :is-root="true"
            @select="selectFile"
          />
        </div>
      </nav>

      <!-- centre: preview / editor -->
      <ContextPreview
        :path="selectedPath"
        :content="previewContent"
        :loading="previewLoading"
        :error="previewError"
        :on-save="savePreview"
      />

      <!-- right: recents -->
      <ContextRecent
        :paths="recent"
        :selected-path="selectedPath"
        @select="selectFile"
      />
    </div>
  </div>
</template>
