<script setup lang="ts">
/**
 * AttachmentTreePicker — modal-style picker that wraps the existing
 * ContextTree from WP01 in select-mode.
 *
 * Workflow:
 *   1. Mount → `Contexts_List` populates the tree.
 *   2. User clicks a file → emits `select` with the relative path.
 *   3. Caller calls `Attachments_Add` with
 *      `content_source = "library:<path>"` and the snapshot read at
 *      add-time (FR-202 — snapshot survives source deletion).
 *
 * The component takes care of the snapshot read so callers don't have
 * to plumb `Contexts_Get` themselves; the parent only needs to supply
 * the (scopeKind, scopeId) target.
 */

import { onMounted, ref, watch } from 'vue';
import ContextTree from '@/views/contexts/ContextTree.vue';
import type {
  Attachment,
  AttachmentScopeKind,
  ContextNode,
} from '@/lib/types';
import { useHarnessClient } from '@/lib/harnessClientContext';
import { X } from '@/shell/icons';

const props = defineProps<{
  open: boolean;
  scopeKind: AttachmentScopeKind;
  scopeId: string;
  /** Default kind for added attachments. Defaults to 'system'. */
  attachmentKind?: 'system' | 'user';
}>();

const emit = defineEmits<{
  (e: 'close'): void;
  (e: 'added', attachment: Attachment): void;
}>();

const client = useHarnessClient();

const tree = ref<ContextNode | null>(null);
const treeError = ref<string | null>(null);
const loadingTree = ref(false);
const adding = ref(false);
const addError = ref<string | null>(null);
const selectedPath = ref<string | null>(null);
// When the user picks a folder, we attach the whole module directory instead
// of a single file. selectedKind tracks which was last chosen so the footer +
// Attach action branch correctly. The backend AttachModule enforces that the
// directory is a module (has a context.md / agents.md root) and rejects it
// otherwise — surfaced as addError.
const selectedKind = ref<'file' | 'folder' | null>(null);

async function loadTree() {
  loadingTree.value = true;
  treeError.value = null;
  try {
    tree.value = await client.contexts.list();
  } catch (err) {
    tree.value = null;
    treeError.value = err instanceof Error ? err.message : String(err);
  } finally {
    loadingTree.value = false;
  }
}

async function onSelect(path: string) {
  selectedPath.value = path;
  selectedKind.value = 'file';
}

async function onSelectFolder(path: string) {
  selectedPath.value = path;
  selectedKind.value = 'folder';
}

async function onAdd() {
  if (!selectedPath.value || adding.value) return;
  adding.value = true;
  addError.value = null;
  try {
    let attached;
    if (selectedKind.value === 'folder') {
      // Attach the whole module directory (root + always-files eager, rest
      // on-demand). AttachModule rejects a non-module dir (no root file).
      attached = await client.contexts.attachModule(
        props.scopeKind,
        props.scopeId,
        selectedPath.value,
      );
    } else {
      const content = await client.contexts.get(selectedPath.value);
      attached = await client.attachments.add({
        scopeKind: props.scopeKind,
        scopeId: props.scopeId,
        contentSource: `library:${selectedPath.value}`,
        content,
        kind: props.attachmentKind ?? 'system',
      });
    }
    emit('added', attached as Attachment);
    selectedPath.value = null;
    selectedKind.value = null;
    emit('close');
  } catch (err) {
    addError.value = err instanceof Error ? err.message : String(err);
  } finally {
    adding.value = false;
  }
}

function close() {
  if (adding.value) return;
  selectedPath.value = null;
  selectedKind.value = null;
  addError.value = null;
  emit('close');
}

onMounted(() => {
  if (props.open) void loadTree();
});

watch(
  () => props.open,
  (open) => {
    if (open) {
      selectedPath.value = null;
      selectedKind.value = null;
      addError.value = null;
      void loadTree();
    }
  },
);
</script>

<template>
  <div
    v-if="open"
    class="fixed inset-0 z-50 flex items-center justify-center"
    role="dialog"
    aria-modal="true"
    aria-label="Pick a context from the library"
    data-testid="attachment-tree-picker"
  >
    <div class="absolute inset-0 bg-modal-overlay" @click="close" />
    <div
      class="relative z-10 w-[480px] max-w-[90vw] max-h-[80vh] overflow-hidden flex flex-col rounded-md border border-border-muted bg-surface-0 shadow-lg"
    >
      <header class="flex items-center justify-between border-b border-border-muted px-4 py-3">
        <div>
          <div class="text-[11px] uppercase tracking-[0.18em] text-ink-subtle">
            ADD CONTEXT
          </div>
          <h2 class="mt-0.5 font-ui text-base font-semibold text-ink">
            Pick from library
          </h2>
        </div>
        <button
          type="button"
          class="text-ink-dim hover:text-ink"
          aria-label="Close"
          @click="close"
        >
          <X :size="16" />
        </button>
      </header>

      <div class="flex-1 overflow-y-auto px-3 py-3">
        <div
          v-if="loadingTree"
          class="px-2 py-3 font-ui text-xs text-ink-muted"
          data-testid="attachment-tree-loading"
        >
          Loading library…
        </div>
        <div
          v-else-if="treeError"
          class="px-2 py-2 font-ui text-xs text-signal-danger"
          role="alert"
          data-testid="attachment-tree-error"
        >
          {{ treeError }}
        </div>
        <div
          v-else-if="!tree || !tree.children || tree.children.length === 0"
          class="px-2 py-3 font-ui text-xs text-ink-muted"
          data-testid="attachment-tree-empty"
        >
          The library is empty. Drop a markdown file into your contexts
          folder, then re-open this picker.
        </div>
        <ContextTree
          v-else
          :node="tree"
          :selected-path="selectedPath"
          :is-root="true"
          @select="onSelect"
          @select-folder="onSelectFolder"
        />
      </div>

      <footer class="flex items-center justify-between gap-2 border-t border-border-muted px-4 py-3">
        <div class="flex-1 min-w-0">
          <span
            v-if="selectedPath"
            class="block font-mono text-[11px] text-ink truncate"
            :title="selectedPath"
            data-testid="attachment-tree-selected"
          >
            {{ selectedPath }}
          </span>
          <span v-else class="font-ui text-[11px] text-ink-dim">
            Select a file, or a folder to attach a whole module
          </span>
          <div
            v-if="addError"
            class="mt-1 font-ui text-[11px] text-signal-danger"
            role="alert"
            data-testid="attachment-tree-add-error"
          >
            {{ addError }}
          </div>
        </div>
        <div class="flex items-center gap-2">
          <button
            type="button"
            class="rounded-sm px-3 py-1 font-ui text-[12px] text-ink-muted hover:text-ink"
            :disabled="adding"
            @click="close"
          >
            Cancel
          </button>
          <button
            type="button"
            class="rounded-sm border border-accent-hairline px-3 py-1 font-ui text-[12px] text-accent hover:bg-accent-glow disabled:opacity-50"
            :disabled="!selectedPath || adding"
            data-testid="attachment-tree-add"
            @click="onAdd"
          >
            {{ adding ? 'Adding…' : selectedKind === 'folder' ? 'Attach module' : 'Attach' }}
          </button>
        </div>
      </footer>
    </div>
  </div>
</template>
