<script setup lang="ts">
/**
 * ContextTree — recursive folder/file tree for the Context Library.
 *
 * Folders expand on click; files emit a `select` event so the parent
 * view loads the preview. Selected file rows highlight via the
 * `selectedPath` prop. The component is intentionally minimal — WP05
 * adds drag-to-reparent and inline rename; WP04 layers on attachment
 * scope badges.
 *
 * Lazy-render: subtrees only mount when their folder is expanded so a
 * 1000-file library doesn't pay the render cost upfront. The plan
 * mentions a >200-row virtualisation hop — single-pass virtualisation
 * is overkill for v1; the lazy branch handles the common case.
 */
import { ref, computed } from 'vue';
import { ChevronRight, FileText, Folder } from '@/shell/icons';
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

const isFolder = computed(() => props.node.kind === 'folder');
const isSelected = computed(
  () => props.selectedPath === props.node.path && props.node.kind === 'file',
);
</script>

<template>
  <li v-if="!isRoot" class="select-none">
    <button
      type="button"
      class="w-full flex items-center gap-1.5 px-2 py-1 rounded-sm text-left text-sm font-ui transition-fast"
      :class="
        isSelected
          ? 'text-ink bg-surface-2 ring-1 ring-accent-hairline'
          : 'text-ink-muted hover:text-ink hover:bg-surface-2'
      "
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
    <ul v-if="isFolder && expanded" class="pl-4 space-y-0.5">
      <ContextTree
        v-for="child in node.children"
        :key="child.path"
        :node="child"
        :selected-path="selectedPath"
        @select="onChildSelect"
        @select-folder="onChildSelectFolder"
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
    />
  </ul>
</template>
