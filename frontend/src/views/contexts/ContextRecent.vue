<script setup lang="ts">
/**
 * ContextRecent — top-N most-recently-applied files.
 *
 * The list is the JSON LRU stored at <root>/.recent.json. WP01 ships
 * the read path; the call site that records "applied as attachment"
 * lands in WP03+ once the attachments table exists. Until then the
 * list stays empty for fresh installs — the empty-state hint
 * communicates the eventual UX.
 */
defineProps<{
  paths: readonly string[];
  selectedPath?: string | null;
}>();

const emit = defineEmits<{
  (e: 'select', path: string): void;
}>();

function basename(p: string): string {
  const i = p.lastIndexOf('/');
  return i === -1 ? p : p.slice(i + 1);
}
</script>

<template>
  <aside
    class="border-l border-border-muted bg-surface-0 flex flex-col"
    aria-label="Recently applied"
  >
    <header class="px-4 py-2 border-b border-border-muted">
      <span class="font-ui text-[10px] uppercase tracking-[0.18em] text-ink-subtle">
        Recently applied
      </span>
    </header>
    <ul v-if="paths.length > 0" class="px-2 py-2 space-y-0.5">
      <li v-for="p in paths" :key="p">
        <button
          type="button"
          class="w-full text-left px-2 py-1 rounded-sm text-sm font-ui transition-fast"
          :class="
            selectedPath === p
              ? 'text-ink bg-surface-2 ring-1 ring-accent-hairline'
              : 'text-ink-muted hover:text-ink hover:bg-surface-2'
          "
          :data-testid="`context-recent-${p}`"
          @click="emit('select', p)"
        >
          <div class="truncate">{{ basename(p) }}</div>
          <div class="font-mono text-[10px] text-ink-subtle truncate">{{ p }}</div>
        </button>
      </li>
    </ul>
    <p
      v-else
      class="px-4 py-3 font-ui text-[12px] text-ink-subtle"
    >
      Files you attach to sessions will appear here.
    </p>
  </aside>
</template>
