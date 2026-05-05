<script setup lang="ts">
/**
 * SessionTreeRow — recursive sidebar row for a session that may have
 * branch children. Renders a chevron when children exist, indents by
 * depth * 12 px, and shows a GitBranch icon for branch sessions.
 *
 * branching-ux-polish-01KQ8TD7 WP03.
 */
import { computed } from 'vue';
import { ChevronDown, ChevronRight, GitBranch } from './icons';
import type { Session } from '@/lib/types';

const props = defineProps<{
  session: Session;
  depth: number;
  hasChildren: boolean;
  isCollapsed: boolean;
  isActive: boolean;
  isRenaming: boolean;
  maxDepth: number;
  renameDraft?: string;
  draggable?: boolean;
}>();

const emit = defineEmits<{
  (e: 'open', id: string): void;
  (e: 'toggle-collapse', id: string): void;
  (e: 'commit-rename', id: string): void;
  (e: 'cancel-rename'): void;
  (e: 'update:renameDraft', value: string): void;
}>();

/** Indentation in pixels: depth capped at maxDepth, 12 px per level. */
const indentPx = computed(() => Math.min(props.depth, props.maxDepth) * 12);

const isBranch = computed(() => !!props.session.parentSessionId);

function setRenameInputRef(el: Element | null) {
  if (el instanceof HTMLInputElement) {
    el.focus();
    el.select();
  }
}
</script>

<template>
  <li
    class="flex items-center group relative"
    :style="{ paddingLeft: `${indentPx}px` }"
    :data-testid="`session-row-${session.id}`"
    :data-session-id="session.id"
    :data-depth="depth"
    :draggable="draggable && !isRenaming"
  >
    <!-- Rename input (when isRenaming) -->
    <template v-if="isRenaming">
      <input
        :ref="setRenameInputRef"
        :value="renameDraft"
        class="flex-1 min-w-0 px-2 py-1 rounded-sm border border-accent bg-surface-2 text-sm font-ui text-ink focus:outline-none"
        :data-testid="`rename-session-input-${session.id}`"
        @input="emit('update:renameDraft', ($event.target as HTMLInputElement).value)"
        @keydown.enter="emit('commit-rename', session.id)"
        @keydown.escape="emit('cancel-rename')"
        @blur="emit('commit-rename', session.id)"
      />
    </template>
    <template v-else>
      <!-- Chevron (only when children exist) -->
      <button
        v-if="hasChildren"
        type="button"
        class="flex-shrink-0 w-4 h-5 flex items-center justify-center text-ink-dim hover:text-ink"
        :aria-expanded="!isCollapsed"
        :aria-label="isCollapsed ? 'Expand branch children' : 'Collapse branch children'"
        :data-testid="`branch-collapse-toggle-${session.id}`"
        @click.stop="emit('toggle-collapse', session.id)"
      >
        <ChevronDown v-if="!isCollapsed" :size="12" />
        <ChevronRight v-else :size="12" />
      </button>
      <!-- Spacer when no chevron (maintain alignment) -->
      <span v-else class="flex-shrink-0 w-4" />

      <!-- Session name button -->
      <button
        type="button"
        class="flex flex-1 min-w-0 items-center gap-1 px-1.5 py-1 rounded-sm text-left transition-fast text-[12px] leading-[1.35]"
        :class="[
          isActive
            ? 'bg-surface-3 text-ink font-medium'
            : 'text-ink-muted hover:bg-surface-2 hover:text-ink',
        ]"
        :data-testid="`open-session-${session.id}`"
        @click="emit('open', session.id)"
      >
        <!-- Branch icon for non-root sessions -->
        <GitBranch
          v-if="isBranch"
          :size="11"
          class="flex-shrink-0 text-ink-dim"
          aria-hidden="true"
        />
        <span
          class="truncate"
          :class="[
            session.autoTitled ? 'session-row__name--auto italic text-ink-subtle' : '',
          ]"
          :data-testid="session.autoTitled ? `auto-titled-name-${session.id}` : undefined"
        >
          {{ session.branchTitle || session.name }}
        </span>
      </button>

      <!-- Action buttons (rename / delete) — appear on hover -->
      <div class="flex-shrink-0 flex items-center opacity-0 group-hover:opacity-100 transition-fast">
        <slot name="actions" />
      </div>
    </template>
  </li>
</template>
