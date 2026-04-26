<script setup lang="ts">
/**
 * ContextsView — the Context Library landing page.
 *
 * Three-column layout (plan §6 / spec §6):
 *   1. Tree   (left)   — folders + files rooted at <DataDir>/contexts/
 *   2. Preview (centre) — read-only render of the selected file
 *   3. Recent (right)   — N most-recently-applied paths (LRU JSON)
 *
 * WP01 scope: read-only browsing + preview. No in-place editor (WP05),
 * no scope assignment (WP04). The empty-state card surfaces the
 * library root path so users know where to drop files.
 */
import { computed, onMounted, ref } from 'vue';
import CanvasHead from '@/shell/CanvasHead.vue';
import { Plus, FileText } from '@/shell/icons';
import { useHarnessClient } from '@/lib/useHarnessAPI';
import type { ContextNode } from '@/lib/types';
import ContextTree from './ContextTree.vue';
import ContextPreview from './ContextPreview.vue';
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

const hasFiles = computed(() => {
  const t = tree.value;
  return t !== null && Array.isArray(t.children) && t.children.length > 0;
});

async function loadTree() {
  treeError.value = null;
  try {
    tree.value = await client.contexts.list();
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

async function createSampleFolder() {
  // The "+ Folder" affordance creates an empty placeholder folder so
  // the user can populate it via the OS file manager (drag-and-drop
  // / Finder integration lands in WP05). The default name is
  // user-editable in WP05's inline rename; for WP01 we ship a
  // timestamped placeholder so concurrent clicks don't collide.
  const stamp = new Date().toISOString().replace(/[:.]/g, '-');
  try {
    await client.contexts.createFolder(`folder-${stamp}`);
    await loadTree();
  } catch (e) {
    treeError.value = e instanceof Error ? e.message : 'Folder create failed.';
  }
}

onMounted(() => {
  void loadTree();
  void loadRecent();
  void loadRoot();
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
    />

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

      <!-- centre: preview -->
      <ContextPreview
        :path="selectedPath"
        :content="previewContent"
        :loading="previewLoading"
        :error="previewError"
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
