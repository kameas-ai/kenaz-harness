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
 */
import { computed, onBeforeUnmount, onMounted, ref } from 'vue';
import CanvasHead from '@/shell/CanvasHead.vue';
import { Plus, FileText } from '@/shell/icons';
import { useHarnessClient } from '@/lib/useHarnessAPI';
import type { ContextNode } from '@/lib/types';
import ContextTree from './ContextTree.vue';
import ContextPreview from './ContextPreview.vue';
import GlobalContextPanel from '@/components/settings/GlobalContextPanel.vue';
import ContextRecent from './ContextRecent.vue';

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

async function createSampleFolder() {
  // The "+ Folder" affordance creates an empty placeholder folder so
  // the user can populate it via the OS file manager. Default name is
  // a timestamp so concurrent clicks don't collide; inline rename
  // lands in a follow-up.
  const stamp = new Date().toISOString().replace(/[:.]/g, '-');
  try {
    await client.contexts.createFolder(`folder-${stamp}`);
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
  <div class="h-full flex flex-col">
    <CanvasHead
      number="07"
      section="CONTEXTS"
      title="Context library"
      :subtitle="
        rootPath
          ? `Markdown + text files in ${rootPath}. Drop a file in the folder or use the “+ Folder” affordance to organise.`
          : 'Markdown + text files attached to sessions, projects, or globally. Local-only — nothing leaves the device.'
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
          <button
            type="button"
            class="text-[11px] text-ink-dim hover:text-accent flex items-center gap-1"
            data-testid="context-create-folder"
            @click="createSampleFolder"
          >
            <Plus :size="12" />
            <span>Folder</span>
          </button>
        </header>
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
              @click="createSampleFolder"
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
