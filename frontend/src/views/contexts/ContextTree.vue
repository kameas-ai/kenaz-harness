<script setup lang="ts">
/**
 * ContextTree — recursive folder/file tree for the Context Library.
 *
 * Folders expand on click; files emit a `select` event so the parent
 * view loads the preview. Selected file rows highlight via the
 * `selectedPath` prop. Rename and delete (WP04 layers on attachment
 * scope badges) are inline row affordances, revealed on hover/focus —
 * drag-to-reparent is not built; see the note on `rename`/`delete`
 * below (controls-and-readouts UNIT-11 / WP16, spec §1.10).
 *
 * Lazy-render: subtrees only mount when their folder is expanded so a
 * 1000-file library doesn't pay the render cost upfront. The plan
 * mentions a >200-row virtualisation hop — single-pass virtualisation
 * is overkill for v1; the lazy branch handles the common case.
 */
import { nextTick, ref, computed } from 'vue';
import { ChevronRight, FileText, Folder, Pencil, Trash2 } from '@/shell/icons';
import type { ContextNode } from '@/lib/types';

const props = defineProps<{
  node: ContextNode;
  selectedPath?: string | null;
  /** Internal — controls whether the wrapping <ul> shows the root row.
   *  The view hides the root and renders children directly. */
  isRoot?: boolean;
}>();

const emit = defineEmits<{
  (e: 'select', path: string): void;
  // Emitted when a folder row is clicked (in addition to expand/collapse).
  // Pickers that support attaching a whole module directory listen for this;
  // the library view ignores it, so file-selection behaviour is unchanged.
  (e: 'select-folder', path: string): void;
  // Emitted when the user confirms an inline rename (Enter) with a
  // non-empty, changed name. The host owns the client call and the
  // sibling-path computation — this component only knows the node.
  (e: 'rename', payload: { path: string; newName: string }): void;
  // Emitted when the user clicks the delete affordance. The host owns
  // confirmation and the client call.
  (e: 'delete', path: string): void;
}>();

// Top-level folder under the root expands by default so a fresh user
// sees their files without having to click; nested folders default
// closed for an uncluttered view. The root itself is never rendered.
const expanded = ref(props.isRoot === true);

function toggle() {
  if (props.node.kind === 'folder') {
    expanded.value = !expanded.value;
    emit('select-folder', props.node.path);
  } else {
    emit('select', props.node.path);
  }
}

function onChildSelect(path: string) {
  emit('select', path);
}

function onChildSelectFolder(path: string) {
  emit('select-folder', path);
}

function onChildRename(payload: { path: string; newName: string }) {
  emit('rename', payload);
}

function onChildDelete(path: string) {
  emit('delete', path);
}

const isFolder = computed(() => props.node.kind === 'folder');
const isSelected = computed(
  () => props.selectedPath === props.node.path && props.node.kind === 'file',
);

// ── inline rename ────────────────────────────────────────────────────────
const renaming = ref(false);
const renameValue = ref('');
const renameInputRef = ref<HTMLInputElement | null>(null);

function beginRename() {
  renameValue.value = props.node.name;
  renaming.value = true;
  void nextTick(() => {
    renameInputRef.value?.focus();
    renameInputRef.value?.select();
  });
}

function cancelRename() {
  renaming.value = false;
  renameValue.value = '';
}

function confirmRename() {
  const trimmed = renameValue.value.trim();
  if (!trimmed || trimmed === props.node.name) {
    cancelRename();
    return;
  }
  emit('rename', { path: props.node.path, newName: trimmed });
  renaming.value = false;
}

function onDeleteClick() {
  emit('delete', props.node.path);
}
</script>

<template>
  <li v-if="!isRoot" class="select-none group/row">
    <div
      v-if="renaming"
      class="flex items-center gap-1.5 px-2 py-1"
      :data-testid="`context-rename-row-${node.path}`"
    >
      <span class="shrink-0 w-3" />
      <span class="shrink-0">
        <Folder v-if="isFolder" :size="13" />
        <FileText v-else :size="13" />
      </span>
      <input
        ref="renameInputRef"
        v-model="renameValue"
        type="text"
        spellcheck="false"
        autocomplete="off"
        aria-label="Rename"
        class="min-w-0 flex-1 rounded-sm border border-border-muted bg-surface-1 px-1.5 py-0.5 font-ui text-[12px] text-ink focus:border-accent focus:outline-none"
        :data-testid="`context-rename-input-${node.path}`"
        @keydown.enter.prevent="confirmRename"
        @keydown.esc.prevent="cancelRename"
        @blur="cancelRename"
      />
    </div>
    <div
      v-else
      class="w-full flex items-center gap-1 px-2 py-1 rounded-sm text-sm font-ui transition-fast"
      :class="
        isSelected
          ? 'text-ink bg-surface-2 ring-1 ring-accent-hairline'
          : 'text-ink-muted hover:text-ink hover:bg-surface-2'
      "
    >
      <button
        type="button"
        class="min-w-0 flex-1 flex items-center gap-1.5 text-left"
        :data-testid="`context-node-${node.path}`"
        @click="toggle"
      >
        <span v-if="isFolder" class="shrink-0">
          <ChevronRight
            :size="12"
            :class="expanded ? 'rotate-90 transition-transform' : 'transition-transform'"
          />
        </span>
        <span v-else class="shrink-0 w-3" />
        <span class="shrink-0">
          <Folder v-if="isFolder" :size="13" />
          <FileText v-else :size="13" />
        </span>
        <span class="truncate">{{ node.name }}</span>
      </button>
      <span
        class="shrink-0 flex items-center gap-0.5 opacity-0 group-hover/row:opacity-100 focus-within:opacity-100"
      >
        <button
          type="button"
          class="p-0.5 rounded-sm text-ink-subtle hover:text-accent hover:bg-surface-3"
          aria-label="Rename"
          :data-testid="`context-rename-btn-${node.path}`"
          @click.stop="beginRename"
        >
          <Pencil :size="11" />
        </button>
        <button
          type="button"
          class="p-0.5 rounded-sm text-ink-subtle hover:text-signal-danger hover:bg-surface-3"
          aria-label="Delete"
          :data-testid="`context-delete-btn-${node.path}`"
          @click.stop="onDeleteClick"
        >
          <Trash2 :size="11" />
        </button>
      </span>
    </div>
    <ul v-if="isFolder && expanded" class="pl-4 space-y-0.5">
      <ContextTree
        v-for="child in node.children"
        :key="child.path"
        :node="child"
        :selected-path="selectedPath"
        @select="onChildSelect"
        @select-folder="onChildSelectFolder"
        @rename="onChildRename"
        @delete="onChildDelete"
      />
    </ul>
  </li>
  <ul v-else class="space-y-0.5" data-testid="context-tree-root">
    <ContextTree
      v-for="child in node.children"
      :key="child.path"
      :node="child"
      :selected-path="selectedPath"
      @select="onChildSelect"
      @select-folder="onChildSelectFolder"
      @rename="onChildRename"
      @delete="onChildDelete"
    />
  </ul>
</template>
