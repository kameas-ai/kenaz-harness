<script setup lang="ts">
/**
 * SlashAutocomplete — dropdown the chat composer renders when the
 * trimmed input begins with `/`. Stateless: the parent owns the full
 * command list and the active index; this component only renders.
 *
 * The parent is responsible for keyboard navigation (Up/Down/Tab/
 * Enter/Escape) so the same handlers stay co-located with the
 * existing `@filepath` autocomplete and the textarea event flow.
 *
 * Filtering happens here (cheap O(n) over the static command list)
 * because the dropdown needs both the filtered list (for rendering)
 * AND awareness of the filtered length (so the parent's keyboard
 * navigation can clamp `activeIndex`). Parent reads `filtered` via
 * a defineExpose handle.
 */

import { computed } from 'vue';
import type { SlashCommandInfo } from '@/lib/types';

const props = defineProps<{
  commands: readonly SlashCommandInfo[];
  /** raw text typed after the leading `/`, e.g. `mod` for `/mod`. */
  query: string;
  activeIndex: number;
}>();

const emit = defineEmits<{
  (e: 'select', name: string): void;
}>();

/**
 * Filtered command list. Match policy: case-insensitive prefix on
 * `name`. Empty query returns every command.
 */
const filtered = computed<readonly SlashCommandInfo[]>(() => {
  const q = props.query.trim().toLowerCase();
  if (q === '') return props.commands;
  return props.commands.filter((c) => c.name.toLowerCase().startsWith(q));
});

defineExpose({ filtered });

function pick(name: string) {
  emit('select', name);
}
</script>

<template>
  <ul
    v-if="filtered.length > 0"
    class="absolute left-0 right-0 bottom-full mb-1 z-30 max-h-60 overflow-y-auto rounded-md border border-border-muted bg-surface-1 shadow-lg"
    data-testid="slash-suggestions"
    role="listbox"
    aria-label="Slash command suggestions"
  >
    <li
      v-for="(cmd, i) in filtered"
      :key="cmd.name"
      class="px-3 py-1.5 cursor-pointer flex items-baseline gap-2"
      :class="i === activeIndex ? 'bg-surface-2 text-accent' : 'text-ink hover:bg-surface-2'"
      :data-testid="`slash-option-${i}`"
      :aria-selected="i === activeIndex"
      role="option"
      @mousedown.prevent="pick(cmd.name)"
    >
      <span class="font-mono text-[12px]">/{{ cmd.name }}</span>
      <span class="font-ui text-[11px] text-ink-dim flex-1 truncate">
        {{ cmd.description }}
      </span>
      <span
        v-if="cmd.comingSoon"
        class="font-ui text-[10px] uppercase tracking-[0.18em] text-signal-warn"
        data-testid="slash-coming-soon-tag"
      >
        coming soon
      </span>
    </li>
  </ul>
</template>
